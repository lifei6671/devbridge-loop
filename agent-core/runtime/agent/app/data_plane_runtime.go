package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/lifei6671/devbridge-loop/agent-core/pkg/events"
	"github.com/lifei6671/devbridge-loop/agent-core/runtime/agent/service"
	"github.com/lifei6671/devbridge-loop/agent-core/runtime/agent/traffic"
	"github.com/lifei6671/devbridge-loop/agent-core/runtime/agent/tunnel"
	"github.com/lifei6671/devbridge-loop/ltfp/codec"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
	"github.com/lifei6671/devbridge-loop/ltfp/transport"
)

var (
	// ErrRuntimeTrafficDependencyMissing 表示 Agent data-plane 关键依赖未初始化。
	ErrRuntimeTrafficDependencyMissing = errors.New("runtime traffic dependency missing")
)

const (
	// defaultTrafficAcceptorReconcileInterval 定义 acceptor worker 扫描周期。
	defaultTrafficAcceptorReconcileInterval = 300 * time.Millisecond
	// defaultTrafficTunnelIOPollInterval 定义 tunnel I/O 轮询窗口，用于响应 context 取消。
	defaultTrafficTunnelIOPollInterval = 100 * time.Millisecond
	// defaultTrafficTunnelMaxPayloadBytes 定义单条 StreamPayload 的上限。
	defaultTrafficTunnelMaxPayloadBytes = 8 * 1024 * 1024
	// defaultTrafficUpstreamDialTimeout 定义 Agent 拨本地 upstream 的默认超时。
	defaultTrafficUpstreamDialTimeout = 3 * time.Second

	// trafficOpenHintEndpointIDKey 定义 endpoint id hint 键名。
	trafficOpenHintEndpointIDKey = "endpoint_id"
	// trafficOpenHintEndpointAddrKey 定义 endpoint addr hint 键名。
	trafficOpenHintEndpointAddrKey = "endpoint_addr"
	// trafficOpenHintEndpointKey 定义 endpoint 地址别名键名。
	trafficOpenHintEndpointKey = "endpoint"
	// trafficOpenHintAddressKey 定义 address 地址别名键名。
	trafficOpenHintAddressKey = "address"
)

// tunnelAssociation 描述 tunnel 与 traffic/service 的关联运行态信息。
type tunnelAssociation struct {
	TunnelID              string
	TrafficID             string
	ServiceID             string
	LocalAddr             string
	OpenAckLatencyMS      uint64
	UpstreamDialLatencyMS uint64
	UpdatedAt             time.Time
}

// initTrafficRuntime 初始化 Agent 侧 traffic acceptor + opener 运行时依赖。
func (r *Runtime) initTrafficRuntime() error {
	if r == nil || r.tunnelRegistry == nil || r.tunnelManager == nil {
		return ErrRuntimeTrafficDependencyMissing
	}
	opener, err := traffic.NewOpener(traffic.OpenerOptions{
		Selector: &runtimeHintEndpointSelector{
			serviceCatalog: r.serviceCatalog,
		},
		Dialer: &runtimeNetUpstreamDialer{
			dialTimeout: defaultTrafficUpstreamDialTimeout,
		},
		Relay: traffic.NewStreamRelay(traffic.StreamRelayOptions{
			// 将 relay 字节计数写入 runtime 指标容器，供 traffic.stats.snapshot 使用。
			Metrics: r.metrics,
		}),
		Metrics: r.metrics,
	})
	if err != nil {
		return fmt.Errorf("init traffic runtime: new opener: %w", err)
	}
	r.trafficAcceptor = traffic.NewAcceptor()
	r.trafficOpener = opener
	r.trafficWorkersMutex.Lock()
	if r.trafficWorkers == nil {
		r.trafficWorkers = make(map[string]struct{})
	}
	r.trafficWorkersMutex.Unlock()
	if r.trafficWakeupChannel == nil {
		r.trafficWakeupChannel = make(chan struct{}, 1)
	}
	// 绑定 manager 事件通知，pool 状态变化时可即时唤醒 acceptor reconcile。
	r.tunnelManager.BindEventNotifier(r)
	return nil
}

// NotifyEvent 实现 tunnel.PoolEventNotifier，统一分发池事件到 data-plane 与上报器。
func (r *Runtime) NotifyEvent(trigger string, reason string) {
	if r == nil {
		return
	}
	r.recordTunnelPoolDiagnoseEvent(trigger, reason)
	normalizedTrigger := strings.TrimSpace(trigger)
	if r.trafficWakeupChannel != nil {
		select {
		case r.trafficWakeupChannel <- struct{}{}:
		default:
		}
	}
	if r.tunnelReporter != nil {
		// 事件驱动上报用于减少 Bridge 与 Agent 的池状态漂移。
		r.tunnelReporter.NotifyEvent(normalizedTrigger)
	}
}

