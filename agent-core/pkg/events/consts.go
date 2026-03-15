package events

const (
	EventTrace = "trace"
	EventDebug = "debug"
	EventInfo  = "info"
	EventWarn  = "warn"
	EventError = "error"
)

const (
	ModuleAgentRuntime        = "agent.runtime"
	ModuleAgentRuntimeBridge  = "agent.runtime.bridge"
	ModuleAgentRuntimeTunnel  = "agent.runtime.tunnel"
	ModuleAgentRuntimeRefill  = "agent.runtime.refill"
	ModuleAgentRuntimeControl = "agent.runtime.control"
	ModuleAgentRuntimeRoute   = "agent.runtime.route"
	ModuleAgentRuntimeService = "agent.runtime.service"
)

const (
	CodeRuntimeEvent = "RUNTIME_EVENT"

	CodeSessionReconnectRequested = "SESSION_RECONNECT_REQUESTED"

	CodeBridgeStateConnecting      = "BRIDGE_STATE_CONNECTING"
	CodeBridgeStateActive          = "BRIDGE_STATE_ACTIVE"
	CodeBridgeStateDraining        = "BRIDGE_STATE_DRAINING"
	CodeBridgeStateStale           = "BRIDGE_STATE_STALE"
	CodeBridgeStateClosed          = "BRIDGE_STATE_CLOSED"
	CodeBridgeRetryScheduled       = "BRIDGE_RETRY_SCHEDULED"
	CodeBridgeReconnectEstablished = "BRIDGE_RECONNECT_ESTABLISHED"
	CodeBridgeControlError         = "BRIDGE_CONTROL_ERROR"

	CodeTunnelRefillPayloadInvalid  = "TUNNEL_REFILL_PAYLOAD_INVALID"
	CodeTunnelRefillRequestReceived = "TUNNEL_REFILL_REQUEST_RECEIVED"
	CodeTunnelRefillRejected        = "TUNNEL_REFILL_REJECTED"
	CodeTunnelRefillExpansionCheck  = "TUNNEL_REFILL_EXPANSION_CHECK"
	CodeTunnelRefillApplied         = "TUNNEL_REFILL_APPLIED"
	CodeTunnelRefillIgnored         = "TUNNEL_REFILL_IGNORED"
	CodeTunnelRefillRequested       = "TUNNEL_REFILL_REQUESTED"

	CodeTunnelPoolEvent                  = "TUNNEL_POOL_EVENT"
	CodeTunnelPoolChanged                = "TUNNEL_POOL_CHANGED"
	CodeTunnelPoolRebuilt                = "TUNNEL_POOL_REBUILT"
	CodeTunnelPoolSessionActive          = "TUNNEL_POOL_SESSION_ACTIVE"
	CodeTunnelPoolSessionDraining        = "TUNNEL_POOL_SESSION_DRAINING"
	CodeTunnelPoolSessionStale           = "TUNNEL_POOL_SESSION_STALE"
	CodeTunnelPoolStartupReconcileFailed = "TUNNEL_POOL_STARTUP_RECONCILE_FAILED"
	CodeTunnelPoolReportFailed           = "TUNNEL_POOL_REPORT_FAILED"
	CodeTunnelPoolReportTriggered        = "TUNNEL_POOL_REPORT_TRIGGERED"

	CodeTunnelIdleTTLReaped           = "TUNNEL_IDLE_TTL_REAPED"
	CodeTunnelCleanupClosed           = "TUNNEL_CLEANUP_CLOSED"
	CodeTunnelCleanupBroken           = "TUNNEL_CLEANUP_BROKEN"
	CodeTunnelIdleAcquired            = "TUNNEL_IDLE_ACQUIRED"
	CodeTunnelActive                  = "TUNNEL_ACTIVE"
	CodeTunnelDialAnnounceBuildFailed = "TUNNEL_DIAL_ANNOUNCE_BUILD_FAILED"
	CodeTunnelDialAnnounceSendFailed  = "TUNNEL_DIAL_ANNOUNCE_SEND_FAILED"
	CodeTunnelDialAnnounced           = "TUNNEL_DIAL_ANNOUNCED"

	CodeRouteAssignAccepted = "ROUTE_ASSIGN_ACCEPTED"
	CodeRouteAssignRejected = "ROUTE_ASSIGN_REJECTED"
	CodeRouteAssignSkipped  = "ROUTE_ASSIGN_SKIPPED"
	CodeRouteRevokeAccepted = "ROUTE_REVOKE_ACCEPTED"
	CodeRouteRevokeRejected = "ROUTE_REVOKE_REJECTED"

	CodeServiceSyncFailed             = "SERVICE_SYNC_FAILED"
	CodeServiceRouteRevokeBuildFailed = "SERVICE_ROUTE_REVOKE_BUILD_FAILED"
	CodeServiceRouteRevokeSendFailed  = "SERVICE_ROUTE_REVOKE_SEND_FAILED"
	CodeServiceUnpublishBuildFailed   = "SERVICE_UNPUBLISH_BUILD_FAILED"
	CodeServiceUnpublishSendFailed    = "SERVICE_UNPUBLISH_SEND_FAILED"
)

const (
	CodePrefixBridgeState      = "BRIDGE_STATE_"
	CodePrefixSessionReconnect = "SESSION_RECONNECT_"
	CodePrefixBridgeRetry      = "BRIDGE_RETRY_"
	CodePrefixTunnelRefill     = "TUNNEL_REFILL_"
	CodePrefixTunnelPoolReport = "TUNNEL_POOL_REPORT_"
)

const (
	// EventCode 为历史兼容别名，等价于 SESSION_RECONNECT_REQUESTED。
	EventCode = CodeSessionReconnectRequested
)
