package adminview

import (
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/obs"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// BridgeOverviewSnapshot 描述管理后台总览页需要的聚合快照。
type BridgeOverviewSnapshot struct {
	ConnectorTotal int                  `json:"connector_total"`
	SessionTotal   int                  `json:"session_total"`
	SessionActive  int                  `json:"session_active"`
	SessionStale   int                  `json:"session_stale"`
	ServiceTotal   int                  `json:"service_total"`
	RouteTotal     int                  `json:"route_total"`
	TunnelIdle     int                  `json:"tunnel_idle"`
	TunnelReserved int                  `json:"tunnel_reserved"`
	TunnelActive   int                  `json:"tunnel_active"`
	TunnelBroken   int                  `json:"tunnel_broken"`
	Listeners      []BridgeListenerItem `json:"listeners"`
	UpdatedAtMS    uint64               `json:"updated_at_ms"`
}

// BridgeListenerItem 描述 Bridge 当前启用的监听器及用途。
type BridgeListenerItem struct {
	ListenerID string `json:"listener_id"`
	Label      string `json:"label"`
	ListenAddr string `json:"listen_addr"`
	Port       string `json:"port"`
	Purpose    string `json:"purpose"`
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
	Binding        string  `json:"binding"`
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
	Binding         string `json:"binding"`
	State           string `json:"state"`
	LastHeartbeatMS uint64 `json:"last_heartbeat_ms"`
	UpdatedAtMS     uint64 `json:"updated_at_ms"`
}

// ServiceItem 定义管理后台 services 列表项。
type ServiceItem struct {
	LogicalServiceID string   `json:"logical_service_id"`
	InstanceID       string   `json:"instance_id"`
	Scope            pb.Scope `json:"scope"`
	ConnectorID      string   `json:"connector_id"`
	SessionID        string   `json:"session_id"`
	SessionState     string   `json:"session_state"`
	ServiceName      string   `json:"service_name"`
	ServiceType      string   `json:"service_type"`
	EndpointCount    int      `json:"endpoint_count"`
	EndpointProto    string   `json:"endpoint_protocol"`
	EndpointHost     string   `json:"endpoint_host"`
	EndpointPort     uint32   `json:"endpoint_port"`
	EndpointAddress  string   `json:"endpoint_address"`
	IngressMode      string   `json:"ingress_mode"`
	SNIName          string   `json:"sni_name"`
	RouteTarget      string   `json:"route_target"`
	AccessHint       string   `json:"access_hint"`
	Status           string   `json:"status"`
	InstanceStatus   string   `json:"instance_status"`
	HealthStatus     string   `json:"health_status"`
	ActiveInstances  int32    `json:"active_instance_count"`
	HealthyInstances int32    `json:"healthy_instance_count"`
	UpdatedAtMS      uint64   `json:"updated_at_ms"`
}

// TunnelItem 定义管理后台 tunnels 列表项。
type TunnelItem struct {
	TunnelID    string `json:"tunnel_id"`
	ConnectorID string `json:"connector_id"`
	SessionID   string `json:"session_id"`
	Binding     string `json:"binding"`
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
	AcquireWaitCount                  uint64            `json:"acquire_wait_count"`
	AcquireWaitTotalMS                int64             `json:"acquire_wait_total_ms"`
	OpenTimeoutTotal                  uint64            `json:"open_timeout_total"`
	OpenRejectTotal                   uint64            `json:"open_reject_total"`
	OpenAckLateTotal                  uint64            `json:"open_ack_late_total"`
	ScopeFallbackTotal                uint64            `json:"scope_fallback_total"`
	RouteConflictRejectionTotal       uint64            `json:"route_conflict_rejection_total"`
	HostDeriveSuccessTotal            uint64            `json:"host_derive_success_total"`
	HostDeriveFailureTotal            uint64            `json:"host_derive_failure_total"`
	EndpointOverrideTotal             uint64            `json:"endpoint_override_total"`
	AuthSuccessTotal                  uint64            `json:"auth_success_total"`
	AuthFailureTotal                  uint64            `json:"auth_failure_total"`
	AuthRateLimitTotal                uint64            `json:"auth_rate_limit_total"`
	AuthSupersedeTotal                uint64            `json:"auth_supersede_total"`
	TLSRejectPlaintextOnRequiredTotal uint64            `json:"tls_reject_plaintext_on_required_total"`
	TLSRejectTLSOnPlaintextTotal      uint64            `json:"tls_reject_tls_on_plaintext_total"`
	TunnelRecycleFailureTotal         uint64            `json:"tunnel_recycle_failure_total"`
	QUICConnectionAcceptTotal         uint64            `json:"quic_connection_accept_total"`
	QUICConnectionActive              int64             `json:"quic_connection_active"`
	QUICConnectionAuthenticatedTotal  uint64            `json:"quic_connection_authenticated_total"`
	QUICTunnelRegisteredTotal         uint64            `json:"quic_tunnel_registered_total"`
	AuthErrorCodeTotals               map[string]uint64 `json:"auth_error_code_totals"`
	TunnelRecycleErrorCodeTotals      map[string]uint64 `json:"tunnel_recycle_error_code_totals"`
	UpdatedAtMS                       uint64            `json:"updated_at_ms"`
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
	logicalServices []pb.LogicalService,
	routes []pb.Route,
	tunnelSnapshot registry.TunnelSnapshot,
	configSnapshot map[string]any,
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
		case registry.SessionStale, registry.SessionFailed:
			staleSessions++
		}
	}
	return BridgeOverviewSnapshot{
		ConnectorTotal: len(uniqueConnectors),
		SessionTotal:   len(sessions),
		SessionActive:  activeSessions,
		SessionStale:   staleSessions,
		ServiceTotal:   len(logicalServices),
		RouteTotal:     len(routes),
		TunnelIdle:     tunnelSnapshot.IdleCount,
		TunnelReserved: tunnelSnapshot.ReservedCount,
		TunnelActive:   tunnelSnapshot.ActiveCount,
		TunnelBroken:   tunnelSnapshot.BrokenCount,
		Listeners:      BuildBridgeListeners(configSnapshot),
		UpdatedAtMS:    uint64(normalizedNow.UnixMilli()),
	}
}

