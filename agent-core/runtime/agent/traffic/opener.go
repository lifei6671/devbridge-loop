package traffic

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

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

// OpenerOptions 定义 TrafficRuntime 依赖。
type OpenerOptions struct {
	Selector EndpointSelector
	Dialer   UpstreamDialer
	Relay    RelayPump
}

// HandleResult 描述单次 TrafficOpen 的执行结果。
type HandleResult struct {
	TrafficID string
	Endpoint  Endpoint
}

// Opener 负责处理 TrafficOpen、拨号、ack 与 relay 生命周期。
type Opener struct {
	selector EndpointSelector
	dialer   UpstreamDialer
	relay    RelayPump
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
	return &Opener{
		selector: options.Selector,
		dialer:   options.Dialer,
		relay:    options.Relay,
	}, nil
}

// Handle 执行单次 TrafficOpen 全流程（open -> dial -> ack -> relay -> close/reset）。
func (opener *Opener) Handle(ctx context.Context, handoff OpenHandoff, tunnel TunnelIO) (HandleResult, error) {
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
	if handoff.Lease != nil {
		// runtime 结束时归还 reader 所有权。
		defer handoff.Lease.Release()
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
		return HandleResult{}, errors.Join(err, ackError)
	}

	selectedEndpoint, err := opener.selector.SelectEndpoint(normalizedContext, handoff.Open.ServiceID, copyStringMap(handoff.Open.EndpointSelectionHint))
	if err != nil {
		ackError := opener.writeOpenAck(terminalWriteContext, tunnel, pb.TrafficOpenAck{
			TrafficID:    handoff.Open.TrafficID,
			Success:      false,
			ErrorCode:    ltfperrors.CodeTrafficOpenRejected,
			ErrorMessage: err.Error(),
		})
		return HandleResult{}, errors.Join(fmt.Errorf("handle traffic open: select endpoint: %w", err), ackError)
	}

	upstream, err := opener.dialer.Dial(normalizedContext, selectedEndpoint)
	if err != nil {
		ackError := opener.writeOpenAck(terminalWriteContext, tunnel, pb.TrafficOpenAck{
			TrafficID:    handoff.Open.TrafficID,
			Success:      false,
			ErrorCode:    ltfperrors.CodeTrafficOpenRejected,
			ErrorMessage: err.Error(),
		})
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
		return HandleResult{}, fmt.Errorf("handle traffic open: write open ack: %w", err)
	}

	relayErr := opener.relay.Relay(normalizedContext, tunnel, upstream, handoff.Open.TrafficID)
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
		return HandleResult{}, errors.Join(fmt.Errorf("handle traffic open: relay: %w", relayErr), writeResetErr)
	}

	closePayload := pb.StreamPayload{
		Close: &pb.TrafficClose{
			TrafficID: handoff.Open.TrafficID,
			Reason:    "relay_completed",
		},
	}
	if err := tunnel.WritePayload(normalizedContext, closePayload); err != nil {
		return HandleResult{}, fmt.Errorf("handle traffic open: write close: %w", err)
	}
	return HandleResult{
		TrafficID: handoff.Open.TrafficID,
		Endpoint:  selectedEndpoint,
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
