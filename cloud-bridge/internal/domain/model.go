package domain

import "time"

const (
	// TunnelMessageHELLO 表示 agent 建连握手。
	TunnelMessageHELLO = "HELLO"
	// TunnelMessageHELLOAck 表示 bridge 对握手应答。
	TunnelMessageHELLOAck = "HELLO_ACK"
	// TunnelMessageFullSyncRequest 表示 agent 请求全量同步窗口。
	TunnelMessageFullSyncRequest = "FULL_SYNC_REQUEST"
	// TunnelMessageFullSyncSnapshot 表示 agent 发送全量快照。
	TunnelMessageFullSyncSnapshot = "FULL_SYNC_SNAPSHOT"
	// TunnelMessageRegisterUpsert 表示注册项新增或更新。
	TunnelMessageRegisterUpsert = "REGISTER_UPSERT"
	// TunnelMessageRegisterDelete 表示注册项删除。
	TunnelMessageRegisterDelete = "REGISTER_DELETE"
	// TunnelMessageTunnelHeartbeat 表示 tunnel 心跳。
	TunnelMessageTunnelHeartbeat = "TUNNEL_HEARTBEAT"
	// TunnelMessageACK 表示事件处理成功。
	TunnelMessageACK = "ACK"
	// TunnelMessageError 表示事件处理失败。
	TunnelMessageError = "ERROR"
)

const (
	// EventStatusAccepted 表示事件被成功应用。
	EventStatusAccepted = "accepted"
	// EventStatusDuplicate 表示事件重复，已按幂等语义忽略。
	EventStatusDuplicate = "duplicate"
	// EventStatusRejected 表示事件被拒绝。
	EventStatusRejected = "rejected"
)

const (
	// SyncErrorInvalidPayload 表示消息体不合法。
	SyncErrorInvalidPayload = "INVALID_PAYLOAD"
	// SyncErrorMissingTunnelID 表示无法从 payload 中识别 tunnelId。
	SyncErrorMissingTunnelID = "MISSING_TUNNEL_ID"
	// SyncErrorUnknownSession 表示该 tunnel 尚未完成 HELLO 建连。
	SyncErrorUnknownSession = "UNKNOWN_TUNNEL_SESSION"
	// SyncErrorStaleEpochEvent 表示收到旧 epoch 事件。
	SyncErrorStaleEpochEvent = "STALE_EPOCH_EVENT"
	// SyncErrorEpochTransition 表示新 epoch 未先发送 HELLO。
	SyncErrorEpochTransition = "EPOCH_TRANSITION_REQUIRES_HELLO"
	// SyncErrorUnsupportedType 表示不支持的消息类型。
	SyncErrorUnsupportedType = "UNSUPPORTED_MESSAGE_TYPE"
	// SyncErrorProtocolDisabled 表示当前 tunnel 同步协议在 bridge 端未启用。
	SyncErrorProtocolDisabled = "TUNNEL_PROTOCOL_DISABLED"
)

// TunnelSession 描述一条 agent 到 bridge 的会话状态。
type TunnelSession struct {
	TunnelID        string    `json:"tunnelId"`
	RDName          string    `json:"rdName"`
	ConnID          string    `json:"connId"`
	BackflowBaseURL string    `json:"backflowBaseUrl"`
	SessionEpoch    int64     `json:"sessionEpoch"`
	ResourceVersion int64     `json:"resourceVersion"`
	Status          string    `json:"status"`
	LastHeartbeatAt time.Time `json:"lastHeartbeatAt"`
	ConnectedAt     time.Time `json:"connectedAt"`
}

