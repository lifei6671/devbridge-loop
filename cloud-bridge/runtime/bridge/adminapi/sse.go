package adminapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/adminview"
)

const (
	// sseProtocolVersion 标识 SSE 协议版本，便于未来兼容升级。
	sseProtocolVersion = "v1"
	// sseDefaultInterval 定义服务端默认推送周期。
	sseDefaultInterval = 5 * time.Second
	// sseMinInterval 定义最短推送周期，避免过高刷新频率冲垮管理面。
	sseMinInterval = 1 * time.Second
	// sseMaxInterval 定义最长推送周期，避免“看起来在线但长时间无数据”。
	sseMaxInterval = 30 * time.Second
	// sseHeartbeatInterval 定义心跳事件间隔，用于连接保活与前端感知。
	sseHeartbeatInterval = 15 * time.Second

	// sseDefaultListLimit 对齐前端当前页数据量预设，避免一次推送过大。
	sseDefaultRouteLimit     = 100
	sseDefaultServiceLimit   = 120
	sseDefaultConnectorLimit = 100
	sseDefaultSessionLimit   = 100
	sseDefaultTunnelLimit    = 120
	sseDefaultLogLimit       = 80

	// sseDefaultTimeRangeMinutes 定义 observability 默认时间窗口。
	sseDefaultTimeRangeMinutes = 30
	// sseMaxTimeRangeMinutes 限制 observability 查询窗口上限（24h）。
	sseMaxTimeRangeMinutes = 24 * 60
)

const (
	sseEventReady     = "bridge.ready"
	sseEventSnapshot  = "bridge.snapshot"
	sseEventHeartbeat = "bridge.heartbeat"
)

type sseTopic string

const (
	sseTopicDashboard     sseTopic = "dashboard"
	sseTopicRoutes        sseTopic = "routes"
	sseTopicServices      sseTopic = "services"
	sseTopicConnectors    sseTopic = "connectors"
	sseTopicTraffic       sseTopic = "traffic"
	sseTopicOps           sseTopic = "ops"
	sseTopicObservability sseTopic = "observability"
)

type sseSnapshotQuery struct {
	sessionStateFilter string
	tunnelStateFilter  string
	tunnelConnectorID  string
	serviceStatus      string
	serviceHealth      string
	serviceType        string
	serviceQueryText   string
	timeRangeMinutes   int
}

// sseEnvelope 定义 SSE 数据帧统一协议头，方便前端按 type/topic 分发。
type sseEnvelope struct {
	Version      string         `json:"version"`
	Type         string         `json:"type"`
	Topic        string         `json:"topic,omitempty"`
	ServerTimeMS uint64         `json:"server_time_ms"`
	Sequence     uint64         `json:"sequence,omitempty"`
	IntervalMS   uint64         `json:"interval_ms,omitempty"`
	Topics       []string       `json:"topics,omitempty"`
	Payload      map[string]any `json:"payload,omitempty"`
}

