package domain

import "time"

// TunnelSession describes one active tunnel between bridge and agent.
type TunnelSession struct {
	TunnelID        string    `json:"tunnelId"`
	RDName          string    `json:"rdName"`
	ConnID          string    `json:"connId"`
	SessionEpoch    int64     `json:"sessionEpoch"`
	Status          string    `json:"status"`
	LastHeartbeatAt time.Time `json:"lastHeartbeatAt"`
	ConnectedAt     time.Time `json:"connectedAt"`
}

// ActiveIntercept stores effective intercept routes.
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

// BridgeRoute is the finalized ingress route published by bridge.
type BridgeRoute struct {
	Env         string `json:"env"`
	ServiceName string `json:"serviceName"`
	Protocol    string `json:"protocol"`
	BridgeHost  string `json:"bridgeHost"`
	BridgePort  int    `json:"bridgePort"`
	TunnelID    string `json:"tunnelId"`
	TargetPort  int    `json:"targetPort"`
}

// TunnelEvent is a normalized payload for future sync processing.
type TunnelEvent struct {
	Type            string         `json:"type"`
	SessionEpoch    int64          `json:"sessionEpoch"`
	ResourceVersion int64          `json:"resourceVersion"`
	EventID         string         `json:"eventId"`
	SentAt          time.Time      `json:"sentAt"`
	Payload         map[string]any `json:"payload"`
}
