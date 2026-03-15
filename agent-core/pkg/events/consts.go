package events

const (
	// EventTrace 表示 trace 级别事件。
	EventTrace = "trace"
	// EventDebug 表示 debug 级别事件。
	EventDebug = "debug"
	// EventInfo 表示 info 级别事件。
	EventInfo = "info"
	// EventWarn 表示 warn 级别事件。
	EventWarn = "warn"
	// EventError 表示 error 级别事件。
	EventError = "error"
)

const (
	// ModuleAgentRuntime 表示 agent 运行时通用模块。
	ModuleAgentRuntime = "agent.runtime"
	// ModuleAgentRuntimeBridge 表示 bridge 会话/连接模块。
	ModuleAgentRuntimeBridge = "agent.runtime.bridge"
	// ModuleAgentRuntimeTunnel 表示 tunnel 生命周期与池管理模块。
	ModuleAgentRuntimeTunnel = "agent.runtime.tunnel"
	// ModuleAgentRuntimeRefill 表示 tunnel 补池与对账模块。
	ModuleAgentRuntimeRefill = "agent.runtime.refill"
	// ModuleAgentRuntimeControl 表示控制面消息处理模块。
	ModuleAgentRuntimeControl = "agent.runtime.control"
	// ModuleAgentRuntimeRoute 表示 route 同步与 ACK 处理模块。
	ModuleAgentRuntimeRoute = "agent.runtime.route"
	// ModuleAgentRuntimeService 表示服务目录增删改同步模块。
	ModuleAgentRuntimeService = "agent.runtime.service"
)

const (
	// BridgeStateUnavailable 表示 bridge 状态未知或不可用。
	BridgeStateUnavailable = "UNAVAILABLE"
	// BridgeStateConnecting 表示 bridge 控制通道正在建立连接。
	BridgeStateConnecting = "CONNECTING"
	// BridgeStateActive 表示 bridge 控制通道已建立并可用。
	BridgeStateActive = "ACTIVE"
	// BridgeStateReconnecting 表示 bridge 正在执行重连流程。
	BridgeStateReconnecting = "RECONNECTING"
	// BridgeStateDraining 表示 bridge 正在排空并拒绝新流量。
	BridgeStateDraining = "DRAINING"
	// BridgeStateStale 表示 bridge 会话已失活但尚未完成重建。
	BridgeStateStale = "STALE"
	// BridgeStateClosed 表示 bridge 控制通道已关闭。
	BridgeStateClosed = "CLOSED"
)