// recordTunnelPoolDiagnoseEvent 将 tunnel pool 治理事件写入 runtime 诊断日志，供 UI 统一观测。
func (r *Runtime) recordTunnelPoolDiagnoseEvent(trigger string, reason string) {
	if r == nil {
		return
	}
	normalizedTrigger := strings.TrimSpace(trigger)
	normalizedReason := strings.TrimSpace(reason)
	if normalizedTrigger == "" {
		return
	}
	level := events.EventInfo
	code := events.CodeTunnelPoolEvent
	switch normalizedTrigger {
	case "refill_requested":
		code = events.CodeTunnelRefillRequested
	case "ttl_reaped":
		code = events.CodeTunnelIdleTTLReaped
	case "tunnel_closed":
		code = events.CodeTunnelCleanupClosed
	case "tunnel_broken":
		code = events.CodeTunnelCleanupBroken
		level = events.EventWarn
	case "idle_acquired":
		code = events.CodeTunnelIdleAcquired
	case "tunnel_active":
		code = events.CodeTunnelActive
	case "pool_changed":
		code = events.CodeTunnelPoolChanged
	case "pool_rebuilt":
		code = events.CodeTunnelPoolRebuilt
	case "session_draining":
		code = events.CodeTunnelPoolSessionDraining
	case "session_stale":
		code = events.CodeTunnelPoolSessionStale
		level = events.EventWarn
	case "session_active":
		code = events.CodeTunnelPoolSessionActive
	case "startup_reconcile_failed":
		code = events.CodeTunnelPoolStartupReconcileFailed
		level = events.EventWarn
	default:
		// 保留透传 trigger，便于后续扩展治理事件时无损观测。
	}

	poolSnapshot := tunnel.Snapshot{}
	if r.tunnelManager != nil {
		poolSnapshot = r.tunnelManager.Snapshot()
	}
	sessionID, sessionEpoch, bridgeState := r.bridgeRuntimeContext()
	message := fmt.Sprintf(
		"tunnel pool event trigger=%s idle=%d active=%d total=%d",
		normalizedTrigger,
		poolSnapshot.IdleCount,
		poolSnapshot.ActiveCount,
		poolSnapshot.TotalCount,
	)
	if normalizedReason != "" {
		message = fmt.Sprintf("%s reason=%s", message, normalizedReason)
	}
	r.appendDiagnoseEvent(runtimeDiagnoseEvent{
		Level:        level,
		Module:       events.ModuleAgentRuntimeTunnel,
		Code:         code,
		Message:      message,
		SessionID:    sessionID,
		SessionEpoch: sessionEpoch,
		BridgeState:  bridgeState,
		Trigger:      normalizedTrigger,
		Reason:       normalizedReason,
	})
}

// runTrafficAcceptorLoop 维护 idle tunnel -> trafficAcceptor worker 的生命周期。
func (r *Runtime) runTrafficAcceptorLoop(ctx context.Context) error {
	if r == nil || r.tunnelRegistry == nil || r.tunnelManager == nil || r.trafficAcceptor == nil || r.trafficOpener == nil {
		return ErrRuntimeTrafficDependencyMissing
	}
	normalizedContext := ctx
	if normalizedContext == nil {
		normalizedContext = context.Background()
	}
	r.reconcileTrafficAcceptorWorkers(normalizedContext)

	reconcileTicker := time.NewTicker(defaultTrafficAcceptorReconcileInterval)
	defer reconcileTicker.Stop()
	for {
		select {
		case <-normalizedContext.Done():
			return normalizedContext.Err()
		case <-reconcileTicker.C:
			// 周期对账用于兜底事件丢失或并发竞态场景。
			r.reconcileTrafficAcceptorWorkers(normalizedContext)
		case <-r.trafficWakeupChannel:
			// 事件驱动优先触发，降低新 idle tunnel 的接流量延迟。
			r.reconcileTrafficAcceptorWorkers(normalizedContext)
		}
	}
}

// reconcileTrafficAcceptorWorkers 为每条 idle tunnel 启动唯一的 acceptor worker。
func (r *Runtime) reconcileTrafficAcceptorWorkers(ctx context.Context) {
	if r == nil || r.tunnelRegistry == nil {
		return
	}
	records := r.tunnelRegistry.List(0)
	for _, record := range records {
		if record.State != tunnel.StateIdle || record.Tunnel == nil {
			continue
		}
		tunnelIO, ok := record.Tunnel.(traffic.TunnelIO)
		if !ok {
			// 不支持 payload 读写的 tunnel 无法进入 traffic runtime，直接回收避免僵尸。
			r.cleanupTunnelAfterTraffic(record.TunnelID, "idle tunnel does not implement traffic tunnel io")
			continue
		}
		if !r.markTrafficWorkerRunning(record.TunnelID) {
			continue
		}
		go r.runTrafficAcceptorWorker(ctx, record.TunnelID, tunnelIO)
	}
}