// ActiveIntercept 代表 bridge 当前生效的接管关系。
type ActiveIntercept struct {
	Env         string    `json:"env"`
	ServiceName string    `json:"serviceName"`
	Protocol    string    `json:"protocol"`
	TunnelID    string    `json:"tunnelId"`
	InstanceID  string    `json:"instanceId"`
	TargetPort  int       `json:"targetPort"`
	Status      string    `json:"status"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// BridgeRoute 代表 bridge 暴露的最终入口路由。
type BridgeRoute struct {
	Env         string `json:"env"`
	ServiceName string `json:"serviceName"`
	Protocol    string `json:"protocol"`
	BridgeHost  string `json:"bridgeHost"`
	BridgePort  int    `json:"bridgePort"`
	TunnelID    string `json:"tunnelId"`
	TargetPort  int    `json:"targetPort"`
}

// TunnelEvent 为 tunnel 同步消息统一模型。
type TunnelEvent struct {
	Type            string         `json:"type"`
	SessionEpoch    int64          `json:"sessionEpoch"`
	ResourceVersion int64          `json:"resourceVersion"`
	EventID         string         `json:"eventId"`
	SentAt          time.Time      `json:"sentAt"`
	Payload         map[string]any `json:"payload"`
}

// TunnelEventReply 为 tunnel 事件处理结果（ACK/ERROR）。
type TunnelEventReply struct {
	Type            string `json:"type"`
	Status          string `json:"status"`
	EventID         string `json:"eventId"`
	SessionEpoch    int64  `json:"sessionEpoch"`
	ResourceVersion int64  `json:"resourceVersion"`
	Deduplicated    bool   `json:"deduplicated"`
	ErrorCode       string `json:"errorCode,omitempty"`
	Message         string `json:"message,omitempty"`
}

// ErrorEntry 表示一条 bridge 运行时错误记录。
type ErrorEntry struct {
	Code       string            `json:"code"`
	Message    string            `json:"message"`
	Context    map[string]string `json:"context"`
	OccurredAt time.Time         `json:"occurredAt"`
}

// ErrorCodeStat 表示按错误码聚合后的统计项。
type ErrorCodeStat struct {
	Code  string `json:"code"`
	Count int    `json:"count"`
}

// ErrorStats 描述 bridge 错误统计快照。
type ErrorStats struct {
	Total      int             `json:"total"`
	UniqueCode int             `json:"uniqueCode"`
	ByCode     []ErrorCodeStat `json:"byCode"`
	Recent     []ErrorEntry    `json:"recent"`
}

// HelloPayload 为 HELLO 消息载荷。
type HelloPayload struct {
	TunnelID        string `json:"tunnelId"`
	RDName          string `json:"rdName"`
	ConnID          string `json:"connId"`
	BackflowBaseURL string `json:"backflowBaseUrl,omitempty"`
}

// TunnelHeartbeatPayload 为 TUNNEL_HEARTBEAT 消息载荷。
type TunnelHeartbeatPayload struct {
	TunnelID string `json:"tunnelId"`
	ConnID   string `json:"connId,omitempty"`
}

// SnapshotEndpoint 为全量快照中的 endpoint 视图。
type SnapshotEndpoint struct {
	Protocol   string `json:"protocol"`
	TargetPort int    `json:"targetPort"`
	Status     string `json:"status"`
}

// SnapshotRegistration 为全量快照中的注册项。
type SnapshotRegistration struct {
	Env         string             `json:"env"`
	ServiceName string             `json:"serviceName"`
	InstanceID  string             `json:"instanceId"`
	Endpoints   []SnapshotEndpoint `json:"endpoints"`
}

// FullSyncSnapshotPayload 为 FULL_SYNC_SNAPSHOT 消息载荷。
type FullSyncSnapshotPayload struct {
	TunnelID      string                 `json:"tunnelId"`
	RDName        string                 `json:"rdName"`
	BridgeHost    string                 `json:"bridgeHost,omitempty"`
	BridgePort    int                    `json:"bridgePort,omitempty"`
	Registrations []SnapshotRegistration `json:"registrations"`
}

// RegisterUpsertPayload 为 REGISTER_UPSERT 消息载荷。
type RegisterUpsertPayload struct {
	TunnelID    string `json:"tunnelId"`
	Env         string `json:"env"`
	ServiceName string `json:"serviceName"`
	Protocol    string `json:"protocol"`
	InstanceID  string `json:"instanceId"`
	TargetPort  int    `json:"targetPort"`
	Status      string `json:"status,omitempty"`
	BridgeHost  string `json:"bridgeHost,omitempty"`
	BridgePort  int    `json:"bridgePort,omitempty"`
}

// RegisterDeletePayload 为 REGISTER_DELETE 消息载荷。
type RegisterDeletePayload struct {
	TunnelID    string `json:"tunnelId"`
	Env         string `json:"env"`
	ServiceName string `json:"serviceName"`
	Protocol    string `json:"protocol"`
	InstanceID  string `json:"instanceId,omitempty"`
	TargetPort  int    `json:"targetPort,omitempty"`
}
