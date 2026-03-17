package traffic

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/lifei6671/devbridge-loop/agent-core/runtime/agent/obs"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

var (
	// ErrRelayDependencyMissing 表示 relay 关键依赖缺失。
	ErrRelayDependencyMissing = errors.New("traffic relay dependency missing")
	// ErrRelayBackpressureTimeout 表示 relay 因反压超时终止。
	ErrRelayBackpressureTimeout = errors.New("traffic relay backpressure timeout")
	// ErrRelayResetByPeer 表示收到对端 reset。
	ErrRelayResetByPeer = errors.New("traffic relay reset by peer")
	// ErrRelayProtocolViolation 表示 relay 收到非法帧。
	ErrRelayProtocolViolation = errors.New("traffic relay protocol violation")
)

const (
	defaultRelayBufferFrames      = 32
	defaultRelayMaxDataFrameBytes = 16 * 1024
	defaultRelayBackpressure      = 5 * time.Second
)

// PayloadWriter 定义数据面消息写出能力。
type PayloadWriter interface {
	// WritePayload 写出一条 StreamPayload。
	WritePayload(ctx context.Context, payload pb.StreamPayload) error
}

// TunnelIO 统一描述 tunnel 的消息读写抽象。
type TunnelIO interface {
	PayloadReader
	PayloadWriter
}

// RelayPump 负责在 tunnel 与 upstream 之间执行 relay。
type RelayPump interface {
	// Relay 启动双向 relay，返回时表示该 traffic 已终止。
	Relay(ctx context.Context, tunnel TunnelIO, upstream io.ReadWriteCloser, trafficID string) error
}

// RelayFunc 允许使用函数形式实现 RelayPump。
type RelayFunc func(ctx context.Context, tunnel TunnelIO, upstream io.ReadWriteCloser, trafficID string) error

// Relay 以函数方式执行 relay。
func (relay RelayFunc) Relay(ctx context.Context, tunnel TunnelIO, upstream io.ReadWriteCloser, trafficID string) error {
	return relay(ctx, tunnel, upstream, trafficID)
}

// closeAckObserver 由 tunnel 侧可选实现，用于记录 relay 阶段已观测的 close_ack 事实。
type closeAckObserver interface {
	MarkCloseAckObserved(trafficID string)
}

type closeAckSentTracker interface {
	TryMarkCloseAckSent(trafficID string) bool
}

// StreamRelayOptions 描述默认 relay 行为配置。
type StreamRelayOptions struct {
	BufferFrames        int
	MaxDataFrameBytes   int
	BackpressureTimeout time.Duration
	Metrics             *obs.Metrics
}

// StreamRelay 是 Agent 侧默认双向 relay 实现。
//
// 约束：
// 1. 单 tunnel reader（readTunnelToUpstream），单 tunnel writer（writeUpstreamToTunnel）。
// 2. 双向 pump 均使用有界缓冲，禁止无界队列。
// 3. 当下游阻塞导致队列满且超过阈值，返回反压超时错误。
type StreamRelay struct {
	bufferFrames        int
	maxDataFrameBytes   int
	backpressureTimeout time.Duration
	metrics             *obs.Metrics
}

type relayWorkerKind uint8

const (
	relayWorkerReadTunnel relayWorkerKind = iota
	relayWorkerWriteUpstream
	relayWorkerReadUpstream
	relayWorkerWriteTunnel
)

type relayWorkerResult struct {
	worker relayWorkerKind
	err    error
}

// NewStreamRelay 创建默认 relay。
func NewStreamRelay(options StreamRelayOptions) *StreamRelay {
	bufferFrames := options.BufferFrames
	if bufferFrames <= 0 {
		bufferFrames = defaultRelayBufferFrames
	}
	maxDataFrameBytes := options.MaxDataFrameBytes
	if maxDataFrameBytes <= 0 {
		maxDataFrameBytes = defaultRelayMaxDataFrameBytes
	}
	backpressureTimeout := options.BackpressureTimeout
	if backpressureTimeout <= 0 {
		backpressureTimeout = defaultRelayBackpressure
	}
	metrics := options.Metrics
	if metrics == nil {
		// 未显式注入时回落默认指标容器，保证 runtime 指标链路可用。
		metrics = obs.DefaultMetrics
	}
	return &StreamRelay{
		bufferFrames:        bufferFrames,
		maxDataFrameBytes:   maxDataFrameBytes,
		backpressureTimeout: backpressureTimeout,
		metrics:             metrics,
	}
}

