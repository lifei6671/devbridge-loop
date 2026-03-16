package control

import (
	"strconv"
	"strings"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// TunnelReportHandlerOptions 定义 tunnel 池上报处理器依赖。
type TunnelReportHandlerOptions struct {
	SessionRegistry  *registry.SessionRegistry
	TunnelRegistry   *registry.TunnelRegistry
	RefillController *RefillController
	ReportStore      *TunnelPoolReportStore
}

// TunnelReportHandler 负责消费 Agent TunnelPoolReport 并决定是否触发补池请求。
type TunnelReportHandler struct {
	sessionRegistry  *registry.SessionRegistry
	tunnelRegistry   *registry.TunnelRegistry
	refillController *RefillController
	reportStore      *TunnelPoolReportStore
}

// NewTunnelReportHandler 创建 tunnel 池上报处理器。
func NewTunnelReportHandler(options TunnelReportHandlerOptions) *TunnelReportHandler {
	refillController := options.RefillController
	if refillController == nil {
		refillController = NewRefillController(RefillControllerOptions{})
	}
	return &TunnelReportHandler{
		sessionRegistry:  options.SessionRegistry,
		tunnelRegistry:   options.TunnelRegistry,
		refillController: refillController,
		reportStore:      options.ReportStore,
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
	now := time.Now().UTC()
	if handler.reportStore != nil {
		connectorID := strings.TrimSpace(envelope.ConnectorID)
		if connectorID == "" && handler.sessionRegistry != nil {
			if sessionRuntime, exists := handler.sessionRegistry.GetBySession(sessionID); exists {
				connectorID = strings.TrimSpace(sessionRuntime.ConnectorID)
			}
		}
		handler.reportStore.Upsert(now, connectorID, sessionID, sessionEpoch, report)
	}
	recycledIdleCount := 0
	if shouldReconcileBridgeIdleWithAgentReport(report.Trigger) {
		recycledIdleCount = handler.reconcileBridgeIdleWithAgentReport(now, sessionID, report)
	}
	refillRequest, shouldSend := handler.refillController.BuildRefillRequest(sessionID, sessionEpoch, report)
	if !shouldSend {
		return pb.TunnelRefillRequest{}, false
	}
	bridgeIdleCount, bridgeInUseCount := handler.snapshotBridgePoolBySession(sessionID)
	if refillRequest.Metadata == nil {
		refillRequest.Metadata = make(map[string]string, 8)
	}
	refillRequest.Metadata["bridge_idle_count"] = strconv.Itoa(bridgeIdleCount)
	refillRequest.Metadata["bridge_in_use_count"] = strconv.Itoa(bridgeInUseCount)
	refillRequest.Metadata["bridge_idle_recycled_count"] = strconv.Itoa(recycledIdleCount)
	return refillRequest, true
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

func (handler *TunnelReportHandler) snapshotBridgePoolBySession(sessionID string) (int, int) {
	if handler == nil || handler.tunnelRegistry == nil {
		return 0, 0
	}
	normalizedSessionID := strings.TrimSpace(sessionID)
	if normalizedSessionID == "" {
		return 0, 0
	}
	idleCount := 0
	inUseCount := 0
	for _, runtime := range handler.tunnelRegistry.List() {
		if strings.TrimSpace(runtime.SessionID) != normalizedSessionID {
			continue
		}
		switch runtime.State {
		case registry.TunnelStateIdle:
			idleCount++
		case registry.TunnelStateReserved, registry.TunnelStateActive:
			inUseCount++
		}
	}
	return idleCount, inUseCount
}

func (handler *TunnelReportHandler) reconcileBridgeIdleWithAgentReport(
	now time.Time,
	sessionID string,
	report pb.TunnelPoolReport,
) int {
	if handler == nil || handler.tunnelRegistry == nil {
		return 0
	}
	normalizedSessionID := strings.TrimSpace(sessionID)
	if normalizedSessionID == "" {
		return 0
	}
	agentIdleCount := report.IdleCount
	if agentIdleCount < 0 {
		agentIdleCount = 0
	}
	bridgeIdleCount, _ := handler.snapshotBridgePoolBySession(normalizedSessionID)
	excessIdleCount := bridgeIdleCount - agentIdleCount
	if excessIdleCount <= 0 {
		return 0
	}
	recycled := handler.tunnelRegistry.RecycleIdleBySession(
		now,
		normalizedSessionID,
		excessIdleCount,
		"agent_pool_reconcile",
	)
	return len(recycled)
}

func shouldReconcileBridgeIdleWithAgentReport(trigger string) bool {
	// event:* 报告在隧道状态切换阶段会抖动，强行对齐容易误回收新建 tunnel。
	return strings.EqualFold(strings.TrimSpace(trigger), "periodic")
}