// handleEventsStream 提供管理后台 SSE 推送通道，按 topic 输出页面快照。
func (server *Server) handleEventsStream(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "GET is required")
		return
	}
	flusher, ok := writer.(http.Flusher)
	if !ok {
		writeError(writer, http.StatusInternalServerError, "UNSUPPORTED_STREAM", "response writer does not support streaming")
		return
	}
	topics, topicsErr := parseSSETopicsQuery(request)
	if topicsErr != nil {
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", topicsErr.Error())
		return
	}
	interval, intervalErr := parseSSEIntervalQuery(request)
	if intervalErr != nil {
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", intervalErr.Error())
		return
	}
	snapshotQuery, snapshotErr := parseSSESnapshotQuery(request)
	if snapshotErr != nil {
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", snapshotErr.Error())
		return
	}
	setAuditParamSummary(
		writer,
		fmt.Sprintf(
			"topics=%s interval_ms=%d session_state=%s tunnel_state=%s connector_id=%s service_status=%s service_health=%s service_type=%s q=%s time_range_minutes=%d",
			strings.Join(sseTopicsToStrings(topics), ","),
			interval.Milliseconds(),
			normalizeAuditFilterValue(snapshotQuery.sessionStateFilter),
			normalizeAuditFilterValue(snapshotQuery.tunnelStateFilter),
			normalizeAuditFilterValue(snapshotQuery.tunnelConnectorID),
			normalizeAuditFilterValue(snapshotQuery.serviceStatus),
			normalizeAuditFilterValue(snapshotQuery.serviceHealth),
			normalizeAuditFilterValue(snapshotQuery.serviceType),
			normalizeAuditFilterValue(snapshotQuery.serviceQueryText),
			snapshotQuery.timeRangeMinutes,
		),
	)

	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("Connection", "keep-alive")
	// 关闭 Nginx 代理缓冲，避免实时事件被延迟聚合。
	writer.Header().Set("X-Accel-Buffering", "no")
	writer.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(writer, "retry: %d\n\n", interval.Milliseconds())
	flusher.Flush()

	sequence := uint64(1)
	readyEnvelope := sseEnvelope{
		Version:      sseProtocolVersion,
		Type:         "ready",
		ServerTimeMS: uint64(server.now().UnixMilli()),
		IntervalMS:   uint64(interval.Milliseconds()),
		Topics:       sseTopicsToStrings(topics),
	}
	if err := writeSSEJSONEvent(writer, flusher, sseEventReady, formatSSEEventID(sequence), readyEnvelope); err != nil {
		return
	}
	sequence++
	if err := server.writeTopicSnapshots(writer, flusher, topics, snapshotQuery, &sequence); err != nil {
		return
	}

	snapshotTicker := time.NewTicker(interval)
	heartbeatTicker := time.NewTicker(sseHeartbeatInterval)
	defer snapshotTicker.Stop()
	defer heartbeatTicker.Stop()

	for {
		select {
		case <-request.Context().Done():
			return
		case <-snapshotTicker.C:
			if err := server.writeTopicSnapshots(writer, flusher, topics, snapshotQuery, &sequence); err != nil {
				return
			}
		case <-heartbeatTicker.C:
			heartbeatEnvelope := sseEnvelope{
				Version:      sseProtocolVersion,
				Type:         "heartbeat",
				ServerTimeMS: uint64(server.now().UnixMilli()),
				Sequence:     sequence,
			}
			if err := writeSSEJSONEvent(
				writer,
				flusher,
				sseEventHeartbeat,
				formatSSEEventID(sequence),
				heartbeatEnvelope,
			); err != nil {
				return
			}
			sequence++
		}
	}
}

// writeTopicSnapshots 按订阅 topic 输出快照事件。
func (server *Server) writeTopicSnapshots(
	writer http.ResponseWriter,
	flusher http.Flusher,
	topics []sseTopic,
	query sseSnapshotQuery,
	sequence *uint64,
) error {
	if sequence == nil {
		return fmt.Errorf("write topic snapshots: nil sequence pointer")
	}
	for _, topic := range topics {
		payload := server.buildSSETopicPayload(topic, query)
		eventEnvelope := sseEnvelope{
			Version:      sseProtocolVersion,
			Type:         "snapshot",
			Topic:        string(topic),
			ServerTimeMS: uint64(server.now().UnixMilli()),
			Sequence:     *sequence,
			Payload:      payload,
		}
		if err := writeSSEJSONEvent(
			writer,
			flusher,
			sseEventSnapshot,
			formatSSEEventID(*sequence),
			eventEnvelope,
		); err != nil {
			return err
		}
		*sequence++
	}
	return nil
}

