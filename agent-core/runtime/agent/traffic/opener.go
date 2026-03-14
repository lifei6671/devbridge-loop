package traffic

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/lifei6671/devbridge-loop/agent-core/runtime/agent/obs"
	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

var (
	// ErrTrafficRuntimeDependencyMissing 表示 TrafficRuntime 关键依赖缺失。
	ErrTrafficRuntimeDependencyMissing = errors.New("traffic runtime dependency missing")
)

const (
	// OpenAckMetadataActualEndpointID 用于回传实际选中的 endpoint_id。
	OpenAckMetadataActualEndpointID = "actual_endpoint_id"
	// OpenAckMetadataActualEndpointAddr 用于回传实际选中的 endpoint_addr。
	OpenAckMetadataActualEndpointAddr = "actual_endpoint_addr"

	// ResetCodeCanceled 表示 traffic 因取消信号终止。
	ResetCodeCanceled = "TRAFFIC_CANCELED"
	// ResetCodeRelayFailed 表示 traffic relay 过程失败。
	ResetCodeRelayFailed = "TRAFFIC_RELAY_FAILED"
)

// Endpoint 描述 Agent 侧最终选中的 upstream endpoint。
type Endpoint struct {
	ID   string
	Addr string
}

// EndpointSelector 定义 service -> endpoint 的本地选择能力。
type EndpointSelector interface {
	// SelectEndpoint 根据 service 与 hint 选择实际 endpoint。
	SelectEndpoint(ctx context.Context, serviceID string, hint map[string]string) (Endpoint, error)
}

// UpstreamDialer 定义到本地 upstream 的拨号能力。
type UpstreamDialer interface {
	// Dial 建立到目标 endpoint 的连接。
	Dial(ctx context.Context, endpoint Endpoint) (io.ReadWriteCloser, error)
}

// TunnelDoneSignal 定义 tunnel 可选的关闭通知能力。
//
// 说明：
// 1. 用于 pre-open 阶段把“对端已取消/连接已关闭”信号映射到 ctx cancel。
// 2. 非强制接口；未实现时 opener 退化为仅依赖调用方 context。
type TunnelDoneSignal interface {
	Done() <-chan struct{}
}

// OpenerOptions 定义 TrafficRuntime 依赖。
type OpenerOptions struct {
	Selector EndpointSelector
	Dialer   UpstreamDialer
	Relay    RelayPump
	Metrics  *obs.Metrics
}

// HandleResult 描述单次 TrafficOpen 的执行结果。
type HandleResult struct {
	TrafficID             string
	Endpoint              Endpoint
	OpenAckLatencyMS      uint64
	UpstreamDialLatencyMS uint64
}

// Opener 负责处理 TrafficOpen、拨号、ack 与 relay 生命周期。
type Opener struct {
	selector EndpointSelector
	dialer   UpstreamDialer
	relay    RelayPump
	metrics  *obs.Metrics
}

// NewOpener 创建 traffic runtime opener。
func NewOpener(options OpenerOptions) (*Opener, error) {
	if options.Selector == nil {
		return nil, fmt.Errorf("new opener: %w: nil selector", ErrTrafficRuntimeDependencyMissing)
	}
	if options.Dialer == nil {
		return nil, fmt.Errorf("new opener: %w: nil dialer", ErrTrafficRuntimeDependencyMissing)
	}
	if options.Relay == nil {
		return nil, fmt.Errorf("new opener: %w: nil relay", ErrTrafficRuntimeDependencyMissing)
	}
	metrics := options.Metrics
	if metrics == nil {
		metrics = obs.DefaultMetrics
	}
	return &Opener{
		selector: options.Selector,
		dialer:   options.Dialer,
		relay:    options.Relay,
		metrics:  metrics,
	}, nil
}

