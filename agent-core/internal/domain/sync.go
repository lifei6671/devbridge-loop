package domain

import "time"

// TunnelMessageType 定义 agent 与 bridge 间的消息类型。
type TunnelMessageType string

const (
	MessageHello            TunnelMessageType = "HELLO"
	MessageHelloACK         TunnelMessageType = "HELLO_ACK"
	MessageFullSyncRequest  TunnelMessageType = "FULL_SYNC_REQUEST"
	MessageFullSyncSnapshot TunnelMessageType = "FULL_SYNC_SNAPSHOT"
	MessageRegisterUpsert   TunnelMessageType = "REGISTER_UPSERT"
	MessageRegisterDelete   TunnelMessageType = "REGISTER_DELETE"
	MessageTunnelHeartbeat  TunnelMessageType = "TUNNEL_HEARTBEAT"
	MessageACK              TunnelMessageType = "ACK"
	MessageError            TunnelMessageType = "ERROR"
)

// SyncEventMeta 为所有同步消息统一幂等头。
type SyncEventMeta struct {
	SessionEpoch    int64     `json:"sessionEpoch"`
	ResourceVersion int64     `json:"resourceVersion"`
	EventID         string    `json:"eventId"`
	SentAt          time.Time `json:"sentAt"`
}

// TunnelMessage 为协议无关消息封装。
type TunnelMessage struct {
	Type TunnelMessageType `json:"type"`
	SyncEventMeta
	Payload any `json:"payload,omitempty"`
}

// TunnelReply 为 bridge 回包结构（ACK/ERROR）。
type TunnelReply struct {
	Type            TunnelMessageType `json:"type"`
	Status          string            `json:"status"`
	EventID         string            `json:"eventId"`
	SessionEpoch    int64             `json:"sessionEpoch"`
	ResourceVersion int64             `json:"resourceVersion"`
	Deduplicated    bool              `json:"deduplicated"`
	ErrorCode       string            `json:"errorCode,omitempty"`
	Message         string            `json:"message,omitempty"`
}

// HelloPayload 为 HELLO 消息负载。
type HelloPayload struct {
	TunnelID        string `json:"tunnelId"`
	RDName          string `json:"rdName"`
	ConnID          string `json:"connId"`
	BackflowBaseURL string `json:"backflowBaseUrl,omitempty"`
}

// FullSyncRequestPayload 为 FULL_SYNC_REQUEST 消息负载。
type FullSyncRequestPayload struct {
	TunnelID string `json:"tunnelId"`
	RDName   string `json:"rdName"`
}

// SnapshotEndpoint 为全量快照 endpoint 结构。
type SnapshotEndpoint struct {
	Protocol   string `json:"protocol"`
	TargetPort int    `json:"targetPort"`
	Status     string `json:"status"`
}

// SnapshotRegistration 为全量快照注册项结构。
type SnapshotRegistration struct {
	Env         string             `json:"env"`
	ServiceName string             `json:"serviceName"`
	InstanceID  string             `json:"instanceId"`
	Endpoints   []SnapshotEndpoint `json:"endpoints"`
}

// FullSyncSnapshotPayload 为 FULL_SYNC_SNAPSHOT 消息负载。
type FullSyncSnapshotPayload struct {
	TunnelID      string                 `json:"tunnelId"`
	RDName        string                 `json:"rdName"`
	Registrations []SnapshotRegistration `json:"registrations"`
}

// RegisterUpsertPayload 为 REGISTER_UPSERT 消息负载。
type RegisterUpsertPayload struct {
	TunnelID    string `json:"tunnelId"`
	Env         string `json:"env"`
	ServiceName string `json:"serviceName"`
	Protocol    string `json:"protocol"`
	InstanceID  string `json:"instanceId"`
	TargetPort  int    `json:"targetPort"`
	Status      string `json:"status,omitempty"`
}

// RegisterDeletePayload 为 REGISTER_DELETE 消息负载。
type RegisterDeletePayload struct {
	TunnelID    string `json:"tunnelId"`
	Env         string `json:"env"`
	ServiceName string `json:"serviceName"`
	Protocol    string `json:"protocol"`
	InstanceID  string `json:"instanceId,omitempty"`
	TargetPort  int    `json:"targetPort,omitempty"`
}

// TunnelHeartbeatPayload 为 TUNNEL_HEARTBEAT 消息负载。
type TunnelHeartbeatPayload struct {
	TunnelID string `json:"tunnelId"`
	ConnID   string `json:"connId,omitempty"`
}
