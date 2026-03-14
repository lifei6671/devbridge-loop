package control

import (
	"strings"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// TunnelReportHandlerOptions 定义 tunnel 池上报处理器依赖。
type TunnelReportHandlerOptions struct {
	SessionRegistry  *registry.SessionRegistry
	RefillController *RefillController
}

// TunnelReportHandler 负责消费 Agent TunnelPoolReport 并决定是否触发补池请求。
type TunnelReportHandler struct {
	sessionRegistry  *registry.SessionRegistry
	refillController *RefillController
}

// NewTunnelReportHandler 创建 tunnel 池上报处理器。
func NewTunnelReportHandler(options TunnelReportHandlerOptions) *TunnelReportHandler {
	refillController := options.RefillController
	if refillController == nil {
		refillController = NewRefillController(RefillControllerOptions{})
	}
	return &TunnelReportHandler{
		sessionRegistry:  options.SessionRegistry,
		refillController: refillController,
	}
}

// HandleReport 处理 TunnelPoolReport 并在需要时生成 TunnelRefillRequest。
func (handler *TunnelReportHandler) HandleReport(
	envelope pb.ControlEnvelope,
	report pb.TunnelPoolReport,
) (pb.TunnelRefillRequest, bool) {
	if handler == nil || handler.refillController == nil {
		return pb.TunnelRefillRequest{}, false
	}
	sessionID := strings.TrimSpace(report.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(envelope.SessionID)
	}
	sessionEpoch := report.SessionEpoch
	if sessionEpoch == 0 {
		sessionEpoch = envelope.SessionEpoch
	}
	if !handler.validateSessionEpoch(sessionID, sessionEpoch) {
		return pb.TunnelRefillRequest{}, false
	}
	return handler.refillController.BuildRefillRequest(sessionID, sessionEpoch, report)
}

// validateSessionEpoch 校验报告会话是否与 Bridge 当前会话视图一致。
func (handler *TunnelReportHandler) validateSessionEpoch(sessionID string, sessionEpoch uint64) bool {
	if strings.TrimSpace(sessionID) == "" || sessionEpoch == 0 {
		return false
	}
	if handler.sessionRegistry == nil {
		// 未注入会话视图时保持兼容，允许继续处理。
		return true
	}
	sessionRuntime, exists := handler.sessionRegistry.GetBySession(sessionID)
	if !exists {
		return false
	}
	return sessionRuntime.Epoch == sessionEpoch
}
