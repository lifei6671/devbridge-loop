package connectorproxy

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/obs"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
	"github.com/lifei6671/devbridge-loop/ltfp/validate"
)

var (
	// ErrOpenHandshakeDependencyMissing 表示 open handshake 依赖缺失。
	ErrOpenHandshakeDependencyMissing = errors.New("open handshake dependency missing")
	// ErrOpenAckTimeout 表示等待 open_ack 超时。
	ErrOpenAckTimeout = errors.New("traffic open ack timeout")
	// ErrTrafficOpenRejected 表示收到 open_ack reject。
	ErrTrafficOpenRejected = errors.New("traffic open rejected")
	// ErrInvalidOpenAck 表示 open_ack 不符合当前 traffic 语义。
	ErrInvalidOpenAck = errors.New("invalid traffic open ack")
)

// OpenHandshakeOptions 定义 open handshake 参数。
type OpenHandshakeOptions struct {
	OpenTimeout         time.Duration
	LateAckDrainTimeout time.Duration
	CancelHandler       *CancelHandler
	Metrics             *obs.Metrics
}

// OpenRejectedError 描述 open_ack reject 详情。
type OpenRejectedError struct {
	Ack pb.TrafficOpenAck
}

// Error 返回 open reject 可读文本。
func (openRejectedError *OpenRejectedError) Error() string {
	if openRejectedError == nil {
		return ErrTrafficOpenRejected.Error()
	}
	return fmt.Sprintf(
		"%s: code=%s message=%s",
		ErrTrafficOpenRejected,
		openRejectedError.Ack.ErrorCode,
		openRejectedError.Ack.ErrorMessage,
	)
}

// Unwrap 返回基础错误类型，便于 errors.Is 判断。
func (openRejectedError *OpenRejectedError) Unwrap() error {
	return ErrTrafficOpenRejected
}

// OpenHandshake manages TrafficOpen / TrafficOpenAck.
type OpenHandshake struct {
	openTimeout         time.Duration
	lateAckDrainTimeout time.Duration
	cancelHandler       *CancelHandler
	metrics             *obs.Metrics
}

// NewOpenHandshake 创建 open handshake 执行器。
func NewOpenHandshake(options OpenHandshakeOptions) *OpenHandshake {
	lateAckDrainTimeout := options.LateAckDrainTimeout
	if lateAckDrainTimeout < 0 {
		lateAckDrainTimeout = 0
	}
	cancelHandler := options.CancelHandler
	if cancelHandler == nil {
		cancelHandler = NewCancelHandler(CancelHandlerOptions{})
	}
	metrics := options.Metrics
	if metrics == nil {
		metrics = obs.DefaultMetrics
	}
	return &OpenHandshake{
		openTimeout:         options.OpenTimeout,
		lateAckDrainTimeout: lateAckDrainTimeout,
		cancelHandler:       cancelHandler,
		metrics:             metrics,
	}
}

// Execute 执行 open -> open_ack 握手。
func (handshake *OpenHandshake) Execute(ctx context.Context, tunnel registry.RuntimeTunnel, open pb.TrafficOpen) (pb.TrafficOpenAck, error) {
	if handshake == nil || tunnel == nil {
		return pb.TrafficOpenAck{}, ErrOpenHandshakeDependencyMissing
	}
	if err := validate.ValidateTrafficOpen(open); err != nil {
		errorCode := ltfperrors.ExtractCode(err)
		if strings.TrimSpace(errorCode) == "" {
			errorCode = ltfperrors.CodeInvalidPayload
		}
		return pb.TrafficOpenAck{}, fmt.Errorf("execute open handshake: validate traffic open: %s: %w", errorCode, err)
	}
	normalizedContext := ctx
	if normalizedContext == nil {
		normalizedContext = context.Background()
	}

	openPayload := pb.StreamPayload{
		OpenReq: &open,
	}
	if err := tunnel.WritePayload(normalizedContext, openPayload); err != nil {
		return pb.TrafficOpenAck{}, fmt.Errorf("execute open handshake: write open req: %w", err)
	}

	readContext := normalizedContext
	cancel := func() {}
	if handshake.openTimeout > 0 {
		readContext, cancel = context.WithTimeout(normalizedContext, handshake.openTimeout)
	}
	defer cancel()

	for {
		payload, err := tunnel.ReadPayload(readContext)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(readContext.Err(), context.DeadlineExceeded) {
				cancelErr := handshake.cancelTimeoutTraffic(normalizedContext, tunnel, open)
				// 迟到 ack 丢弃在后台执行，避免阻塞 timeout 返回路径。
				go handshake.discardLateOpenAck(tunnel, open)
				return pb.TrafficOpenAck{}, fmt.Errorf("execute open handshake: %w", errors.Join(ErrOpenAckTimeout, cancelErr))
			}
			return pb.TrafficOpenAck{}, fmt.Errorf("execute open handshake: read open ack: %w", err)
		}
		if payload.OpenAck == nil {
			return pb.TrafficOpenAck{}, fmt.Errorf("execute open handshake: %w: open_ack is required", ErrInvalidOpenAck)
		}
		ack := *payload.OpenAck
		if strings.TrimSpace(ack.TrafficID) != strings.TrimSpace(open.TrafficID) {
			return pb.TrafficOpenAck{}, fmt.Errorf(
				"execute open handshake: %w: expected_traffic_id=%s got=%s",
				ErrInvalidOpenAck,
				strings.TrimSpace(open.TrafficID),
				strings.TrimSpace(ack.TrafficID),
			)
		}
		if !ack.Success {
			return ack, &OpenRejectedError{Ack: ack}
		}
		return ack, nil
	}
}

func (handshake *OpenHandshake) cancelTimeoutTraffic(ctx context.Context, tunnel registry.RuntimeTunnel, open pb.TrafficOpen) error {
	if handshake == nil || handshake.cancelHandler == nil {
		return ErrOpenHandshakeDependencyMissing
	}
	if err := handshake.cancelHandler.HandleOpenAckTimeout(ctx, tunnel, open); err != nil {
		return fmt.Errorf("cancel timeout traffic: %w", err)
	}
	return nil
}

func (handshake *OpenHandshake) discardLateOpenAck(tunnel registry.RuntimeTunnel, open pb.TrafficOpen) {
	if handshake == nil || tunnel == nil || handshake.lateAckDrainTimeout <= 0 {
		return
	}
	drainContext, cancel := context.WithTimeout(context.Background(), handshake.lateAckDrainTimeout)
	defer cancel()
	trafficID := strings.TrimSpace(open.TrafficID)
	if trafficID == "" {
		return
	}
	for {
		payload, err := tunnel.ReadPayload(drainContext)
		if err != nil {
			return
		}
		if payload.OpenAck == nil {
			continue
		}
		ack := *payload.OpenAck
		if strings.TrimSpace(ack.TrafficID) != trafficID {
			continue
		}
		if handshake.metrics != nil {
			handshake.metrics.IncBridgeTrafficOpenAckLateTotal()
		}
		log.Printf(
			"drop late traffic open ack metric=%s traffic_id=%s tunnel_id=%s success=%t error_code=%s",
			obs.MetricBridgeTrafficOpenAckLateTotal,
			trafficID,
			strings.TrimSpace(tunnel.ID()),
			ack.Success,
			strings.TrimSpace(ack.ErrorCode),
		)
		return
	}
}
