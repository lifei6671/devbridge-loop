package connectorproxy

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

var (
	// ErrCancelHandlerDependencyMissing 表示取消处理器关键依赖缺失。
	ErrCancelHandlerDependencyMissing = errors.New("cancel handler dependency missing")
)

const (
	// OpenTimeoutResetCode 表示 open_ack 超时触发的 reset 错误码。
	OpenTimeoutResetCode = "TRAFFIC_OPEN_ACK_TIMEOUT"
	// OpenTimeoutResetMessage 表示 open_ack 超时触发的 reset 错误信息。
	OpenTimeoutResetMessage = "bridge open ack timeout"
	// defaultCancelWriteTimeout 表示取消 reset 写入默认超时。
	defaultCancelWriteTimeout = 200 * time.Millisecond
)

// CancelHandlerOptions 描述取消处理器参数。
type CancelHandlerOptions struct {
	ResetCode    string
	ResetMessage string
	WriteTimeout time.Duration
}

// CancelHandler handles open timeout and cancellation.
type CancelHandler struct {
	resetCode    string
	resetMessage string
	writeTimeout time.Duration
}

// NewCancelHandler 创建 open 超时取消处理器。
func NewCancelHandler(options CancelHandlerOptions) *CancelHandler {
	resetCode := strings.TrimSpace(options.ResetCode)
	if resetCode == "" {
		resetCode = OpenTimeoutResetCode
	}
	resetMessage := strings.TrimSpace(options.ResetMessage)
	if resetMessage == "" {
		resetMessage = OpenTimeoutResetMessage
	}
	writeTimeout := options.WriteTimeout
	if writeTimeout <= 0 {
		writeTimeout = defaultCancelWriteTimeout
	}
	return &CancelHandler{
		resetCode:    resetCode,
		resetMessage: resetMessage,
		writeTimeout: writeTimeout,
	}
}

// HandleOpenAckTimeout 在 open_ack 超时后发 reset；写入失败时直接关闭 tunnel。
func (handler *CancelHandler) HandleOpenAckTimeout(ctx context.Context, tunnel registry.RuntimeTunnel, open pb.TrafficOpen) error {
	if handler == nil || tunnel == nil {
		return ErrCancelHandlerDependencyMissing
	}
	trafficID := strings.TrimSpace(open.TrafficID)
	if trafficID == "" {
		return ErrCancelHandlerDependencyMissing
	}
	normalizedContext := ctx
	if normalizedContext == nil {
		normalizedContext = context.Background()
	}
	writeContext, cancel := context.WithTimeout(normalizedContext, handler.writeTimeout)
	defer cancel()
	resetPayload := pb.StreamPayload{
		Reset: &pb.TrafficReset{
			TrafficID:    trafficID,
			ErrorCode:    handler.resetCode,
			ErrorMessage: handler.resetMessage,
		},
	}
	if writeErr := tunnel.WritePayload(writeContext, resetPayload); writeErr != nil {
		closeErr := tunnel.Close()
		if closeErr != nil {
			return fmt.Errorf("handle open_ack timeout: write reset and close tunnel: %w", errors.Join(writeErr, closeErr))
		}
		return fmt.Errorf("handle open_ack timeout: write reset failed, close tunnel fallback: %w", writeErr)
	}
	return nil
}