// Relay 启动 tunnel <-> upstream 双向 relay。
func (relay *StreamRelay) Relay(ctx context.Context, tunnel TunnelIO, upstream io.ReadWriteCloser, trafficID string) error {
	if relay == nil || tunnel == nil || upstream == nil {
		return ErrRelayDependencyMissing
	}
	normalizedTrafficID := strings.TrimSpace(trafficID)
	if normalizedTrafficID == "" {
		return ErrRelayDependencyMissing
	}
	normalizedContext := ctx
	if normalizedContext == nil {
		normalizedContext = context.Background()
	}
	relayContext, cancelRelay := context.WithCancel(normalizedContext)
	defer cancelRelay()

	tunnelToUpstream := make(chan []byte, relay.bufferFrames)
	upstreamToTunnel := make(chan []byte, relay.bufferFrames)

	resultChannel := make(chan relayWorkerResult, 4)
	var workerWaitGroup sync.WaitGroup
	workerWaitGroup.Add(4)
	go func() {
		defer workerWaitGroup.Done()
		defer close(tunnelToUpstream)
		resultChannel <- relayWorkerResult{
			worker: relayWorkerReadTunnel,
			err:    relay.readTunnelToUpstream(relayContext, tunnel, normalizedTrafficID, tunnelToUpstream),
		}
	}()
	go func() {
		defer workerWaitGroup.Done()
		resultChannel <- relayWorkerResult{
			worker: relayWorkerWriteUpstream,
			err:    relay.writeTunnelDataToUpstream(relayContext, upstream, tunnelToUpstream),
		}
	}()
	go func() {
		defer workerWaitGroup.Done()
		defer close(upstreamToTunnel)
		resultChannel <- relayWorkerResult{
			worker: relayWorkerReadUpstream,
			err:    relay.readUpstreamToTunnel(relayContext, upstream, upstreamToTunnel),
		}
	}()
	go func() {
		defer workerWaitGroup.Done()
		resultChannel <- relayWorkerResult{
			worker: relayWorkerWriteTunnel,
			err:    relay.writeUpstreamDataToTunnel(relayContext, tunnel, normalizedTrafficID, upstreamToTunnel),
		}
	}()

	go func() {
		workerWaitGroup.Wait()
		close(resultChannel)
	}()

	var relayError error
	readTunnelEOF := false
	readUpstreamEOF := false
	writeUpstreamDone := false
	writeTunnelDone := false
	stopByEOF := false
	stopRelay := sync.OnceFunc(func() {
		cancelRelay()
		_ = upstream.Close()
	})
	for result := range resultChannel {
		switch result.worker {
		case relayWorkerWriteUpstream:
			writeUpstreamDone = true
		case relayWorkerWriteTunnel:
			writeTunnelDone = true
		}
		if result.err == nil {
			if readTunnelEOF && writeUpstreamDone {
				stopByEOF = true
				stopRelay()
			}
			if readUpstreamEOF && writeTunnelDone {
				stopByEOF = true
				stopRelay()
			}
			continue
		}
		err := result.err
		if relayError != nil {
			continue
		}
		if errors.Is(err, io.EOF) {
			switch result.worker {
			case relayWorkerReadTunnel:
				readTunnelEOF = true
				if writeUpstreamDone {
					stopByEOF = true
					stopRelay()
				}
			case relayWorkerReadUpstream:
				readUpstreamEOF = true
				if writeTunnelDone {
					stopByEOF = true
					stopRelay()
				}
			}
			continue
		}
		if stopByEOF && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
			continue
		}
		relayError = err
		stopRelay()
	}
	if (readTunnelEOF || readUpstreamEOF) && relayError == nil {
		return nil
	}
	return relayError
}

func (relay *StreamRelay) readTunnelToUpstream(
	ctx context.Context,
	tunnel TunnelIO,
	trafficID string,
	dataChannel chan<- []byte,
) error {
	for {
		payload, err := tunnel.ReadPayload(ctx)
		if err != nil {
			return err
		}
		if payload.Close != nil && strings.TrimSpace(payload.Close.TrafficID) == trafficID {
			if shouldWriteCloseAckOnce(tunnel, trafficID) {
				closeAckPayload := pb.StreamPayload{
					CloseAck: &pb.TrafficCloseAck{
						TrafficID: strings.TrimSpace(payload.Close.TrafficID),
						Accepted:  true,
					},
				}
				if writeAckErr := tunnel.WritePayload(ctx, closeAckPayload); writeAckErr != nil {
					return fmt.Errorf("read tunnel close: write close ack: %w", writeAckErr)
				}
			}
			// close_ack 可能在 relay 阶段被消费，后续 recycle 阶段通过该标记继续满足前置条件。
			if observer, ok := tunnel.(closeAckObserver); ok {
				observer.MarkCloseAckObserved(trafficID)
			}
			return io.EOF
		}
		if payload.Reset != nil && strings.TrimSpace(payload.Reset.TrafficID) == trafficID {
			return fmt.Errorf(
				"read tunnel reset: %w: traffic_id=%s code=%s message=%s",
				ErrRelayResetByPeer,
				trafficID,
				payload.Reset.ErrorCode,
				payload.Reset.ErrorMessage,
			)
		}
		if len(payload.Data) == 0 {
			// Open/OpenAck 或空 Data 在 relay 阶段不参与数据转发。
			continue
		}
		chunk := append([]byte(nil), payload.Data...)
		if err := relay.sendWithBackpressure(ctx, dataChannel, chunk); err != nil {
			return fmt.Errorf("read tunnel data: %w", err)
		}
	}
}