// runTrafficAcceptorWorker 执行单 tunnel 的 open 接收、激活、处理与回收闭环。
func (r *Runtime) runTrafficAcceptorWorker(ctx context.Context, tunnelID string, tunnelIO traffic.TunnelIO) {
	defer r.clearTrafficWorkerRunning(tunnelID)
	if r == nil || r.trafficAcceptor == nil || r.trafficOpener == nil || r.tunnelManager == nil {
		return
	}
	for {
		handoff, err := r.trafficAcceptor.WaitTrafficOpen(ctx, tunnelID, tunnelIO)
		if err != nil {
			if isTrafficContextCanceled(ctx, err) {
				return
			}
			r.cleanupTunnelAfterTraffic(tunnelID, fmt.Sprintf("wait traffic open failed: %v", err))
			return
		}

		if err := r.tunnelManager.ActivateIdle(tunnelID); err != nil {
			// 未进入 opener 流程前需要主动释放 lease，避免 reader 所有权泄漏。
			if handoff.Lease != nil {
				handoff.Lease.Release()
			}
			if errors.Is(err, tunnel.ErrTunnelNotFound) {
				return
			}
			r.cleanupTunnelAfterTraffic(tunnelID, fmt.Sprintf("activate idle failed: %v", err))
			return
		}
		r.upsertTunnelAssociation(tunnelAssociation{
			TunnelID:  tunnelID,
			TrafficID: strings.TrimSpace(handoff.Open.TrafficID),
			ServiceID: strings.TrimSpace(handoff.Open.ServiceID),
			// open 阶段先写入 hint 地址，后续以实际选中的 endpoint 地址覆盖。
			LocalAddr: resolveTrafficHintEndpointAddr(handoff.Open.EndpointSelectionHint),
			UpdatedAt: time.Now().UTC(),
		})
		handleResult, err := r.trafficOpener.Handle(ctx, handoff, tunnelIO)
		if err != nil {
			r.cleanupTunnelAfterTraffic(tunnelID, fmt.Sprintf("handle traffic open failed: %v", err))
			return
		}
		r.upsertTunnelAssociation(tunnelAssociation{
			TunnelID:              tunnelID,
			TrafficID:             strings.TrimSpace(handleResult.TrafficID),
			ServiceID:             strings.TrimSpace(handoff.Open.ServiceID),
			LocalAddr:             strings.TrimSpace(handleResult.Endpoint.Addr),
			OpenAckLatencyMS:      handleResult.OpenAckLatencyMS,
			UpstreamDialLatencyMS: handleResult.UpstreamDialLatencyMS,
			UpdatedAt:             time.Now().UTC(),
		})
		recycled, finalClose, recycleErr := r.handleTunnelRecyclePhase(ctx, tunnelID, handleResult.TrafficID, tunnelIO)
		if recycleErr != nil {
			r.cleanupTunnelAfterTraffic(tunnelID, fmt.Sprintf("wait recycle failed: %v", recycleErr))
			return
		}
		if finalClose {
			r.cleanupTunnelAfterTraffic(tunnelID, "tunnel recycle final close")
			return
		}
		if !recycled {
			r.cleanupTunnelAfterTraffic(tunnelID, "tunnel recycle rejected")
			return
		}
		// 成功回收入池后继续下一轮 traffic。
	}
}

// markTrafficWorkerRunning 标记 tunnel 的 acceptor worker 已启动，避免重复并发 reader。
func (r *Runtime) markTrafficWorkerRunning(tunnelID string) bool {
	if r == nil {
		return false
	}
	normalizedTunnelID := strings.TrimSpace(tunnelID)
	if normalizedTunnelID == "" {
		return false
	}
	r.trafficWorkersMutex.Lock()
	defer r.trafficWorkersMutex.Unlock()
	if _, exists := r.trafficWorkers[normalizedTunnelID]; exists {
		return false
	}
	r.trafficWorkers[normalizedTunnelID] = struct{}{}
	return true
}

// clearTrafficWorkerRunning 清理 tunnel 的 acceptor worker 运行标记。
func (r *Runtime) clearTrafficWorkerRunning(tunnelID string) {
	if r == nil {
		return
	}
	normalizedTunnelID := strings.TrimSpace(tunnelID)
	if normalizedTunnelID == "" {
		return
	}
	r.trafficWorkersMutex.Lock()
	defer r.trafficWorkersMutex.Unlock()
	delete(r.trafficWorkers, normalizedTunnelID)
}

// cleanupTunnelAfterTraffic 在 traffic 终态后回收 tunnel 记录。
func (r *Runtime) cleanupTunnelAfterTraffic(tunnelID string, reason string) {
	if r == nil || r.tunnelManager == nil {
		return
	}
	normalizedTunnelID := strings.TrimSpace(tunnelID)
	if normalizedTunnelID == "" {
		return
	}
	// tunnel 回收后同步清理关联信息，避免 UI 读取到陈旧关联。
	r.removeTunnelAssociation(normalizedTunnelID)
	if err := r.tunnelManager.CloseAndRemoveWithReason(normalizedTunnelID, strings.TrimSpace(reason)); err == nil || errors.Is(err, tunnel.ErrTunnelNotFound) {
		return
	}
	// 兜底走 broken 回收，避免状态机停留在中间态。
	_ = r.tunnelManager.MarkBrokenAndRemove(normalizedTunnelID, strings.TrimSpace(reason))
}