const (
	// CodeRuntimeEvent 表示运行时默认事件码。
	CodeRuntimeEvent = "RUNTIME_EVENT"

	// CodeSessionReconnectRequested 表示请求触发会话重连。
	CodeSessionReconnectRequested = "SESSION_RECONNECT_REQUESTED"

	// CodeTCPFramedDialTunnelErr 通过 TCP Frame 方式连接 Tunnel 失败了
	CodeTCPFramedDialTunnelErr = "TCP_FRAMED_DIAL_TUNNEL_ERROR"

	// CodeGRPCTunnelStreamErr GRPC 连接 Tunnel 失败
	CodeGRPCTunnelStreamErr = "GRPC_TUNNEL_STREAM_ERROE"

	// CodeBridgeStateConnecting 表示 bridge 正在连接。
	CodeBridgeStateConnecting = "BRIDGE_STATE_CONNECTING"
	// CodeBridgeStateActive 表示 bridge 已连接并处于 ACTIVE。
	CodeBridgeStateActive = "BRIDGE_STATE_ACTIVE"
	// CodeBridgeStateDraining 表示 bridge 进入排空态。
	CodeBridgeStateDraining = "BRIDGE_STATE_DRAINING"
	// CodeBridgeStateStale 表示 bridge 会话失活或陈旧。
	CodeBridgeStateStale = "BRIDGE_STATE_STALE"
	// CodeBridgeStateClosed 表示 bridge 会话已关闭。
	CodeBridgeStateClosed = "BRIDGE_STATE_CLOSED"
	// CodeBridgeRetryScheduled 表示 bridge 已安排重试。
	CodeBridgeRetryScheduled = "BRIDGE_RETRY_SCHEDULED"
	// CodeBridgeReconnectEstablished 表示 bridge 重连建立成功。
	CodeBridgeReconnectEstablished = "BRIDGE_RECONNECT_ESTABLISHED"
	// CodeBridgeControlError 表示收到控制面错误。
	CodeBridgeControlError = "BRIDGE_CONTROL_ERROR"

	// CodeTunnelRefillPayloadInvalid 表示补池请求 payload 非法。
	CodeTunnelRefillPayloadInvalid = "TUNNEL_REFILL_PAYLOAD_INVALID"
	// CodeTunnelRefillRequestReceived 表示收到补池请求。
	CodeTunnelRefillRequestReceived = "TUNNEL_REFILL_REQUEST_RECEIVED"
	// CodeTunnelRefillRejected 表示补池请求被拒绝。
	CodeTunnelRefillRejected = "TUNNEL_REFILL_REJECTED"
	// CodeTunnelRefillExpansionCheck 表示已执行扩容必要性检查。
	CodeTunnelRefillExpansionCheck = "TUNNEL_REFILL_EXPANSION_CHECK"
	// CodeTunnelRefillApplied 表示补池动作已执行。
	CodeTunnelRefillApplied = "TUNNEL_REFILL_APPLIED"
	// CodeTunnelRefillIgnored 表示补池请求被忽略（如已满足）。
	CodeTunnelRefillIgnored = "TUNNEL_REFILL_IGNORED"
	// CodeTunnelRefillRequested 表示运行时触发了补池请求。
	CodeTunnelRefillRequested = "TUNNEL_REFILL_REQUESTED"

	// CodeTunnelPoolEvent 表示 tunnel 池通用事件。
	CodeTunnelPoolEvent = "TUNNEL_POOL_EVENT"
	// CodeTunnelPoolChanged 表示 tunnel 池计数发生变化。
	CodeTunnelPoolChanged = "TUNNEL_POOL_CHANGED"
	// CodeTunnelPoolRebuilt 表示 tunnel 池完成重建。
	CodeTunnelPoolRebuilt = "TUNNEL_POOL_REBUILT"
	// CodeTunnelPoolSessionActive 表示会话 ACTIVE 时的池事件。
	CodeTunnelPoolSessionActive = "TUNNEL_POOL_SESSION_ACTIVE"
	// CodeTunnelPoolSessionDraining 表示会话 DRAINING 时的池事件。
	CodeTunnelPoolSessionDraining = "TUNNEL_POOL_SESSION_DRAINING"
	// CodeTunnelPoolSessionStale 表示会话 STALE 时的池事件。
	CodeTunnelPoolSessionStale = "TUNNEL_POOL_SESSION_STALE"
	// CodeTunnelPoolStartupReconcileFailed 表示启动期池对账失败。
	CodeTunnelPoolStartupReconcileFailed = "TUNNEL_POOL_STARTUP_RECONCILE_FAILED"
	// CodeTunnelPoolReportFailed 表示 tunnel 池上报失败。
	CodeTunnelPoolReportFailed = "TUNNEL_POOL_REPORT_FAILED"
	// CodeTunnelPoolReportTriggered 表示触发了 tunnel 池上报。
	CodeTunnelPoolReportTriggered = "TUNNEL_POOL_REPORT_TRIGGERED"

	// CodeTunnelIdleTTLReaped 表示 idle tunnel 因 TTL 被回收。
	CodeTunnelIdleTTLReaped = "TUNNEL_IDLE_TTL_REAPED"
	// CodeTunnelCleanupClosed 表示 tunnel 以 closed 路径完成清理。
	CodeTunnelCleanupClosed = "TUNNEL_CLEANUP_CLOSED"
	// CodeTunnelCleanupBroken 表示 tunnel 以 broken 路径完成清理。
	CodeTunnelCleanupBroken = "TUNNEL_CLEANUP_BROKEN"
	// CodeTunnelIdleAcquired 表示获取到 idle tunnel。
	CodeTunnelIdleAcquired = "TUNNEL_IDLE_ACQUIRED"
	// CodeTunnelActive 表示 tunnel 切换为 active。
	CodeTunnelActive = "TUNNEL_ACTIVE"
	// CodeTunnelDialAnnounceBuildFailed 表示 dial announce 构建失败。
	CodeTunnelDialAnnounceBuildFailed = "TUNNEL_DIAL_ANNOUNCE_BUILD_FAILED"
	// CodeTunnelDialAnnounceSendFailed 表示 dial announce 发送失败。
	CodeTunnelDialAnnounceSendFailed = "TUNNEL_DIAL_ANNOUNCE_SEND_FAILED"
	// CodeTunnelDialAnnounced 表示 dial announce 已发送。
	CodeTunnelDialAnnounced = "TUNNEL_DIAL_ANNOUNCED"

	// CodeRouteAssignAccepted 表示 RouteAssign 被接受。
	CodeRouteAssignAccepted = "ROUTE_ASSIGN_ACCEPTED"
	// CodeRouteAssignRejected 表示 RouteAssign 被拒绝。
	CodeRouteAssignRejected = "ROUTE_ASSIGN_REJECTED"
	// CodeRouteAssignSkipped 表示 RouteAssign 被跳过（如不适用协议）。
	CodeRouteAssignSkipped = "ROUTE_ASSIGN_SKIPPED"
	// CodeRouteRevokeAccepted 表示 RouteRevoke 被接受。
	CodeRouteRevokeAccepted = "ROUTE_REVOKE_ACCEPTED"
	// CodeRouteRevokeRejected 表示 RouteRevoke 被拒绝。
	CodeRouteRevokeRejected = "ROUTE_REVOKE_REJECTED"

	// CodeServiceSyncFailed 表示服务目录同步失败。
	CodeServiceSyncFailed = "SERVICE_SYNC_FAILED"
	// CodeServiceRouteRevokeBuildFailed 表示服务路由撤销构建失败。
	CodeServiceRouteRevokeBuildFailed = "SERVICE_ROUTE_REVOKE_BUILD_FAILED"
	// CodeServiceRouteRevokeSendFailed 表示服务路由撤销发送失败。
	CodeServiceRouteRevokeSendFailed = "SERVICE_ROUTE_REVOKE_SEND_FAILED"
	// CodeServiceUnpublishBuildFailed 表示服务下线消息构建失败。
	CodeServiceUnpublishBuildFailed = "SERVICE_UNPUBLISH_BUILD_FAILED"
	// CodeServiceUnpublishSendFailed 表示服务下线消息发送失败。
	CodeServiceUnpublishSendFailed = "SERVICE_UNPUBLISH_SEND_FAILED"
)

const (
	// CodePrefixBridgeState 表示 bridge 状态类事件码前缀。
	CodePrefixBridgeState = "BRIDGE_STATE_"
	// CodePrefixSessionReconnect 表示会话重连类事件码前缀。
	CodePrefixSessionReconnect = "SESSION_RECONNECT_"
	// CodePrefixBridgeRetry 表示 bridge 重试类事件码前缀。
	CodePrefixBridgeRetry = "BRIDGE_RETRY_"
	// CodePrefixTunnelRefill 表示 tunnel 补池类事件码前缀。
	CodePrefixTunnelRefill = "TUNNEL_REFILL_"
	// CodePrefixTunnelPoolReport 表示 tunnel 池上报类事件码前缀。
	CodePrefixTunnelPoolReport = "TUNNEL_POOL_REPORT_"
)

const (
	// EventCode 为历史兼容别名，等价于 SESSION_RECONNECT_REQUESTED。
	EventCode = CodeSessionReconnectRequested
)