// Handle 执行单次 TrafficOpen 全流程（open -> dial -> ack -> relay -> close/reset）。
func (opener *Opener) Handle(ctx context.Context, handoff OpenHandoff, tunnel TunnelIO) (HandleResult, error) {
	handleStartedAt := time.Now()
	if opener == nil {
		if handoff.Lease != nil {
			handoff.Lease.Release()
		}
		return HandleResult{}, fmt.Errorf("handle traffic open: %w", ErrTrafficRuntimeDependencyMissing)
	}
	if tunnel == nil {
		if handoff.Lease != nil {
			handoff.Lease.Release()
		}
		return HandleResult{}, fmt.Errorf("handle traffic open: %w: nil tunnel io", ErrTrafficRuntimeDependencyMissing)
	}
	normalizedContext := ctx
	if normalizedContext == nil {
		// nil context 时回落到 Background，避免阻塞流程 panic。
		normalizedContext = context.Background()
	}
	terminalWriteContext := context.WithoutCancel(normalizedContext)
	executionContext, cancelExecution := context.WithCancel(normalizedContext)
	defer cancelExecution()
	stopTunnelCloseWatch := watchTunnelCloseSignal(executionContext, cancelExecution, tunnel)
	defer stopTunnelCloseWatch()
	if handoff.Lease != nil {
		// runtime 结束时归还 reader 所有权。
		defer handoff.Lease.Release()
	}
	baseLogFields := obs.LogFields{
		TrafficID: strings.TrimSpace(handoff.Open.TrafficID),
		ServiceID: strings.TrimSpace(handoff.Open.ServiceID),
		TunnelID:  strings.TrimSpace(handoff.TunnelID),
	}
	if err := validateTrafficOpen(handoff.Open); err != nil {
		errorCode := ltfperrors.ExtractCode(err)
		if strings.TrimSpace(errorCode) == "" {
			errorCode = ltfperrors.CodeInvalidPayload
		}
		ackError := opener.writeOpenAck(terminalWriteContext, tunnel, pb.TrafficOpenAck{
			TrafficID:    strings.TrimSpace(handoff.Open.TrafficID),
			Success:      false,
			ErrorCode:    errorCode,
			ErrorMessage: err.Error(),
		})
		opener.observeOpenAckLatency(handleStartedAt)
		log.Printf(
			"agent traffic open rejected event=validate_open_failed %s err=%v",
			obs.FormatLogFields(baseLogFields),
			err,
		)
		return HandleResult{}, errors.Join(err, ackError)
	}

	selectedEndpoint, err := opener.selector.SelectEndpoint(executionContext, handoff.Open.ServiceID, copyStringMap(handoff.Open.EndpointSelectionHint))
	if err != nil {
		ackError := opener.writeOpenAck(terminalWriteContext, tunnel, pb.TrafficOpenAck{
			TrafficID: handoff.Open.TrafficID,
			Success:   false,
			// endpoint 选择失败属于 open reject 语义。
			ErrorCode:    ltfperrors.CodeTrafficOpenRejected,
			ErrorMessage: err.Error(),
		})
		opener.observeOpenAckLatency(handleStartedAt)
		log.Printf(
			"agent traffic open rejected event=select_endpoint_failed %s err=%v",
			obs.FormatLogFields(baseLogFields),
			err,
		)
		return HandleResult{}, errors.Join(fmt.Errorf("handle traffic open: select endpoint: %w", err), ackError)
	}

	dialStartedAt := time.Now()
	upstream, err := opener.dialer.Dial(executionContext, selectedEndpoint)
	dialLatencyMS := opener.observeDialLatency(dialStartedAt)
	if err != nil {
		ackError := opener.writeOpenAck(terminalWriteContext, tunnel, pb.TrafficOpenAck{
			TrafficID: handoff.Open.TrafficID,
			Success:   false,
			// 拨号失败单独打标，供 Bridge 做 connector dial 分类。
			ErrorCode:    ltfperrors.CodeConnectorDialFailed,
			ErrorMessage: err.Error(),
		})
		_ = opener.observeOpenAckLatency(handleStartedAt)
		log.Printf(
			"agent traffic open rejected event=dial_upstream_failed %s err=%v",
			obs.FormatLogFields(obs.LogFields{
				TrafficID: strings.TrimSpace(handoff.Open.TrafficID),
				ServiceID: strings.TrimSpace(handoff.Open.ServiceID),
				TunnelID:  strings.TrimSpace(handoff.TunnelID),
			}),
			err,
		)
		return HandleResult{}, errors.Join(fmt.Errorf("handle traffic open: dial upstream: %w", err), ackError)
	}
	defer func() {
		_ = upstream.Close()
	}()

	ack := pb.TrafficOpenAck{
		TrafficID: handoff.Open.TrafficID,
		Success:   true,
		Metadata: map[string]string{
			OpenAckMetadataActualEndpointID:   strings.TrimSpace(selectedEndpoint.ID),
			OpenAckMetadataActualEndpointAddr: strings.TrimSpace(selectedEndpoint.Addr),
		},
	}
	if err := opener.writeOpenAck(terminalWriteContext, tunnel, ack); err != nil {
		_ = opener.observeOpenAckLatency(handleStartedAt)
		log.Printf(
			"agent traffic open failed event=write_open_ack_failed %s err=%v",
			obs.FormatLogFields(obs.LogFields{
				TrafficID:          strings.TrimSpace(handoff.Open.TrafficID),
				ServiceID:          strings.TrimSpace(handoff.Open.ServiceID),
				ActualEndpointID:   strings.TrimSpace(selectedEndpoint.ID),
				ActualEndpointAddr: strings.TrimSpace(selectedEndpoint.Addr),
				TunnelID:           strings.TrimSpace(handoff.TunnelID),
			}),
			err,
		)
		return HandleResult{}, fmt.Errorf("handle traffic open: write open ack: %w", err)
	}
	openAckLatencyMS := opener.observeOpenAckLatency(handleStartedAt)

	relayErr := opener.relay.Relay(executionContext, tunnel, upstream, handoff.Open.TrafficID)
	if relayErr != nil {
		resetCode := ResetCodeRelayFailed
		if errors.Is(relayErr, context.Canceled) || errors.Is(relayErr, context.DeadlineExceeded) {
			resetCode = ResetCodeCanceled
		}
		resetPayload := pb.StreamPayload{
			Reset: &pb.TrafficReset{
				TrafficID:    handoff.Open.TrafficID,
				ErrorCode:    resetCode,
				ErrorMessage: relayErr.Error(),
			},
		}
		writeResetErr := tunnel.WritePayload(terminalWriteContext, resetPayload)
		log.Printf(
			"agent traffic terminal event=write_reset state=reset reset_code=%s %s err=%v",
			resetCode,
			obs.FormatLogFields(obs.LogFields{
				TrafficID:          strings.TrimSpace(handoff.Open.TrafficID),
				ServiceID:          strings.TrimSpace(handoff.Open.ServiceID),
				ActualEndpointID:   strings.TrimSpace(selectedEndpoint.ID),
				ActualEndpointAddr: strings.TrimSpace(selectedEndpoint.Addr),
				TunnelID:           strings.TrimSpace(handoff.TunnelID),
			}),
			errors.Join(relayErr, writeResetErr),
		)
		log.Printf(
			"agent traffic open failed event=relay_failed %s err=%v",
			obs.FormatLogFields(obs.LogFields{
				TrafficID:          strings.TrimSpace(handoff.Open.TrafficID),
				ServiceID:          strings.TrimSpace(handoff.Open.ServiceID),
				ActualEndpointID:   strings.TrimSpace(selectedEndpoint.ID),
				ActualEndpointAddr: strings.TrimSpace(selectedEndpoint.Addr),
				TunnelID:           strings.TrimSpace(handoff.TunnelID),
			}),
			relayErr,
		)
		return HandleResult{}, errors.Join(fmt.Errorf("handle traffic open: relay: %w", relayErr), writeResetErr)
	}

	closePayload := pb.StreamPayload{
		Close: &pb.TrafficClose{
			TrafficID: handoff.Open.TrafficID,
			Reason:    "relay_completed",
		},
	}
	if err := tunnel.WritePayload(executionContext, closePayload); err != nil {
		log.Printf(
			"agent traffic open failed event=write_close_failed %s err=%v",
			obs.FormatLogFields(obs.LogFields{
				TrafficID:          strings.TrimSpace(handoff.Open.TrafficID),
				ServiceID:          strings.TrimSpace(handoff.Open.ServiceID),
				ActualEndpointID:   strings.TrimSpace(selectedEndpoint.ID),
				ActualEndpointAddr: strings.TrimSpace(selectedEndpoint.Addr),
				TunnelID:           strings.TrimSpace(handoff.TunnelID),
			}),
			err,
		)
		return HandleResult{}, fmt.Errorf("handle traffic open: write close: %w", err)
	}
	log.Printf(
		"agent traffic terminal event=write_close state=closed reason=relay_completed %s",
		obs.FormatLogFields(obs.LogFields{
			TrafficID:          strings.TrimSpace(handoff.Open.TrafficID),
			ServiceID:          strings.TrimSpace(handoff.Open.ServiceID),
			ActualEndpointID:   strings.TrimSpace(selectedEndpoint.ID),
			ActualEndpointAddr: strings.TrimSpace(selectedEndpoint.Addr),
			TunnelID:           strings.TrimSpace(handoff.TunnelID),
		}),
	)
	return HandleResult{
		TrafficID:             handoff.Open.TrafficID,
		Endpoint:              selectedEndpoint,
		OpenAckLatencyMS:      openAckLatencyMS,
		UpstreamDialLatencyMS: dialLatencyMS,
	}, nil
}

