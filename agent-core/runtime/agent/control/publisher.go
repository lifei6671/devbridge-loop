package control

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// Publisher 负责构造并发送控制面资源消息。
type Publisher struct {
	mu              sync.Mutex
	sessionID       string
	sessionEpoch    uint64
	resourceVersion uint64
	eventSequence   uint64
}

// PublisherState 描述发布器当前元信息状态。
type PublisherState struct {
	SessionID       string
	SessionEpoch    uint64
	ResourceVersion uint64
	EventSequence   uint64
}

// NewPublisher 创建资源消息发布器。
func NewPublisher(sessionID string, sessionEpoch uint64, resourceVersion uint64) *Publisher {
	return &Publisher{
		sessionID:       sessionID,
		sessionEpoch:    sessionEpoch,
		resourceVersion: resourceVersion,
	}
}

// SetSession 更新发布器会话上下文。
func (publisher *Publisher) SetSession(sessionID string, sessionEpoch uint64) {
	publisher.mu.Lock()
	defer publisher.mu.Unlock()
	// 重连后更新会话代际，后续消息将自动携带新代际字段。
	publisher.sessionID = sessionID
	publisher.sessionEpoch = sessionEpoch
}

// Publish 构造并返回控制面 envelope（骨架阶段由调用方实际发送）。
func (publisher *Publisher) Publish(ctx context.Context, messageType pb.ControlMessageType, resourceType string, resourceID string, payload any) (pb.ControlEnvelope, error) {
	_ = ctx // skeleton: 发送链路尚未接入，先保留上下文参数

	rawPayload, err := json.Marshal(payload)
	if err != nil {
		// payload 编码失败时返回错误，避免发送非法消息。
		return pb.ControlEnvelope{}, fmt.Errorf("marshal control payload: %w", err)
	}

	publisher.mu.Lock()
	defer publisher.mu.Unlock()
	// 资源版本与事件序号在本地单调递增，便于幂等与回放控制。
	publisher.resourceVersion++
	publisher.eventSequence++
	eventID := fmt.Sprintf("evt-%d-%d", time.Now().UnixNano(), publisher.eventSequence)

	envelope := pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     messageType,
		SessionID:       publisher.sessionID,
		SessionEpoch:    publisher.sessionEpoch,
		ResourceType:    resourceType,
		ResourceID:      resourceID,
		EventID:         eventID,
		ResourceVersion: publisher.resourceVersion,
		Payload:         rawPayload,
	}
	return envelope, nil
}

// State 返回发布器当前状态快照。
func (publisher *Publisher) State() PublisherState {
	publisher.mu.Lock()
	defer publisher.mu.Unlock()
	// 返回只读副本，避免外部并发修改。
	return PublisherState{
		SessionID:       publisher.sessionID,
		SessionEpoch:    publisher.sessionEpoch,
		ResourceVersion: publisher.resourceVersion,
		EventSequence:   publisher.eventSequence,
	}
}
