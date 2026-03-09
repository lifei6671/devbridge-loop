package domain

import "time"

// TunnelMessageType defines sync message categories for agent <-> bridge.
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

// SyncEventMeta is the idempotency header required by tunnel messages.
type SyncEventMeta struct {
	SessionEpoch    int64     `json:"sessionEpoch"`
	ResourceVersion int64     `json:"resourceVersion"`
	EventID         string    `json:"eventId"`
	SentAt          time.Time `json:"sentAt"`
}

// TunnelMessage is the protocol-agnostic tunnel envelope.
type TunnelMessage struct {
	Type TunnelMessageType `json:"type"`
	SyncEventMeta
	Payload any `json:"payload,omitempty"`
}