// buildSSETopicPayload 生成单个 topic 的快照载荷。
func (server *Server) buildSSETopicPayload(topic sseTopic, query sseSnapshotQuery) map[string]any {
	switch topic {
	case sseTopicDashboard:
		sessions := safeListSessions(server.dependencies)
		logicalServices := safeListLogicalServices(server.dependencies)
		routes := safeListRoutes(server.dependencies)
		tunnelSnapshot := safeTunnelSnapshot(server.dependencies)
		return map[string]any{
			"overview": adminview.BuildBridgeOverview(
				server.now(),
				sessions,
				logicalServices,
				routes,
				tunnelSnapshot,
				safeBuildConfigSnapshot(server.dependencies),
			),
			"tunnel_summary":   adminview.BuildTunnelSummary(server.now(), tunnelSnapshot),
			"traffic_summary":  adminview.BuildTrafficSummary(server.now(), server.dependencies.Metrics),
			"diagnose_summary": adminview.BuildDiagnoseSummary(server.now(), sessions, tunnelSnapshot, server.dependencies.Metrics),
		}
	case sseTopicRoutes:
		items := adminview.BuildRouteItems(safeListRoutes(server.dependencies))
		if len(items) > sseDefaultRouteLimit {
			items = items[:sseDefaultRouteLimit]
		}
		return map[string]any{
			"items": items,
		}
	case sseTopicServices:
		serviceItems := adminview.BuildServiceItems(
			server.now(),
			safeListLogicalServices(server.dependencies),
			safeListServiceInstances(server.dependencies),
			safeListSessions(server.dependencies),
		)
		if query.tunnelConnectorID != "" ||
			query.sessionStateFilter != "" ||
			query.serviceStatus != "" ||
			query.serviceHealth != "" ||
			query.serviceType != "" ||
			query.serviceQueryText != "" {
			filteredItems := make([]adminview.ServiceItem, 0, len(serviceItems))
			for _, item := range serviceItems {
				if query.tunnelConnectorID != "" && strings.TrimSpace(item.ConnectorID) != query.tunnelConnectorID {
					continue
				}
				if query.sessionStateFilter != "" && strings.ToUpper(strings.TrimSpace(item.SessionState)) != query.sessionStateFilter {
					continue
				}
				if query.serviceStatus != "" && strings.ToUpper(strings.TrimSpace(item.Status)) != query.serviceStatus {
					continue
				}
				if query.serviceHealth != "" && strings.ToUpper(strings.TrimSpace(item.HealthStatus)) != query.serviceHealth {
					continue
				}
				if query.serviceType != "" && strings.ToLower(strings.TrimSpace(item.ServiceType)) != query.serviceType {
					continue
				}
				if query.serviceQueryText != "" {
					searchText := strings.ToLower(
						strings.Join([]string{
							item.LogicalServiceID,
							item.InstanceID,
							item.ServiceName,
							item.ConnectorID,
							item.SessionID,
							item.EndpointAddress,
							item.SNIName,
						}, " "),
					)
					if !strings.Contains(searchText, query.serviceQueryText) {
						continue
					}
				}
				filteredItems = append(filteredItems, item)
			}
			serviceItems = filteredItems
		}
		if len(serviceItems) > sseDefaultServiceLimit {
			serviceItems = serviceItems[:sseDefaultServiceLimit]
		}
		return map[string]any{
			"items":                 serviceItems,
			"connector_id_filter":   normalizeAuditFilterValue(query.tunnelConnectorID),
			"session_state_filter":  normalizeAuditFilterValue(query.sessionStateFilter),
			"service_status_filter": normalizeAuditFilterValue(query.serviceStatus),
			"health_status_filter":  normalizeAuditFilterValue(query.serviceHealth),
			"service_type_filter":   normalizeAuditFilterValue(query.serviceType),
			"query_text_filter":     normalizeAuditFilterValue(query.serviceQueryText),
		}
	case sseTopicConnectors:
		connectorItems := adminview.BuildConnectorItems(
			safeListSessions(server.dependencies),
			safeListServiceInstances(server.dependencies),
		)
		if len(connectorItems) > sseDefaultConnectorLimit {
			connectorItems = connectorItems[:sseDefaultConnectorLimit]
		}
		sessionItems := adminview.BuildSessionItems(safeListSessions(server.dependencies))
		if query.sessionStateFilter != "" {
			filteredSessionItems := make([]adminview.SessionItem, 0, len(sessionItems))
			for _, item := range sessionItems {
				if strings.ToUpper(strings.TrimSpace(item.State)) != query.sessionStateFilter {
					continue
				}
				filteredSessionItems = append(filteredSessionItems, item)
			}
			sessionItems = filteredSessionItems
		}
		if len(sessionItems) > sseDefaultSessionLimit {
			sessionItems = sessionItems[:sseDefaultSessionLimit]
		}
		return map[string]any{
			"connectors":           connectorItems,
			"sessions":             sessionItems,
			"session_state_filter": normalizeAuditFilterValue(query.sessionStateFilter),
		}
	case sseTopicTraffic:
		tunnelRuntimes := filterTunnelsByConnector(
			safeListTunnels(server.dependencies),
			query.tunnelConnectorID,
		)
		tunnelSummary := adminview.BuildTunnelSummary(server.now(), safeTunnelSnapshot(server.dependencies))
		if query.tunnelConnectorID != "" {
			// 仅在 connector 过滤生效时按过滤后的 runtime 重新聚合，保证汇总口径一致。
			tunnelSummary = adminview.BuildTunnelSummaryFromRuntimes(server.now(), tunnelRuntimes)
		}
		tunnelItems := adminview.BuildTunnelItems(tunnelRuntimes)
		if query.tunnelStateFilter != "" {
			filteredTunnelItems := make([]adminview.TunnelItem, 0, len(tunnelItems))
			for _, item := range tunnelItems {
				if strings.ToLower(strings.TrimSpace(item.State)) != query.tunnelStateFilter {
					continue
				}
				filteredTunnelItems = append(filteredTunnelItems, item)
			}
			tunnelItems = filteredTunnelItems
		}
		if len(tunnelItems) > sseDefaultTunnelLimit {
			tunnelItems = tunnelItems[:sseDefaultTunnelLimit]
		}
		connectorItems := adminview.BuildConnectorItems(
			safeListSessions(server.dependencies),
			safeListServiceInstances(server.dependencies),
		)
		if len(connectorItems) > sseDefaultConnectorLimit {
			connectorItems = connectorItems[:sseDefaultConnectorLimit]
		}
		agentPoolReports := filterTunnelPoolReportsByConnector(
			safeListTunnelPoolReports(server.dependencies),
			query.tunnelConnectorID,
		)
		return map[string]any{
			"tunnel_summary":          tunnelSummary,
			"agent_pool_summary":      buildAgentTunnelPoolSummary(server.now(), agentPoolReports),
			"tunnels":                 tunnelItems,
			"connectors":              connectorItems,
			"traffic_summary":         adminview.BuildTrafficSummary(server.now(), server.dependencies.Metrics),
			"tunnel_state_filter":     normalizeAuditFilterValue(query.tunnelStateFilter),
			"tunnel_connector_filter": normalizeAuditFilterValue(query.tunnelConnectorID),
		}
	case sseTopicOps:
		configSnapshot := map[string]any{}
		if server.dependencies.BuildConfigSnapshot != nil {
			configSnapshot = server.dependencies.BuildConfigSnapshot()
		}
		connectorItems := adminview.BuildConnectorItems(
			safeListSessions(server.dependencies),
			safeListServiceInstances(server.dependencies),
		)
		if len(connectorItems) > sseDefaultConnectorLimit {
			connectorItems = connectorItems[:sseDefaultConnectorLimit]
		}
		sessionItems := adminview.BuildSessionItems(safeListSessions(server.dependencies))
		if len(sessionItems) > sseDefaultSessionLimit {
			sessionItems = sessionItems[:sseDefaultSessionLimit]
		}
		return map[string]any{
			"snapshot":   configSnapshot,
			"connectors": connectorItems,
			"sessions":   sessionItems,
		}
	case sseTopicObservability:
		now := server.now()
		toMS := uint64(now.UnixMilli())
		fromMS := now.Add(-time.Duration(query.timeRangeMinutes) * time.Minute).UnixMilli()
		if fromMS < 0 {
			fromMS = 0
		}
		logItems := server.auditLogs.query(uint64(fromMS), toMS)
		pagedLogs, _ := paginate(logItems, pageQuery{
			offset: 0,
			limit:  sseDefaultLogLimit,
		})
		trafficSummary := adminview.BuildTrafficSummary(now, server.dependencies.Metrics)
		return map[string]any{
			"from_ms": uint64(fromMS),
			"to_ms":   toMS,
			"logs":    pagedLogs,
			"metrics": []map[string]any{
				{
					"ts_ms":                               toMS,
					"acquire_wait_count":                  trafficSummary.AcquireWaitCount,
					"acquire_wait_total_ms":               trafficSummary.AcquireWaitTotalMS,
					"open_timeout_total":                  trafficSummary.OpenTimeoutTotal,
					"open_reject_total":                   trafficSummary.OpenRejectTotal,
					"open_ack_late_total":                 trafficSummary.OpenAckLateTotal,
					"scope_fallback_total":                trafficSummary.ScopeFallbackTotal,
					"route_conflict_rejection_total":      trafficSummary.RouteConflictRejectionTotal,
					"host_derive_success_total":           trafficSummary.HostDeriveSuccessTotal,
					"host_derive_failure_total":           trafficSummary.HostDeriveFailureTotal,
					"endpoint_override_total":             trafficSummary.EndpointOverrideTotal,
					"quic_connection_accept_total":        trafficSummary.QUICConnectionAcceptTotal,
					"quic_connection_active":              trafficSummary.QUICConnectionActive,
					"quic_connection_authenticated_total": trafficSummary.QUICConnectionAuthenticatedTotal,
					"quic_tunnel_registered_total":        trafficSummary.QUICTunnelRegisteredTotal,
				},
			},
			"diagnose_summary":   adminview.BuildDiagnoseSummary(now, safeListSessions(server.dependencies), safeTunnelSnapshot(server.dependencies), server.dependencies.Metrics),
			"time_range_minutes": query.timeRangeMinutes,
		}
	default:
		return map[string]any{}
	}
}

