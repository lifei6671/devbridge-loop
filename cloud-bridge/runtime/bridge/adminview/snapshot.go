package adminview

import (
	"sort"
	"strings"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/obs"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// BridgeOverviewSnapshot 描述管理后台总览页需要的聚合快照。
type BridgeOverviewSnapshot struct {
	ConnectorTotal int    `json:"connector_total"`
	SessionTotal   int    `json:"session_total"`
	SessionActive  int    `json:"session_active"`
	SessionStale   int    `json:"session_stale"`
	ServiceTotal   int    `json:"service_total"`
	RouteTotal     int    `json:"route_total"`
	TunnelIdle     int    `json:"tunnel_idle"`
	TunnelReserved int    `json:"tunnel_reserved"`
	TunnelActive   int    `json:"tunnel_active"`
	TunnelBroken   int    `json:"tunnel_broken"`
	UpdatedAtMS    uint64 `json:"updated_at_ms"`
}

// RouteItem 定义管理后台 routes 列表项。
type RouteItem struct {
	RouteID         string `json:"route_id"`
	Namespace       string `json:"namespace"`
	Environment     string `json:"environment"`
	TargetType      string `json:"target_type"`
	Protocol        string `json:"protocol"`
	Host            string `json:"host"`
	PathPrefix      string `json:"path_prefix"`
	Priority        uint32 `json:"priority"`
	ResourceVersion uint64 `json:"resource_version"`
}

// ConnectorItem 定义管理后台 connectors 列表项。
type ConnectorItem struct {
	ConnectorID    string  `json:"connector_id"`
	SessionID      string  `json:"session_id"`
	SessionEpoch   uint64  `json:"session_epoch"`
	SessionState   string  `json:"session_state"`
	ServiceCount   int     `json:"service_count"`
	ActiveServices int     `json:"active_service_count"`
	HealthRate     float64 `json:"health_rate"`
	UpdatedAtMS    uint64  `json:"updated_at_ms"`
}

// SessionItem 定义管理后台 sessions 列表项。
type SessionItem struct {
	SessionID       string `json:"session_id"`
	ConnectorID     string `json:"connector_id"`
	Epoch           uint64 `json:"epoch"`
	State           string `json:"state"`
	LastHeartbeatMS uint64 `json:"last_heartbeat_ms"`
	UpdatedAtMS     uint64 `json:"updated_at_ms"`
}

// TunnelItem 定义管理后台 tunnels 列表项。
type TunnelItem struct {
	TunnelID    string `json:"tunnel_id"`
	ConnectorID string `json:"connector_id"`
	SessionID   string `json:"session_id"`
	TrafficID   string `json:"traffic_id"`
	State       string `json:"state"`
	LastError   string `json:"last_error"`
	CreatedAtMS uint64 `json:"created_at_ms"`
	UpdatedAtMS uint64 `json:"updated_at_ms"`
}

// TunnelSummarySnapshot 定义管理后台 tunnel 汇总数据。
type TunnelSummarySnapshot struct {
	Idle        int    `json:"idle"`
	Reserved    int    `json:"reserved"`
	Active      int    `json:"active"`
	Closed      int    `json:"closed"`
	Broken      int    `json:"broken"`
	Total       int    `json:"total"`
	UpdatedAtMS uint64 `json:"updated_at_ms"`
}

// TrafficSummarySnapshot 定义管理后台 traffic 汇总数据。
type TrafficSummarySnapshot struct {
	AcquireWaitCount      uint64 `json:"acquire_wait_count"`
	AcquireWaitTotalMS    int64  `json:"acquire_wait_total_ms"`
	OpenTimeoutTotal      uint64 `json:"open_timeout_total"`
	OpenRejectTotal       uint64 `json:"open_reject_total"`
	OpenAckLateTotal      uint64 `json:"open_ack_late_total"`
	HybridFallbackTotal   uint64 `json:"hybrid_fallback_total"`
	EndpointOverrideTotal uint64 `json:"endpoint_override_total"`
	UpdatedAtMS           uint64 `json:"updated_at_ms"`
}

// DiagnoseSummarySnapshot 定义管理后台诊断聚合输出。
type DiagnoseSummarySnapshot struct {
	Health      string   `json:"health"`
	Issues      []string `json:"issues"`
	UpdatedAtMS uint64   `json:"updated_at_ms"`
}

// BuildBridgeOverview 构建 Bridge 总览快照。
func BuildBridgeOverview(
	now time.Time,
	sessions []registry.SessionRuntime,
	services []pb.Service,
	routes []pb.Route,
	tunnelSnapshot registry.TunnelSnapshot,
) BridgeOverviewSnapshot {
	normalizedNow := now
	if normalizedNow.IsZero() {
		normalizedNow = time.Now().UTC()
	}
	uniqueConnectors := make(map[string]struct{})
	activeSessions := 0
	staleSessions := 0
	for _, session := range sessions {
		connectorID := strings.TrimSpace(session.ConnectorID)
		if connectorID != "" {
			uniqueConnectors[connectorID] = struct{}{}
		}
		switch session.State {
		case registry.SessionActive:
			activeSessions++
		case registry.SessionStale:
			staleSessions++
		}
	}
	return BridgeOverviewSnapshot{
		ConnectorTotal: len(uniqueConnectors),
		SessionTotal:   len(sessions),
		SessionActive:  activeSessions,
		SessionStale:   staleSessions,
		ServiceTotal:   len(services),
		RouteTotal:     len(routes),
		TunnelIdle:     tunnelSnapshot.IdleCount,
		TunnelReserved: tunnelSnapshot.ReservedCount,
		TunnelActive:   tunnelSnapshot.ActiveCount,
		TunnelBroken:   tunnelSnapshot.BrokenCount,
		UpdatedAtMS:    uint64(normalizedNow.UnixMilli()),
	}
}

// BuildRouteItems 构建 routes 只读列表项（按 route_id 排序保证稳定分页）。
func BuildRouteItems(routes []pb.Route) []RouteItem {
	items := make([]RouteItem, 0, len(routes))
	for _, route := range routes {
		items = append(items, RouteItem{
			RouteID:         strings.TrimSpace(route.RouteID),
			Namespace:       strings.TrimSpace(route.Namespace),
			Environment:     strings.TrimSpace(route.Environment),
			TargetType:      strings.TrimSpace(string(route.Target.Type)),
			Protocol:        strings.TrimSpace(route.Match.Protocol),
			Host:            strings.TrimSpace(route.Match.Host),
			PathPrefix:      strings.TrimSpace(route.Match.PathPrefix),
			Priority:        route.Priority,
			ResourceVersion: route.ResourceVersion,
		})
	}
	sort.Slice(items, func(left, right int) bool {
		return items[left].RouteID < items[right].RouteID
	})
	return items
}

// BuildConnectorItems 构建 connectors 只读列表项。
func BuildConnectorItems(sessions []registry.SessionRuntime, services []pb.Service) []ConnectorItem {
	serviceCountByConnector := make(map[string]int)
	activeServiceCountByConnector := make(map[string]int)
	for _, service := range services {
		connectorID := strings.TrimSpace(service.ConnectorID)
		if connectorID == "" {
			continue
		}
		serviceCountByConnector[connectorID]++
		if service.Status == pb.ServiceStatusActive {
			activeServiceCountByConnector[connectorID]++
		}
	}

	itemsByConnector := make(map[string]ConnectorItem)
	for _, session := range sessions {
		connectorID := strings.TrimSpace(session.ConnectorID)
		if connectorID == "" {
			continue
		}
		existingItem, exists := itemsByConnector[connectorID]
		updatedAtMS := uint64(0)
		if !session.UpdatedAt.IsZero() {
			updatedAtMS = uint64(session.UpdatedAt.UTC().UnixMilli())
		}
		lastHeartbeatMS := uint64(0)
		if !session.LastHeartbeat.IsZero() {
			lastHeartbeatMS = uint64(session.LastHeartbeat.UTC().UnixMilli())
		}
		nextItem := ConnectorItem{
			ConnectorID:    connectorID,
			SessionID:      strings.TrimSpace(session.SessionID),
			SessionEpoch:   session.Epoch,
			SessionState:   strings.TrimSpace(string(session.State)),
			ServiceCount:   serviceCountByConnector[connectorID],
			ActiveServices: activeServiceCountByConnector[connectorID],
			HealthRate:     0,
			UpdatedAtMS:    maxUint64(updatedAtMS, lastHeartbeatMS),
		}
		if nextItem.ServiceCount > 0 {
			nextItem.HealthRate = float64(nextItem.ActiveServices) / float64(nextItem.ServiceCount)
		}
		if !exists || nextItem.SessionEpoch >= existingItem.SessionEpoch {
			itemsByConnector[connectorID] = nextItem
		}
	}
	for connectorID, serviceCount := range serviceCountByConnector {
		if _, exists := itemsByConnector[connectorID]; exists {
			continue
		}
		nextItem := ConnectorItem{
			ConnectorID:    connectorID,
			ServiceCount:   serviceCount,
			ActiveServices: activeServiceCountByConnector[connectorID],
			SessionState:   "UNAVAILABLE",
		}
		if nextItem.ServiceCount > 0 {
			nextItem.HealthRate = float64(nextItem.ActiveServices) / float64(nextItem.ServiceCount)
		}
		itemsByConnector[connectorID] = nextItem
	}

	items := make([]ConnectorItem, 0, len(itemsByConnector))
	for _, item := range itemsByConnector {
		items = append(items, item)
	}
	sort.Slice(items, func(left, right int) bool {
		if items[left].UpdatedAtMS == items[right].UpdatedAtMS {
			return items[left].ConnectorID < items[right].ConnectorID
		}
		return items[left].UpdatedAtMS > items[right].UpdatedAtMS
	})
	return items
}

// BuildSessionItems 构建 sessions 只读列表项。
func BuildSessionItems(sessions []registry.SessionRuntime) []SessionItem {
	items := make([]SessionItem, 0, len(sessions))
	for _, session := range sessions {
		lastHeartbeatMS := uint64(0)
		if !session.LastHeartbeat.IsZero() {
			lastHeartbeatMS = uint64(session.LastHeartbeat.UTC().UnixMilli())
		}
		updatedAtMS := uint64(0)
		if !session.UpdatedAt.IsZero() {
			updatedAtMS = uint64(session.UpdatedAt.UTC().UnixMilli())
		}
		items = append(items, SessionItem{
			SessionID:       strings.TrimSpace(session.SessionID),
			ConnectorID:     strings.TrimSpace(session.ConnectorID),
			Epoch:           session.Epoch,
			State:           strings.TrimSpace(string(session.State)),
			LastHeartbeatMS: lastHeartbeatMS,
			UpdatedAtMS:     updatedAtMS,
		})
	}
	sort.Slice(items, func(left, right int) bool {
		if items[left].UpdatedAtMS == items[right].UpdatedAtMS {
			return items[left].SessionID < items[right].SessionID
		}
		return items[left].UpdatedAtMS > items[right].UpdatedAtMS
	})
	return items
}

// BuildTunnelItems 构建 tunnels 只读列表项。
func BuildTunnelItems(tunnels []registry.TunnelRuntime) []TunnelItem {
	items := make([]TunnelItem, 0, len(tunnels))
	for _, tunnelRuntime := range tunnels {
		createdAtMS := uint64(0)
		if !tunnelRuntime.CreatedAt.IsZero() {
			createdAtMS = uint64(tunnelRuntime.CreatedAt.UTC().UnixMilli())
		}
		updatedAtMS := uint64(0)
		if !tunnelRuntime.UpdatedAt.IsZero() {
			updatedAtMS = uint64(tunnelRuntime.UpdatedAt.UTC().UnixMilli())
		}
		items = append(items, TunnelItem{
			TunnelID:    strings.TrimSpace(tunnelRuntime.TunnelID),
			ConnectorID: strings.TrimSpace(tunnelRuntime.ConnectorID),
			SessionID:   strings.TrimSpace(tunnelRuntime.SessionID),
			TrafficID:   strings.TrimSpace(tunnelRuntime.TrafficID),
			State:       strings.TrimSpace(string(tunnelRuntime.State)),
			LastError:   strings.TrimSpace(tunnelRuntime.LastError),
			CreatedAtMS: createdAtMS,
			UpdatedAtMS: updatedAtMS,
		})
	}
	sort.Slice(items, func(left, right int) bool {
		if items[left].UpdatedAtMS == items[right].UpdatedAtMS {
			return items[left].TunnelID < items[right].TunnelID
		}
		return items[left].UpdatedAtMS > items[right].UpdatedAtMS
	})
	return items
}

// BuildTunnelSummary 构建 tunnel 汇总快照。
func BuildTunnelSummary(now time.Time, snapshot registry.TunnelSnapshot) TunnelSummarySnapshot {
	normalizedNow := now
	if normalizedNow.IsZero() {
		normalizedNow = time.Now().UTC()
	}
	updatedAtMS := uint64(normalizedNow.UnixMilli())
	if !snapshot.UpdatedAt.IsZero() {
		updatedAtMS = uint64(snapshot.UpdatedAt.UTC().UnixMilli())
	}
	return TunnelSummarySnapshot{
		Idle:        snapshot.IdleCount,
		Reserved:    snapshot.ReservedCount,
		Active:      snapshot.ActiveCount,
		Closed:      snapshot.ClosedCount,
		Broken:      snapshot.BrokenCount,
		Total:       snapshot.TotalCount,
		UpdatedAtMS: updatedAtMS,
	}
}

// BuildTrafficSummary 构建 traffic 指标汇总。
func BuildTrafficSummary(now time.Time, metrics *obs.Metrics) TrafficSummarySnapshot {
	normalizedNow := now
	if normalizedNow.IsZero() {
		normalizedNow = time.Now().UTC()
	}
	if metrics == nil {
		metrics = obs.DefaultMetrics
	}
	return TrafficSummarySnapshot{
		AcquireWaitCount:      metrics.BridgeTunnelAcquireWaitCount(),
		AcquireWaitTotalMS:    metrics.BridgeTunnelAcquireWaitTotalMs(),
		OpenTimeoutTotal:      metrics.BridgeTrafficOpenTimeoutTotal(),
		OpenRejectTotal:       metrics.BridgeTrafficOpenRejectTotal(),
		OpenAckLateTotal:      metrics.BridgeTrafficOpenAckLateTotal(),
		HybridFallbackTotal:   metrics.BridgeHybridFallbackTotal(),
		EndpointOverrideTotal: metrics.BridgeActualEndpointOverrideTotal(),
		UpdatedAtMS:           uint64(normalizedNow.UnixMilli()),
	}
}

// BuildDiagnoseSummary 构建诊断聚合结果。
func BuildDiagnoseSummary(
	now time.Time,
	sessions []registry.SessionRuntime,
	tunnelSnapshot registry.TunnelSnapshot,
	metrics *obs.Metrics,
) DiagnoseSummarySnapshot {
	normalizedNow := now
	if normalizedNow.IsZero() {
		normalizedNow = time.Now().UTC()
	}
	issues := make([]string, 0, 4)
	staleSessionCount := 0
	for _, session := range sessions {
		if session.State == registry.SessionStale {
			staleSessionCount++
		}
	}
	if staleSessionCount > 0 {
		issues = append(issues, "存在 STALE 会话，请检查心跳与连接稳定性")
	}
	if tunnelSnapshot.BrokenCount > 0 {
		issues = append(issues, "存在 BROKEN tunnel，请检查 connector 路径与上游可达性")
	}
	trafficSummary := BuildTrafficSummary(normalizedNow, metrics)
	if trafficSummary.OpenTimeoutTotal > 0 {
		issues = append(issues, "检测到 traffic open timeout，请关注预开池水位与补池节奏")
	}
	health := "healthy"
	if len(issues) > 0 {
		health = "degraded"
	}
	return DiagnoseSummarySnapshot{
		Health:      health,
		Issues:      issues,
		UpdatedAtMS: uint64(normalizedNow.UnixMilli()),
	}
}

func maxUint64(left uint64, right uint64) uint64 {
	if left > right {
		return left
	}
	return right
}