// BuildBridgeListeners 基于当前配置快照提取 Bridge 已启用的监听器清单。
func BuildBridgeListeners(configSnapshot map[string]any) []BridgeListenerItem {
	if len(configSnapshot) == 0 {
		return []BridgeListenerItem{}
	}

	ingressConfig := nestedConfigMap(configSnapshot, "ingress")
	adminConfig := nestedConfigMap(configSnapshot, "admin")
	controlPlaneConfig := nestedConfigMap(configSnapshot, "control_plane")

	listeners := make([]BridgeListenerItem, 0, 5)
	appendListener := func(listenerID string, label string, listenAddr string, purpose string) {
		normalizedListenAddr := strings.TrimSpace(listenAddr)
		if normalizedListenAddr == "" {
			return
		}
		listeners = append(listeners, BridgeListenerItem{
			ListenerID: listenerID,
			Label:      label,
			ListenAddr: normalizedListenAddr,
			Port:       extractListenPort(normalizedListenAddr),
			Purpose:    purpose,
		})
	}

	appendListener(
		"ingress_http",
		"Ingress HTTP",
		readConfigString(ingressConfig, "http_addr"),
		"HTTP L7 入口，承接共享的浏览器与 API 流量。",
	)
	appendListener(
		"ingress_grpc",
		"Ingress gRPC",
		readConfigString(ingressConfig, "grpc_addr"),
		"gRPC 入口，承接共享的 gRPC 协议流量。",
	)
	appendListener(
		"control_plane_tcp",
		"Control Plane TCP",
		readConfigString(controlPlaneConfig, "listen_addr"),
		"Agent 控制面 TCP framed 通道，同时接收 TCP tunnel。",
	)
	appendListener(
		"control_plane_grpc",
		"Control Plane gRPC",
		readConfigString(controlPlaneConfig, "grpc_h2_listen_addr"),
		"Agent 控制面 gRPC H2 通道，同时接收 gRPC tunnel。",
	)
	if strings.ToLower(readConfigString(controlPlaneConfig, "tls_mode")) != "plaintext" {
		appendListener(
			"control_plane_quic",
			"Control Plane QUIC",
			readConfigString(controlPlaneConfig, "quic_listen_addr"),
			"Agent 控制面 QUIC 通道，同时接收 QUIC tunnel。",
		)
	}
	if readConfigBool(adminConfig, "enabled") {
		appendListener(
			"admin_ui_api",
			"Admin UI / API",
			readConfigString(adminConfig, "listen_addr"),
			"管理后台 UI 与 Admin API 入口。",
		)
	}
	return listeners
}

func nestedConfigMap(snapshot map[string]any, key string) map[string]any {
	if snapshot == nil {
		return map[string]any{}
	}
	rawSection, exists := snapshot[key]
	if !exists {
		return map[string]any{}
	}
	section, ok := rawSection.(map[string]any)
	if !ok || section == nil {
		return map[string]any{}
	}
	return section
}