func shouldWriteCloseAckOnce(tunnel TunnelIO, trafficID string) bool {
	if tunnel == nil {
		return false
	}
	tracker, ok := tunnel.(closeAckSentTracker)
	if !ok {
		// 未实现状态跟踪接口时按兼容路径放行一次 ACK。
		return true
	}
	return tracker.TryMarkCloseAckSent(trafficID)
}

func (relay *StreamRelay) writeTunnelDataToUpstream(
	ctx context.Context,
	upstream io.Writer,
	dataChannel <-chan []byte,
) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case chunk, open := <-dataChannel:
			if !open {
				return nil
			}
			if len(chunk) == 0 {
				continue
			}
			if err := writeAll(ctx, upstream, chunk); err != nil {
				return fmt.Errorf("write upstream data: %w", err)
			}
			// tunnel->upstream 视为 Agent runtime 下行字节。
			relay.observeDownloadBytes(len(chunk))
		}
	}
}

func (relay *StreamRelay) readUpstreamToTunnel(
	ctx context.Context,
	upstream io.Reader,
	dataChannel chan<- []byte,
) error {
	buffer := make([]byte, relay.maxDataFrameBytes)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		readSize, err := upstream.Read(buffer)
		if readSize > 0 {
			chunk := append([]byte(nil), buffer[:readSize]...)
			if sendErr := relay.sendWithBackpressure(ctx, dataChannel, chunk); sendErr != nil {
				return fmt.Errorf("read upstream data: %w", sendErr)
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return io.EOF
			}
			return fmt.Errorf("read upstream data: %w", err)
		}
	}
}

func (relay *StreamRelay) writeUpstreamDataToTunnel(
	ctx context.Context,
	tunnel TunnelIO,
	trafficID string,
	dataChannel <-chan []byte,
) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case chunk, open := <-dataChannel:
			if !open {
				return nil
			}
			if len(chunk) == 0 {
				continue
			}
			payload := pb.StreamPayload{
				Data: chunk,
			}
			if err := tunnel.WritePayload(ctx, payload); err != nil {
				return fmt.Errorf("write tunnel data: traffic_id=%s: %w", trafficID, err)
			}
			// upstream->tunnel 视为 Agent runtime 上行字节。
			relay.observeUploadBytes(len(chunk))
		}
	}
}

func (relay *StreamRelay) sendWithBackpressure(ctx context.Context, channel chan<- []byte, payload []byte) error {
	if relay.backpressureTimeout <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case channel <- payload:
			return nil
		}
	}
	timer := time.NewTimer(relay.backpressureTimeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case channel <- payload:
		return nil
	case <-timer.C:
		return fmt.Errorf("%w: timeout=%s", ErrRelayBackpressureTimeout, relay.backpressureTimeout)
	}
}

func writeAll(ctx context.Context, writer io.Writer, payload []byte) error {
	writtenSize := 0
	for writtenSize < len(payload) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		currentWritten, err := writer.Write(payload[writtenSize:])
		if currentWritten > 0 {
			writtenSize += currentWritten
		}
		if err != nil {
			return err
		}
		if currentWritten == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

// observeUploadBytes 记录 runtime 上行累计字节。
func (relay *StreamRelay) observeUploadBytes(byteCount int) {
	if relay == nil || relay.metrics == nil {
		return
	}
	relay.metrics.AddAgentTrafficUploadBytes(byteCount)
}

// observeDownloadBytes 记录 runtime 下行累计字节。
func (relay *StreamRelay) observeDownloadBytes(byteCount int) {
	if relay == nil || relay.metrics == nil {
		return
	}
	relay.metrics.AddAgentTrafficDownloadBytes(byteCount)
}