// handleTunnelRecyclePhase 等待 close_ack + recycle 并执行回收入池或终态关闭。
func (r *Runtime) handleTunnelRecyclePhase(
	ctx context.Context,
	tunnelID string,
	trafficID string,
	tunnelIO traffic.TunnelIO,
) (bool, bool, error) {
	if r == nil || r.tunnelManager == nil || tunnelIO == nil {
		return false, false, ErrRuntimeTrafficDependencyMissing
	}
	normalizedTunnelID := strings.TrimSpace(tunnelID)
	normalizedTrafficID := strings.TrimSpace(trafficID)
	waitContext := ctx
	if waitContext == nil {
		waitContext = context.Background()
	}
	recycleTimeout := r.cfg.TunnelPool.RecycleAckTO
	if recycleTimeout <= 0 {
		recycleTimeout = 3 * time.Second
	}
	usePhaseReadTimeout := shouldUseTrafficRecycleReadTimeout(tunnelIO)
	// 预等待阶段给更宽松的窗口，避免 Bridge 仍在回写客户端响应时提前超时。
	preRecycleTimeout := recycleTimeout * 5
	if preRecycleTimeout < 15*time.Second {
		preRecycleTimeout = 15 * time.Second
	}
	closeAckObserved := false
	for {
		readContext := waitContext
		cancelRead := func() {}
		if usePhaseReadTimeout {
			if _, hasDeadline := waitContext.Deadline(); !hasDeadline {
				phaseTimeout := recycleTimeout
				if !closeAckObserved {
					phaseTimeout = preRecycleTimeout
				}
				if phaseTimeout > 0 {
					readContext, cancelRead = context.WithTimeout(waitContext, phaseTimeout)
				}
			}
		}
		if !usePhaseReadTimeout {
			// grpc_h2 的 per-read timeout 会触发 stream 级 cancel，导致 tunnel 被误判为终态；
			// recycle 阶段在 grpc 下改为仅跟随上层 context（runtime 生命周期）等待。
			readContext = waitContext
		}
		payload, err := tunnelIO.ReadPayload(readContext)
		cancelRead()
		if err != nil {
			return false, false, err
		}
		if payload.CloseAck != nil && strings.TrimSpace(payload.CloseAck.TrafficID) == normalizedTrafficID {
			closeAckObserved = true
			continue
		}
		if payload.Close != nil && strings.TrimSpace(payload.Close.TrafficID) == normalizedTrafficID {
			// Bridge 主动发 close 的场景下，本地回 ack 后即可进入 recycle 等待。
			closeAckObserved = true
			// 对端若再次发送 close，按协议回 ACK，避免对端等待。
			_ = tunnelIO.WritePayload(waitContext, pb.StreamPayload{
				CloseAck: &pb.TrafficCloseAck{
					TrafficID: normalizedTrafficID,
					Accepted:  true,
				},
			})
			continue
		}
		if payload.Reset != nil && strings.TrimSpace(payload.Reset.TrafficID) == normalizedTrafficID {
			return false, false, fmt.Errorf(
				"traffic reset before recycle: code=%s message=%s",
				strings.TrimSpace(payload.Reset.ErrorCode),
				strings.TrimSpace(payload.Reset.ErrorMessage),
			)
		}
		if payload.Recycle == nil {
			// 忽略与当前回收阶段无关的帧。
			continue
		}
		recycle := payload.Recycle
		recycleTunnelID := strings.TrimSpace(recycle.TunnelID)
		recycleAckTunnelID := normalizedTunnelID
		if recycleTunnelID != "" && recycleTunnelID != normalizedTunnelID {
			if !isGRPCTrafficTunnelIO(tunnelIO) {
				_ = r.writeTunnelRecycleAck(waitContext, tunnelIO, normalizedTunnelID, recycle.RecycleSeq, false, "tunnel_mismatch", "recycle tunnel id mismatch")
				return false, false, fmt.Errorf("recycle tunnel id mismatch: got=%s want=%s", recycleTunnelID, normalizedTunnelID)
			}
			// grpc_h2 兼容路径：Bridge 侧可能使用 stream 本地 tunnel_id；
			// 允许沿用对端 recycle tunnel_id 回 ACK，并仍使用本地 tunnel_id 驱动 Agent 状态机。
			recycleAckTunnelID = recycleTunnelID
		}
		record, exists := r.tunnelManager.Get(normalizedTunnelID)
		lastRecycleSeq := uint64(0)
		if exists && record != nil {
			lastRecycleSeq = record.LastRecycleSeq
		}
		if recycle.RecycleSeq == 0 || recycle.RecycleSeq <= lastRecycleSeq {
			_ = r.writeTunnelRecycleAck(waitContext, tunnelIO, recycleAckTunnelID, recycle.RecycleSeq, false, "invalid_seq", "recycle seq not monotonic")
			return false, false, fmt.Errorf("invalid recycle seq: got=%d last=%d", recycle.RecycleSeq, lastRecycleSeq)
		}
		if !closeAckObserved {
			// 在部分时序下，close 可能已在 relay 阶段被消费，此处允许直接进入 recycle 以提升复用鲁棒性。
			closeAckObserved = true
		}
		if err := r.writeTunnelRecycleAck(waitContext, tunnelIO, recycleAckTunnelID, recycle.RecycleSeq, true, "", ""); err != nil {
			return false, false, err
		}
		if recycle.IsFinal {
			return false, true, nil
		}
		if err := r.tunnelManager.RecycleToIdle(normalizedTunnelID, recycle.RecycleSeq); err != nil {
			return false, false, err
		}
		// tunnel 回收入池后清理上轮 traffic 关联，等待下一次 open。
		r.removeTunnelAssociation(normalizedTunnelID)
		return true, false, nil
	}
}