func readConfigString(section map[string]any, key string) string {
	if section == nil {
		return ""
	}
	rawValue, exists := section[key]
	if !exists {
		return ""
	}
	if textValue, ok := rawValue.(string); ok {
		return strings.TrimSpace(textValue)
	}
	return ""
}

func readConfigBool(section map[string]any, key string) bool {
	if section == nil {
		return false
	}
	rawValue, exists := section[key]
	if !exists {
		return false
	}
	if boolValue, ok := rawValue.(bool); ok {
		return boolValue
	}
	return false
}

func extractListenPort(listenAddr string) string {
	normalizedListenAddr := strings.TrimSpace(listenAddr)
	if normalizedListenAddr == "" {
		return "--"
	}
	_, port, err := net.SplitHostPort(normalizedListenAddr)
	if err == nil && strings.TrimSpace(port) != "" {
		return strings.TrimSpace(port)
	}
	return "--"
}

// BuildRouteItems 构建 routes 只读列表项（按 route_id 排序保证稳定分页）。
func BuildRouteItems(routes []pb.Route) []RouteItem {
	items := make([]RouteItem, 0, len(routes))
	for _, route := range routes {
		items = append(items, RouteItem{
			RouteID:         strings.TrimSpace(route.RouteID),
			Namespace:       strings.TrimSpace(route.Scope.Namespace),
			Environment:     strings.TrimSpace(route.Scope.Environment),
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
func BuildConnectorItems(sessions []registry.SessionRuntime, serviceInstances []pb.ServiceInstance) []ConnectorItem {
	serviceCountByConnector := make(map[string]int)
	activeServiceCountByConnector := make(map[string]int)
	for _, serviceInstance := range serviceInstances {
		connectorID := strings.TrimSpace(serviceInstance.ConnectorID)
		if connectorID == "" {
			continue
		}
		serviceCountByConnector[connectorID]++
		if serviceInstance.InstanceStatus == pb.ServiceStatusActive {
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
			Binding:        strings.TrimSpace(session.Binding),
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
			Binding:         strings.TrimSpace(session.Binding),
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

// BuildServiceItems 构建 services 只读列表项，包含与 session 的关联信息和访问提示。
func BuildServiceItems(
	now time.Time,
	logicalServices []pb.LogicalService,
	serviceInstances []pb.ServiceInstance,
	sessions []registry.SessionRuntime,
) []ServiceItem {
	normalizedNow := now
	if normalizedNow.IsZero() {
		normalizedNow = time.Now().UTC()
	}
	nowMS := uint64(normalizedNow.UnixMilli())
	logicalServicesByID := make(map[string]pb.LogicalService, len(logicalServices))
	for _, logicalService := range logicalServices {
		logicalServicesByID[strings.TrimSpace(logicalService.LogicalServiceID)] = logicalService
	}

	latestSessionByConnector := make(map[string]registry.SessionRuntime)
	for _, session := range sessions {
		connectorID := strings.TrimSpace(session.ConnectorID)
		if connectorID == "" {
			continue
		}
		existingSession, exists := latestSessionByConnector[connectorID]
		if !exists {
			latestSessionByConnector[connectorID] = session
			continue
		}
		shouldReplace := session.Epoch > existingSession.Epoch
		if !shouldReplace {
			shouldReplace = session.UpdatedAt.After(existingSession.UpdatedAt)
		}
		if shouldReplace {
			latestSessionByConnector[connectorID] = session
		}
	}

	items := make([]ServiceItem, 0, len(serviceInstances))
	for _, serviceInstance := range serviceInstances {
		connectorID := strings.TrimSpace(serviceInstance.ConnectorID)
		logicalService := logicalServicesByID[strings.TrimSpace(serviceInstance.LogicalServiceID)]
		sessionItem, hasSession := latestSessionByConnector[connectorID]
		sessionID := ""
		sessionState := "UNAVAILABLE"
		updatedAtMS := nowMS
		if hasSession {
			sessionID = strings.TrimSpace(sessionItem.SessionID)
			sessionState = strings.TrimSpace(string(sessionItem.State))
			if !sessionItem.UpdatedAt.IsZero() {
				updatedAtMS = uint64(sessionItem.UpdatedAt.UTC().UnixMilli())
			}
			if !sessionItem.LastHeartbeat.IsZero() {
				lastHeartbeatMS := uint64(sessionItem.LastHeartbeat.UTC().UnixMilli())
				if lastHeartbeatMS > updatedAtMS {
					updatedAtMS = lastHeartbeatMS
				}
			}
		}

		endpointProto := ""
		endpointHost := ""
		var endpointPort uint32
		endpointAddress := "--"
		sniName := strings.TrimSpace(serviceInstance.Exposure.SNIName)
		if len(serviceInstance.Endpoints) > 0 {
			firstEndpoint := serviceInstance.Endpoints[0]
			endpointProto = strings.TrimSpace(firstEndpoint.Protocol)
			endpointHost = strings.TrimSpace(firstEndpoint.Host)
			endpointPort = firstEndpoint.Port
			if endpointHost != "" && endpointPort > 0 {
				endpointAddress = fmt.Sprintf("%s:%d", endpointHost, endpointPort)
			} else if endpointHost != "" {
				endpointAddress = endpointHost
			} else if endpointPort > 0 {
				endpointAddress = fmt.Sprintf(":%d", endpointPort)
			}
			if sniName == "" {
				sniName = strings.TrimSpace(firstEndpoint.ServerName)
			}
		}
		serviceType := endpointProto
		ingressMode := strings.TrimSpace(string(serviceInstance.Exposure.IngressMode))
		if ingressMode == "" {
			ingressMode = "direct"
		}
		routeTarget := fmt.Sprintf(
			"connector_service.selector={serviceName:%s,scope:%s/%s}",
			strings.TrimSpace(logicalService.ServiceName),
			strings.TrimSpace(logicalService.Scope.Namespace),
			strings.TrimSpace(logicalService.Scope.Environment),
		)
		accessHint := routeTarget
		if sniName != "" {
			accessHint = fmt.Sprintf("%s; route.match.sni=%s", accessHint, sniName)
		}

		items = append(items, ServiceItem{
			LogicalServiceID: strings.TrimSpace(logicalService.LogicalServiceID),
			InstanceID:       strings.TrimSpace(serviceInstance.InstanceID),
			Scope: pb.Scope{
				Namespace:   strings.TrimSpace(logicalService.Scope.Namespace),
				Environment: strings.TrimSpace(logicalService.Scope.Environment),
			},
			ConnectorID:      connectorID,
			SessionID:        sessionID,
			SessionState:     sessionState,
			ServiceName:      strings.TrimSpace(logicalService.ServiceName),
			ServiceType:      serviceType,
			EndpointCount:    len(serviceInstance.Endpoints),
			EndpointProto:    endpointProto,
			EndpointHost:     endpointHost,
			EndpointPort:     endpointPort,
			EndpointAddress:  endpointAddress,
			IngressMode:      ingressMode,
			SNIName:          sniName,
			RouteTarget:      routeTarget,
			AccessHint:       accessHint,
			Status:           strings.TrimSpace(string(logicalService.Status)),
			InstanceStatus:   strings.TrimSpace(string(serviceInstance.InstanceStatus)),
			HealthStatus:     strings.TrimSpace(string(serviceInstance.HealthStatus)),
			ActiveInstances:  logicalService.ActiveInstanceCount,
			HealthyInstances: logicalService.HealthyInstanceCount,
			UpdatedAtMS:      updatedAtMS,
		})
	}

	sort.Slice(items, func(left, right int) bool {
		if items[left].UpdatedAtMS == items[right].UpdatedAtMS {
			if items[left].ConnectorID == items[right].ConnectorID {
				return items[left].InstanceID < items[right].InstanceID
			}
			return items[left].ConnectorID < items[right].ConnectorID
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
			Binding:     strings.TrimSpace(tunnelRuntime.Binding),
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

// BuildTunnelSummaryFromRuntimes 基于 tunnel 列表构建汇总，适用于 connector 过滤后的聚合。
func BuildTunnelSummaryFromRuntimes(now time.Time, tunnels []registry.TunnelRuntime) TunnelSummarySnapshot {
	normalizedNow := now
	if normalizedNow.IsZero() {
		normalizedNow = time.Now().UTC()
	}
	summary := TunnelSummarySnapshot{
		UpdatedAtMS: uint64(normalizedNow.UnixMilli()),
	}
	latestUpdatedAtMS := uint64(0)
	for _, tunnelRuntime := range tunnels {
		switch tunnelRuntime.State {
		case registry.TunnelStateIdle:
			summary.Idle++
		case registry.TunnelStateReserved:
			summary.Reserved++
		case registry.TunnelStateActive:
			summary.Active++
		case registry.TunnelStateClosed:
			summary.Closed++
		case registry.TunnelStateBroken:
			summary.Broken++
		}
		summary.Total++
		if !tunnelRuntime.UpdatedAt.IsZero() {
			updatedAtMS := uint64(tunnelRuntime.UpdatedAt.UTC().UnixMilli())
			if updatedAtMS > latestUpdatedAtMS {
				latestUpdatedAtMS = updatedAtMS
			}
		}
	}
	if latestUpdatedAtMS == 0 {
		latestUpdatedAtMS = summary.UpdatedAtMS
	}
	summary.UpdatedAtMS = latestUpdatedAtMS
	return summary
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
		AcquireWaitCount:                  metrics.BridgeTunnelAcquireWaitCount(),
		AcquireWaitTotalMS:                metrics.BridgeTunnelAcquireWaitTotalMs(),
		OpenTimeoutTotal:                  metrics.BridgeTrafficOpenTimeoutTotal(),
		OpenRejectTotal:                   metrics.BridgeTrafficOpenRejectTotal(),
		OpenAckLateTotal:                  metrics.BridgeTrafficOpenAckLateTotal(),
		ScopeFallbackTotal:                metrics.BridgeScopeFallbackTotal(),
		RouteConflictRejectionTotal:       metrics.BridgeRouteConflictRejectionTotal(),
		HostDeriveSuccessTotal:            metrics.BridgeHostDeriveTotal(true),
		HostDeriveFailureTotal:            metrics.BridgeHostDeriveTotal(false),
		EndpointOverrideTotal:             metrics.BridgeActualEndpointOverrideTotal(),
		AuthSuccessTotal:                  metrics.BridgeAuthSuccessTotal(),
		AuthFailureTotal:                  metrics.BridgeAuthFailureTotal(),
		AuthRateLimitTotal:                metrics.BridgeAuthRateLimitTotal(),
		AuthSupersedeTotal:                metrics.BridgeAuthSupersedeTotal(),
		TLSRejectPlaintextOnRequiredTotal: metrics.BridgeTLSRejectPlaintextOnRequiredTotal(),
		TLSRejectTLSOnPlaintextTotal:      metrics.BridgeTLSRejectTLSOnPlaintextTotal(),
		TunnelRecycleFailureTotal:         metrics.BridgeTunnelRecycleFailureTotal(),
		QUICConnectionAcceptTotal:         metrics.BridgeQUICConnectionAcceptTotal(),
		QUICConnectionActive:              metrics.BridgeQUICConnectionActive(),
		QUICConnectionAuthenticatedTotal:  metrics.BridgeQUICConnectionAuthenticatedTotal(),
		QUICTunnelRegisteredTotal:         metrics.BridgeQUICTunnelRegisteredTotal(),
		AuthErrorCodeTotals:               metrics.BridgeAuthErrorCodeTotals(),
		TunnelRecycleErrorCodeTotals:      metrics.BridgeTunnelRecycleErrorCodeTotals(),
		UpdatedAtMS:                       uint64(normalizedNow.UnixMilli()),
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
		if session.State == registry.SessionStale || session.State == registry.SessionFailed {
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
	if trafficSummary.AuthFailureTotal > 0 {
		issues = append(issues, "检测到 connector 认证失败，请检查 token、TLS 模式与会话抢占行为")
	}
	if trafficSummary.TunnelRecycleFailureTotal > 0 {
		issues = append(issues, buildTunnelRecycleDiagnoseIssue(trafficSummary.TunnelRecycleErrorCodeTotals))
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

// buildTunnelRecycleDiagnoseIssue 按 recycle 错误码分布输出最有诊断价值的提示语。
func buildTunnelRecycleDiagnoseIssue(errorCodeTotals map[string]uint64) string {
	if errorCodeTotals[ltfperrors.CodeTunnelRecycleInvalidSeq] > 0 ||
		errorCodeTotals[ltfperrors.CodeTunnelRecycleTunnelMismatch] > 0 {
		return "检测到 tunnel recycle 协议状态不一致，请检查 tunnel_id/recycle_seq 收敛与双端状态机"
	}
	if errorCodeTotals[ltfperrors.CodeTunnelRecycleCloseAckRequired] > 0 {
		return "检测到 tunnel recycle 因 close_ack_required 失败，请检查 close_ack 闭环与 simultaneous close 时序"
	}
	if errorCodeTotals[ltfperrors.CodeTunnelRecycleBufferDirty] > 0 {
		return "检测到 tunnel recycle 因 buffer_dirty 失败，请检查 flush 排空与长尾响应"
	}
	if errorCodeTotals[ltfperrors.CodeTunnelRecycleDeadlineHit] > 0 {
		return "检测到 tunnel recycle 超时，请关注 close_ack 等待、flush 时延与池水位"
	}
	return "检测到 tunnel recycle 失败，请检查 tunnel 健康度、close_ack 闭环与缓冲排空状态"
}

func maxUint64(left uint64, right uint64) uint64 {
	if left > right {
		return left
	}
	return right
}