// writeSSEJSONEvent 以 SSE 标准格式输出 JSON 事件并主动 flush。
func writeSSEJSONEvent(
	writer http.ResponseWriter,
	flusher http.Flusher,
	eventName string,
	eventID string,
	payload any,
) error {
	encodedPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal sse payload: %w", err)
	}
	if _, err := fmt.Fprintf(writer, "event: %s\n", strings.TrimSpace(eventName)); err != nil {
		return err
	}
	if strings.TrimSpace(eventID) != "" {
		if _, err := fmt.Fprintf(writer, "id: %s\n", strings.TrimSpace(eventID)); err != nil {
			return err
		}
	}
	// 兼容 payload 中可能出现换行的场景，逐行写 data 前缀。
	for _, line := range strings.Split(string(encodedPayload), "\n") {
		if _, err := fmt.Fprintf(writer, "data: %s\n", line); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprint(writer, "\n"); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

func formatSSEEventID(sequence uint64) string {
	return fmt.Sprintf("%d", sequence)
}

func parseSSETopicsQuery(request *http.Request) ([]sseTopic, error) {
	rawTopics := ""
	if request != nil && request.URL != nil {
		rawTopics = strings.TrimSpace(request.URL.Query().Get("topics"))
	}
	if rawTopics == "" {
		return []sseTopic{sseTopicDashboard}, nil
	}
	if strings.EqualFold(rawTopics, "all") {
		return allSSETopics(), nil
	}
	seenTopics := make(map[sseTopic]struct{})
	topics := make([]sseTopic, 0, 4)
	for _, rawTopic := range strings.Split(rawTopics, ",") {
		topic, ok := normalizeSSETopic(rawTopic)
		if !ok {
			return nil, fmt.Errorf("unsupported topics value: %s", strings.TrimSpace(rawTopic))
		}
		if _, exists := seenTopics[topic]; exists {
			continue
		}
		seenTopics[topic] = struct{}{}
		topics = append(topics, topic)
	}
	if len(topics) == 0 {
		return nil, fmt.Errorf("topics is empty")
	}
	return topics, nil
}

func parseSSEIntervalQuery(request *http.Request) (time.Duration, error) {
	rawIntervalMS := ""
	if request != nil && request.URL != nil {
		rawIntervalMS = strings.TrimSpace(request.URL.Query().Get("interval_ms"))
	}
	if rawIntervalMS == "" {
		return sseDefaultInterval, nil
	}
	parsedIntervalMS, err := strconv.Atoi(rawIntervalMS)
	if err != nil || parsedIntervalMS <= 0 {
		return 0, fmt.Errorf("invalid interval_ms")
	}
	if parsedIntervalMS < int(sseMinInterval.Milliseconds()) {
		parsedIntervalMS = int(sseMinInterval.Milliseconds())
	}
	if parsedIntervalMS > int(sseMaxInterval.Milliseconds()) {
		parsedIntervalMS = int(sseMaxInterval.Milliseconds())
	}
	return time.Duration(parsedIntervalMS) * time.Millisecond, nil
}

func parseSSESnapshotQuery(request *http.Request) (sseSnapshotQuery, error) {
	snapshotQuery := sseSnapshotQuery{
		timeRangeMinutes: sseDefaultTimeRangeMinutes,
	}
	if request == nil || request.URL == nil {
		return snapshotQuery, nil
	}
	snapshotQuery.sessionStateFilter = strings.ToUpper(strings.TrimSpace(request.URL.Query().Get("session_state")))
	if snapshotQuery.sessionStateFilter == "ALL" {
		snapshotQuery.sessionStateFilter = ""
	}
	snapshotQuery.tunnelStateFilter = strings.ToLower(strings.TrimSpace(request.URL.Query().Get("tunnel_state")))
	if snapshotQuery.tunnelStateFilter == "all" {
		snapshotQuery.tunnelStateFilter = ""
	}
	snapshotQuery.tunnelConnectorID = strings.TrimSpace(request.URL.Query().Get("connector_id"))
	if strings.EqualFold(snapshotQuery.tunnelConnectorID, "all") {
		snapshotQuery.tunnelConnectorID = ""
	}
	snapshotQuery.serviceStatus = strings.ToUpper(strings.TrimSpace(request.URL.Query().Get("status")))
	if snapshotQuery.serviceStatus == "ALL" {
		snapshotQuery.serviceStatus = ""
	}
	snapshotQuery.serviceHealth = strings.ToUpper(strings.TrimSpace(request.URL.Query().Get("health_status")))
	if snapshotQuery.serviceHealth == "ALL" {
		snapshotQuery.serviceHealth = ""
	}
	snapshotQuery.serviceType = strings.ToLower(strings.TrimSpace(request.URL.Query().Get("service_type")))
	if snapshotQuery.serviceType == "all" {
		snapshotQuery.serviceType = ""
	}
	snapshotQuery.serviceQueryText = strings.ToLower(strings.TrimSpace(request.URL.Query().Get("q")))
	rawTimeRangeMinutes := strings.TrimSpace(request.URL.Query().Get("time_range_minutes"))
	if rawTimeRangeMinutes == "" {
		return snapshotQuery, nil
	}
	parsedTimeRangeMinutes, err := strconv.Atoi(rawTimeRangeMinutes)
	if err != nil || parsedTimeRangeMinutes <= 0 {
		return sseSnapshotQuery{}, fmt.Errorf("invalid time_range_minutes")
	}
	if parsedTimeRangeMinutes > sseMaxTimeRangeMinutes {
		parsedTimeRangeMinutes = sseMaxTimeRangeMinutes
	}
	snapshotQuery.timeRangeMinutes = parsedTimeRangeMinutes
	return snapshotQuery, nil
}

func normalizeSSETopic(rawTopic string) (sseTopic, bool) {
	switch strings.ToLower(strings.TrimSpace(rawTopic)) {
	case string(sseTopicDashboard):
		return sseTopicDashboard, true
	case string(sseTopicRoutes):
		return sseTopicRoutes, true
	case string(sseTopicServices):
		return sseTopicServices, true
	case string(sseTopicConnectors):
		return sseTopicConnectors, true
	case string(sseTopicTraffic):
		return sseTopicTraffic, true
	case string(sseTopicOps):
		return sseTopicOps, true
	case string(sseTopicObservability):
		return sseTopicObservability, true
	default:
		return "", false
	}
}

func allSSETopics() []sseTopic {
	return []sseTopic{
		sseTopicDashboard,
		sseTopicRoutes,
		sseTopicServices,
		sseTopicConnectors,
		sseTopicTraffic,
		sseTopicOps,
		sseTopicObservability,
	}
}

func sseTopicsToStrings(topics []sseTopic) []string {
	result := make([]string, 0, len(topics))
	for _, topic := range topics {
		result = append(result, string(topic))
	}
	return result
}

func normalizeAuditFilterValue(value string) string {
	normalizedValue := strings.TrimSpace(value)
	if normalizedValue == "" {
		return "ALL"
	}
	return normalizedValue
}