func shouldUseTrafficRecycleReadTimeout(tunnelIO traffic.TunnelIO) bool {
	return !isGRPCTrafficTunnelIO(tunnelIO)
}

func isGRPCTrafficTunnelIO(tunnelIO traffic.TunnelIO) bool {
	if tunnelIO == nil {
		return false
	}
	type bindingTypeAware interface {
		BindingType() transport.BindingType
	}
	awareTunnelIO, ok := tunnelIO.(bindingTypeAware)
	if !ok {
		return false
	}
	return awareTunnelIO.BindingType() == transport.BindingTypeGRPCH2
}

func (r *Runtime) writeTunnelRecycleAck(
	ctx context.Context,
	tunnelIO traffic.TunnelIO,
	tunnelID string,
	recycleSeq uint64,
	accepted bool,
	errorCode string,
	errorMessage string,
) error {
	if tunnelIO == nil {
		return ErrRuntimeTrafficDependencyMissing
	}
	return tunnelIO.WritePayload(ctx, pb.StreamPayload{
		RecycleAck: &pb.TunnelRecycleAck{
			TunnelID:     strings.TrimSpace(tunnelID),
			RecycleSeq:   recycleSeq,
			Accepted:     accepted,
			ErrorCode:    strings.TrimSpace(errorCode),
			ErrorMessage: strings.TrimSpace(errorMessage),
		},
	})
}

// upsertTunnelAssociation 写入 tunnel 与 traffic/service 的关联运行态。
func (r *Runtime) upsertTunnelAssociation(association tunnelAssociation) {
	if r == nil {
		return
	}
	normalizedTunnelID := strings.TrimSpace(association.TunnelID)
	if normalizedTunnelID == "" {
		return
	}
	r.tunnelAssocMutex.Lock()
	defer r.tunnelAssocMutex.Unlock()
	if r.tunnelAssociations == nil {
		r.tunnelAssociations = make(map[string]tunnelAssociation)
	}
	existingAssociation := r.tunnelAssociations[normalizedTunnelID]
	if strings.TrimSpace(association.TrafficID) == "" {
		association.TrafficID = existingAssociation.TrafficID
	}
	if strings.TrimSpace(association.ServiceID) == "" {
		association.ServiceID = existingAssociation.ServiceID
	}
	if strings.TrimSpace(association.LocalAddr) == "" {
		association.LocalAddr = existingAssociation.LocalAddr
	}
	if association.OpenAckLatencyMS == 0 {
		association.OpenAckLatencyMS = existingAssociation.OpenAckLatencyMS
	}
	if association.UpstreamDialLatencyMS == 0 {
		association.UpstreamDialLatencyMS = existingAssociation.UpstreamDialLatencyMS
	}
	if association.UpdatedAt.IsZero() {
		association.UpdatedAt = time.Now().UTC()
	}
	association.TunnelID = normalizedTunnelID
	r.tunnelAssociations[normalizedTunnelID] = association
}

// tunnelAssociationByID 读取指定 tunnel 的关联运行态快照。
func (r *Runtime) tunnelAssociationByID(tunnelID string) (tunnelAssociation, bool) {
	if r == nil {
		return tunnelAssociation{}, false
	}
	normalizedTunnelID := strings.TrimSpace(tunnelID)
	if normalizedTunnelID == "" {
		return tunnelAssociation{}, false
	}
	r.tunnelAssocMutex.RLock()
	defer r.tunnelAssocMutex.RUnlock()
	association, exists := r.tunnelAssociations[normalizedTunnelID]
	return association, exists
}

// removeTunnelAssociation 删除 tunnel 的关联运行态快照。
func (r *Runtime) removeTunnelAssociation(tunnelID string) {
	if r == nil {
		return
	}
	normalizedTunnelID := strings.TrimSpace(tunnelID)
	if normalizedTunnelID == "" {
		return
	}
	r.tunnelAssocMutex.Lock()
	defer r.tunnelAssocMutex.Unlock()
	delete(r.tunnelAssociations, normalizedTunnelID)
}