func (opener *Opener) writeOpenAck(ctx context.Context, tunnel TunnelIO, ack pb.TrafficOpenAck) error {
	payload := pb.StreamPayload{OpenAck: &ack}
	if err := tunnel.WritePayload(ctx, payload); err != nil {
		return fmt.Errorf("write open ack: %w", err)
	}
	return nil
}

func validateTrafficOpen(open pb.TrafficOpen) error {
	normalizedTrafficID := strings.TrimSpace(open.TrafficID)
	if normalizedTrafficID == "" {
		return ltfperrors.New(ltfperrors.CodeMissingRequiredField, "trafficId is required")
	}
	if strings.TrimSpace(open.ServiceID) == "" {
		return ltfperrors.New(ltfperrors.CodeTrafficInvalidServiceID, "serviceId is required")
	}
	return nil
}

func copyStringMap(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	copied := make(map[string]string, len(source))
	for key, value := range source {
		copied[key] = value
	}
	return copied
}

// watchTunnelCloseSignal 监听 tunnel Done 信号并主动取消执行上下文。
func watchTunnelCloseSignal(ctx context.Context, cancel context.CancelFunc, tunnel TunnelIO) func() {
	doneSignal, ok := tunnel.(TunnelDoneSignal)
	if !ok {
		return func() {}
	}
	doneChannel := doneSignal.Done()
	if doneChannel == nil {
		return func() {}
	}
	stopChannel := make(chan struct{})
	var stopOnce sync.Once
	go func() {
		select {
		case <-ctx.Done():
		case <-stopChannel:
		case <-doneChannel:
			// 对端关闭 tunnel 时立刻取消 pre-open/dial/relay 执行上下文。
			cancel()
		}
	}()
	return func() {
		stopOnce.Do(func() {
			close(stopChannel)
		})
	}
}

// observeOpenAckLatency 记录单次 traffic 从进入 Handle 到写出 open_ack 的延迟。
func (opener *Opener) observeOpenAckLatency(startedAt time.Time) uint64 {
	latency := time.Since(startedAt)
	if latency < 0 {
		latency = 0
	}
	latencyMS := uint64(latency.Milliseconds())
	if opener == nil || opener.metrics == nil {
		return latencyMS
	}
	// open_ack 延迟统一按 Handle 入口时间到 ack 写出时间计算。
	opener.metrics.ObserveAgentTrafficOpenAckLatency(latency)
	return latencyMS
}

// observeDialLatency 记录单次 upstream dial 调用的耗时。
func (opener *Opener) observeDialLatency(startedAt time.Time) uint64 {
	latency := time.Since(startedAt)
	if latency < 0 {
		latency = 0
	}
	latencyMS := uint64(latency.Milliseconds())
	if opener == nil || opener.metrics == nil {
		return latencyMS
	}
	// upstream dial 延迟按 Dial 调用时长计算，便于定位上游建连瓶颈。
	opener.metrics.ObserveAgentUpstreamDialLatency(latency)
	return latencyMS
}
