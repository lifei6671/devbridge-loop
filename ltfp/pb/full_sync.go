package pb

// SyncMessageType 定义同步消息的类别。
type SyncMessageType string

const (
	// SyncMessageTypeFullSyncRequest 表示请求服务端发送全量快照。
	SyncMessageTypeFullSyncRequest SyncMessageType = "FULL_SYNC_REQUEST"
	// SyncMessageTypeFullSyncSnapshot 表示返回全量快照数据。
	SyncMessageTypeFullSyncSnapshot SyncMessageType = "FULL_SYNC_SNAPSHOT"
	// SyncMessageTypeDeltaEvent 表示发送增量事件。
	SyncMessageTypeDeltaEvent SyncMessageType = "DELTA_EVENT"
)

// FullSyncRequest 描述全量同步请求参数。
type FullSyncRequest struct {
	// requestId 用于关联本次 full-sync 请求与响应。
	RequestID string `json:"requestId"`
	// connectorId 标识请求方 connector。
	ConnectorID string `json:"connectorId"`
	// sessionId 标识请求方当前会话。
	SessionID string `json:"sessionId"`
	// sessionEpoch 用于校验是否属于当前有效会话。
	SessionEpoch uint64 `json:"sessionEpoch"`
	// sinceResourceVersion 表示增量追赶起点，0 表示请求完整快照。
	SinceResourceVersion uint64 `json:"sinceResourceVersion,omitempty"`
}

// FullSyncSnapshot 描述全量快照载荷。
type FullSyncSnapshot struct {
	// requestId 用于与请求关联，便于端到端追踪。
	RequestID string `json:"requestId"`
	// sessionEpoch 表示生成快照时使用的会话代际。
	SessionEpoch uint64 `json:"sessionEpoch"`
	// snapshotVersion 表示快照版本，用于后续增量追赶边界。
	SnapshotVersion uint64 `json:"snapshotVersion"`
	// services 为当前有效服务快照集合。
	Services []Service `json:"services,omitempty"`
	// routes 为当前有效路由快照集合。
	Routes []Route `json:"routes,omitempty"`
	// completed=true 表示该快照窗口已结束。
	Completed bool `json:"completed"`
}

// DeltaEvent 描述增量同步事件封装。
type DeltaEvent struct {
	// messageType 表示资源事件类型，例如 PublishService/UnpublishService。
	MessageType ControlMessageType `json:"messageType"`
	// sessionId 标识事件归属会话。
	SessionID string `json:"sessionId"`
	// sessionEpoch 用于防止旧会话事件污染新会话。
	SessionEpoch uint64 `json:"sessionEpoch"`
	// eventId 用于幂等去重。
	EventID string `json:"eventId"`
	// resourceVersion 表示资源代际版本。
	ResourceVersion uint64 `json:"resourceVersion"`
	// payload 承载资源消息体。
	Payload any `json:"payload,omitempty"`
}