// isTrafficContextCanceled 判断错误是否由 context 主动取消触发。
func isTrafficContextCanceled(ctx context.Context, err error) bool {
	if ctx != nil && ctx.Err() != nil {
		return true
	}
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// runtimeHintEndpointSelector 使用 endpoint hint 做本地 endpoint 选择。
type runtimeHintEndpointSelector struct {
	serviceCatalog *service.Catalog
}

// SelectEndpoint 基于 service_id 与 hint 选择最终 endpoint。
func (selector *runtimeHintEndpointSelector) SelectEndpoint(
	ctx context.Context,
	serviceID string,
	hint map[string]string,
) (traffic.Endpoint, error) {
	_ = selector
	_ = ctx
	normalizedServiceID := strings.TrimSpace(serviceID)
	if normalizedServiceID == "" {
		return traffic.Endpoint{}, fmt.Errorf("select endpoint: empty service id")
	}
	normalizedHint := copyTrafficHintMap(hint)
	endpointAddrCandidates := []string{
		normalizedHint[trafficOpenHintEndpointAddrKey],
		normalizedHint[trafficOpenHintEndpointKey],
		normalizedHint[trafficOpenHintAddressKey],
	}
	resolvedEndpointAddr := ""
	for _, endpointAddr := range endpointAddrCandidates {
		if strings.TrimSpace(endpointAddr) == "" {
			continue
		}
		resolvedEndpointAddr = strings.TrimSpace(endpointAddr)
		break
	}
	if resolvedEndpointAddr == "" {
		catalogEndpoint, hasCatalogEndpoint := selector.selectEndpointFromCatalog(normalizedServiceID, normalizedHint)
		if hasCatalogEndpoint {
			return catalogEndpoint, nil
		}
		return traffic.Endpoint{}, fmt.Errorf("select endpoint: service=%s missing endpoint hint", normalizedServiceID)
	}
	endpointID := strings.TrimSpace(normalizedHint[trafficOpenHintEndpointIDKey])
	if endpointID == "" {
		// 未显式提供 endpoint_id 时退化为地址字符串，保证日志可追踪。
		endpointID = resolvedEndpointAddr
	}
	return traffic.Endpoint{
		ID:   endpointID,
		Addr: resolvedEndpointAddr,
	}, nil
}

func (selector *runtimeHintEndpointSelector) selectEndpointFromCatalog(
	serviceID string,
	hint map[string]string,
) (traffic.Endpoint, bool) {
	if selector == nil || selector.serviceCatalog == nil {
		return traffic.Endpoint{}, false
	}
	normalizedServiceID := strings.TrimSpace(serviceID)
	if normalizedServiceID == "" {
		return traffic.Endpoint{}, false
	}
	endpointIDHint := strings.TrimSpace(hint[trafficOpenHintEndpointIDKey])
	endpointAddrHint := strings.TrimSpace(hint[trafficOpenHintEndpointAddrKey])
	if endpointAddrHint == "" {
		endpointAddrHint = strings.TrimSpace(hint[trafficOpenHintEndpointKey])
	}
	if endpointAddrHint == "" {
		endpointAddrHint = strings.TrimSpace(hint[trafficOpenHintAddressKey])
	}
	records := selector.serviceCatalog.List()
	for _, record := range records {
		candidateServiceID := strings.TrimSpace(record.Registration.ServiceID)
		candidateServiceKey := strings.TrimSpace(record.Registration.ServiceKey)
		if normalizedServiceID != candidateServiceID && normalizedServiceID != candidateServiceKey {
			continue
		}
		if endpointIDHint != "" {
			for _, endpoint := range record.Registration.Endpoints {
				if strings.TrimSpace(endpoint.EndpointID) != endpointIDHint {
					continue
				}
				if endpointAddr := resolveEndpointAddress(endpoint); endpointAddr != "" {
					return traffic.Endpoint{
						ID:   endpointIDHint,
						Addr: endpointAddr,
					}, true
				}
			}
		}
		if endpointAddrHint != "" {
			for _, endpoint := range record.Registration.Endpoints {
				endpointAddr := resolveEndpointAddress(endpoint)
				if endpointAddr == "" || endpointAddr != endpointAddrHint {
					continue
				}
				endpointID := strings.TrimSpace(endpoint.EndpointID)
				if endpointID == "" {
					endpointID = endpointAddr
				}
				return traffic.Endpoint{
					ID:   endpointID,
					Addr: endpointAddr,
				}, true
			}
		}
		for _, endpoint := range record.Registration.Endpoints {
			endpointAddr := resolveEndpointAddress(endpoint)
			if endpointAddr == "" {
				continue
			}
			endpointID := strings.TrimSpace(endpoint.EndpointID)
			if endpointID == "" {
				endpointID = endpointAddr
			}
			return traffic.Endpoint{
				ID:   endpointID,
				Addr: endpointAddr,
			}, true
		}
	}
	return traffic.Endpoint{}, false
}

func resolveEndpointAddress(endpoint pb.ServiceEndpoint) string {
	normalizedHost := strings.TrimSpace(endpoint.Host)
	if normalizedHost == "" {
		return ""
	}
	if endpoint.Port > 0 {
		return net.JoinHostPort(normalizedHost, strconv.Itoa(int(endpoint.Port)))
	}
	return normalizedHost
}

// runtimeNetUpstreamDialer 是 Agent 默认 upstream TCP 拨号器。
type runtimeNetUpstreamDialer struct {
	dialTimeout time.Duration
}

// Dial 建立到本地 upstream endpoint 的连接。
func (dialer *runtimeNetUpstreamDialer) Dial(
	ctx context.Context,
	endpoint traffic.Endpoint,
) (io.ReadWriteCloser, error) {
	if dialer == nil {
		return nil, ErrRuntimeTrafficDependencyMissing
	}
	normalizedContext := ctx
	if normalizedContext == nil {
		normalizedContext = context.Background()
	}
	if dialer.dialTimeout > 0 {
		if _, hasDeadline := normalizedContext.Deadline(); !hasDeadline {
			var cancel context.CancelFunc
			normalizedContext, cancel = context.WithTimeout(normalizedContext, dialer.dialTimeout)
			defer cancel()
		}
	}
	normalizedAddress := strings.TrimSpace(endpoint.Addr)
	if normalizedAddress == "" {
		return nil, fmt.Errorf("dial upstream: empty endpoint address")
	}
	connection, err := (&net.Dialer{}).DialContext(normalizedContext, "tcp", normalizedAddress)
	if err != nil {
		return nil, fmt.Errorf("dial upstream: endpoint=%s: %w", normalizedAddress, err)
	}
	return connection, nil
}

// runtimeTrafficTunnel 适配 transport.Tunnel 到 traffic.TunnelIO。
type runtimeTrafficTunnel struct {
	tunnel          transport.Tunnel
	jsonCodec       *codec.JSONCodec
	maxPayloadBytes int
	ioPollInterval  time.Duration
}

var _ tunnel.RuntimeTunnel = (*runtimeTrafficTunnel)(nil)
var _ traffic.TunnelIO = (*runtimeTrafficTunnel)(nil)

// newRuntimeTrafficTunnelAdapter 创建 Agent data-plane tunnel payload 适配器。
func newRuntimeTrafficTunnelAdapter(rawTunnel transport.Tunnel) *runtimeTrafficTunnel {
	return &runtimeTrafficTunnel{
		tunnel:          rawTunnel,
		jsonCodec:       codec.NewJSONCodec(),
		maxPayloadBytes: defaultTrafficTunnelMaxPayloadBytes,
		ioPollInterval:  defaultTrafficTunnelIOPollInterval,
	}
}

// ID 返回 tunnel 唯一标识。
func (adapter *runtimeTrafficTunnel) ID() string {
	if adapter == nil || adapter.tunnel == nil {
		return ""
	}
	return strings.TrimSpace(adapter.tunnel.ID())
}

// BindingType 返回底层 tunnel 的承载类型。
func (adapter *runtimeTrafficTunnel) BindingType() transport.BindingType {
	if adapter == nil || adapter.tunnel == nil {
		return ""
	}
	return adapter.tunnel.BindingInfo().Type
}

// Close 关闭底层 tunnel。
func (adapter *runtimeTrafficTunnel) Close() error {
	if adapter == nil || adapter.tunnel == nil {
		return nil
	}
	return adapter.tunnel.Close()
}

// Done 返回底层 tunnel 关闭信号，供 traffic opener 监听取消。
func (adapter *runtimeTrafficTunnel) Done() <-chan struct{} {
	if adapter == nil || adapter.tunnel == nil {
		doneChannel := make(chan struct{})
		close(doneChannel)
		return doneChannel
	}
	return adapter.tunnel.Done()
}

// Err 返回底层 tunnel 最近错误。
func (adapter *runtimeTrafficTunnel) Err() error {
	if adapter == nil || adapter.tunnel == nil {
		return ErrRuntimeTrafficDependencyMissing
	}
	return adapter.tunnel.Err()
}

// ReadPayload 从底层 tunnel 读取并解码一条 StreamPayload。
func (adapter *runtimeTrafficTunnel) ReadPayload(ctx context.Context) (pb.StreamPayload, error) {
	if adapter == nil || adapter.tunnel == nil || adapter.jsonCodec == nil {
		return pb.StreamPayload{}, ErrRuntimeTrafficDependencyMissing
	}
	normalizedContext := ctx
	if normalizedContext == nil {
		normalizedContext = context.Background()
	}
	usePollingDeadline := shouldUseTrafficTunnelPollingDeadline(adapter.tunnel)
	readBuffer := make([]byte, adapter.maxPayloadBytes)
	for {
		if err := normalizedContext.Err(); err != nil {
			return pb.StreamPayload{}, err
		}
		if err := adapter.tunnel.SetReadDeadline(
			nextTrafficTunnelReadDeadline(normalizedContext, adapter.ioPollInterval, usePollingDeadline),
		); err != nil {
			return pb.StreamPayload{}, fmt.Errorf("read payload: set read deadline: %w", err)
		}
		readSize, readErr := adapter.tunnel.Read(readBuffer)
		if readSize > 0 {
			decodedPayload, decodeErr := adapter.jsonCodec.DecodeStreamPayload(readBuffer[:readSize])
			if decodeErr != nil {
				return pb.StreamPayload{}, fmt.Errorf("read payload: decode stream payload: %w", decodeErr)
			}
			return decodedPayload, nil
		}
		if readErr == nil {
			continue
		}
		if errors.Is(readErr, transport.ErrTimeout) {
			// grpc_h2 不使用短轮询超时；命中 timeout 仅代表上层 deadline 到达。
			if !usePollingDeadline {
				if err := normalizedContext.Err(); err != nil {
					return pb.StreamPayload{}, err
				}
			}
			continue
		}
		return pb.StreamPayload{}, fmt.Errorf("read payload: read tunnel: %w", readErr)
	}
}

// WritePayload 将 StreamPayload 编码后写入底层 tunnel。
func (adapter *runtimeTrafficTunnel) WritePayload(ctx context.Context, payload pb.StreamPayload) error {
	if adapter == nil || adapter.tunnel == nil || adapter.jsonCodec == nil {
		return ErrRuntimeTrafficDependencyMissing
	}
	encodedPayload, err := adapter.jsonCodec.EncodeStreamPayload(payload)
	if err != nil {
		return fmt.Errorf("write payload: encode stream payload: %w", err)
	}
	if len(encodedPayload) > adapter.maxPayloadBytes {
		return fmt.Errorf("write payload: payload exceeds limit size=%d max=%d", len(encodedPayload), adapter.maxPayloadBytes)
	}
	normalizedContext := ctx
	if normalizedContext == nil {
		normalizedContext = context.Background()
	}
	usePollingDeadline := shouldUseTrafficTunnelPollingDeadline(adapter.tunnel)
	for {
		if err := normalizedContext.Err(); err != nil {
			return err
		}
		if err := adapter.tunnel.SetWriteDeadline(
			nextTrafficTunnelWriteDeadline(normalizedContext, adapter.ioPollInterval, usePollingDeadline),
		); err != nil {
			return fmt.Errorf("write payload: set write deadline: %w", err)
		}
		writtenSize, writeErr := adapter.tunnel.Write(encodedPayload)
		if writeErr == nil {
			if writtenSize != len(encodedPayload) {
				return io.ErrShortWrite
			}
			return nil
		}
		if errors.Is(writeErr, transport.ErrTimeout) {
			// grpc_h2 不使用短轮询超时；命中 timeout 仅代表上层 deadline 到达。
			if !usePollingDeadline {
				if err := normalizedContext.Err(); err != nil {
					return err
				}
			}
			continue
		}
		return fmt.Errorf("write payload: write tunnel: %w", writeErr)
	}
}

func shouldUseTrafficTunnelPollingDeadline(rawTunnel transport.Tunnel) bool {
	if rawTunnel == nil {
		return true
	}
	return rawTunnel.BindingInfo().Type != transport.BindingTypeGRPCH2
}

func nextTrafficTunnelReadDeadline(ctx context.Context, pollInterval time.Duration, usePolling bool) time.Time {
	if !usePolling {
		if ctx == nil {
			return time.Time{}
		}
		if contextDeadline, hasDeadline := ctx.Deadline(); hasDeadline {
			return contextDeadline
		}
		return time.Time{}
	}
	return nextTrafficTunnelIODeadline(ctx, pollInterval)
}

func nextTrafficTunnelWriteDeadline(ctx context.Context, pollInterval time.Duration, usePolling bool) time.Time {
	if !usePolling {
		if ctx == nil {
			return time.Time{}
		}
		if contextDeadline, hasDeadline := ctx.Deadline(); hasDeadline {
			return contextDeadline
		}
		return time.Time{}
	}
	return nextTrafficTunnelIODeadline(ctx, pollInterval)
}

// nextTrafficTunnelIODeadline 计算下一次 tunnel I/O 的短轮询 deadline。
func nextTrafficTunnelIODeadline(ctx context.Context, pollInterval time.Duration) time.Time {
	effectivePollInterval := pollInterval
	if effectivePollInterval <= 0 {
		effectivePollInterval = defaultTrafficTunnelIOPollInterval
	}
	nextDeadline := time.Now().UTC().Add(effectivePollInterval)
	if ctx == nil {
		return nextDeadline
	}
	if contextDeadline, hasDeadline := ctx.Deadline(); hasDeadline && contextDeadline.Before(nextDeadline) {
		return contextDeadline
	}
	return nextDeadline
}

// copyTrafficHintMap 复制 endpoint hint，避免调用方后续修改影响 selector 读值。
func copyTrafficHintMap(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	copied := make(map[string]string, len(source))
	for key, value := range source {
		copied[key] = value
	}
	return copied
}

// resolveTrafficHintEndpointAddr 从 endpoint_selection_hint 中提取地址类 hint。
func resolveTrafficHintEndpointAddr(hint map[string]string) string {
	normalizedHint := copyTrafficHintMap(hint)
	if len(normalizedHint) == 0 {
		return ""
	}
	addressCandidates := []string{
		normalizedHint[trafficOpenHintEndpointAddrKey],
		normalizedHint[trafficOpenHintEndpointKey],
		normalizedHint[trafficOpenHintAddressKey],
	}
	for _, address := range addressCandidates {
		if strings.TrimSpace(address) == "" {
			continue
		}
		return strings.TrimSpace(address)
	}
	return ""
}
