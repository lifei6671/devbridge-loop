package transport

import (
	"fmt"
	"sync"
	"time"
)

const (
	defaultHeartbeatSendInterval  = 5 * time.Second
	defaultHeartbeatMissThreshold = 3
)

// HeartbeatPolicy 描述控制面 heartbeat 的发送与判死策略。
type HeartbeatPolicy struct {
	SendInterval    time.Duration
	MissThreshold   int
	QueueDelayGrace time.Duration
}

// NormalizeAndValidate 归一化并校验 heartbeat 策略。
func (policy HeartbeatPolicy) NormalizeAndValidate() (HeartbeatPolicy, error) {
	normalizedPolicy := policy
	if normalizedPolicy.SendInterval <= 0 {
		// 未配置发送周期时采用保守默认值。
		normalizedPolicy.SendInterval = defaultHeartbeatSendInterval
	}
	if normalizedPolicy.MissThreshold <= 0 {
		// 丢失阈值必须至少为 1，默认使用 3 次连续丢失。
		normalizedPolicy.MissThreshold = defaultHeartbeatMissThreshold
	}
	if normalizedPolicy.QueueDelayGrace < 0 {
		// grace 不能为负，否则会把排队抖动错误折算成超时。
		return HeartbeatPolicy{}, fmt.Errorf(
			"normalize heartbeat policy: %w: queue_delay_grace=%s",
			ErrInvalidArgument,
			normalizedPolicy.QueueDelayGrace,
		)
	}
	if normalizedPolicy.QueueDelayGrace == 0 {
		// 未显式指定时使用一个发送周期作为抖动缓冲。
		normalizedPolicy.QueueDelayGrace = normalizedPolicy.SendInterval
	}
	return normalizedPolicy, nil
}

// DefaultHeartbeatPolicy 返回默认 heartbeat 策略。
func DefaultHeartbeatPolicy() HeartbeatPolicy {
	return HeartbeatPolicy{
		SendInterval:    defaultHeartbeatSendInterval,
		MissThreshold:   defaultHeartbeatMissThreshold,
		QueueDelayGrace: defaultHeartbeatSendInterval,
	}
}

// FailureTimeout 返回从最近一次收到对端存活信号到判死的总超时窗口。
func (policy HeartbeatPolicy) FailureTimeout() time.Duration {
	normalizedPolicy, err := policy.NormalizeAndValidate()
	if err != nil {
		// 调用方若传入非法策略，返回零值让上层在校验阶段处理。
		return 0
	}
	return normalizedPolicy.SendInterval*time.Duration(normalizedPolicy.MissThreshold) + normalizedPolicy.QueueDelayGrace
}

// HeartbeatStatus 描述某一时刻 heartbeat 的运行快照。
type HeartbeatStatus struct {
	StartedAt         time.Time
	LastSentAt        time.Time
	LastReceivedAt    time.Time
	NextSendAt        time.Time
	FailureDeadline   time.Time
	ConsecutiveMisses int
	Dead              bool
}

// HeartbeatMonitor 负责跟踪控制面 heartbeat 的发送与判死状态。
type HeartbeatMonitor struct {
	policy HeartbeatPolicy

	mutex          sync.Mutex
	startedAt      time.Time
	lastSentAt     time.Time
	lastReceivedAt time.Time
}

// NewHeartbeatMonitor 创建 heartbeat 状态跟踪器。
func NewHeartbeatMonitor(policy HeartbeatPolicy) (*HeartbeatMonitor, error) {
	normalizedPolicy, err := policy.NormalizeAndValidate()
	if err != nil {
		return nil, err
	}
	return &HeartbeatMonitor{
		policy: normalizedPolicy,
	}, nil
}

// Start 标记控制面 heartbeat 从指定时间点开始生效。
func (monitor *HeartbeatMonitor) Start(now time.Time) {
	monitor.mutate(now, func(normalizedNow time.Time) {
		if monitor.startedAt.IsZero() {
			// 控制面 ready 后只初始化一次起点，避免中途重置统计窗口。
			monitor.startedAt = normalizedNow
		}
	})
}

// ObserveSend 记录一条 heartbeat 已经真正发送完成。
func (monitor *HeartbeatMonitor) ObserveSend(now time.Time) {
	monitor.mutate(now, func(normalizedNow time.Time) {
		if monitor.startedAt.IsZero() {
			// 尚未显式 Start 时，首次发送视为控制面已进入 heartbeat 周期。
			monitor.startedAt = normalizedNow
		}
		monitor.lastSentAt = normalizedNow
	})
}

// ObserveReceive 记录一条来自对端的 heartbeat 或等价存活信号。
func (monitor *HeartbeatMonitor) ObserveReceive(now time.Time) {
	monitor.mutate(now, func(normalizedNow time.Time) {
		if monitor.startedAt.IsZero() {
			// 若首次事件是接收方向，也把该时间点视为 heartbeat 起点。
			monitor.startedAt = normalizedNow
		}
		monitor.lastReceivedAt = normalizedNow
	})
}

// ObservePeerActivity 是 ObserveReceive 的语义化别名，便于 control loop 在收到任意对端控制消息时复用。
func (monitor *HeartbeatMonitor) ObservePeerActivity(now time.Time) {
	monitor.ObserveReceive(now)
}

// ShouldSend 判断当前时间点是否已到下一次 heartbeat 发送时机。
func (monitor *HeartbeatMonitor) ShouldSend(now time.Time) bool {
	if now.IsZero() {
		// 与 Snapshot 一致：零时间表示“使用当前时间判定”。
		now = time.Now().UTC()
	}
	status := monitor.Snapshot(now)
	return !status.StartedAt.IsZero() && !now.Before(status.NextSendAt)
}

// Snapshot 返回指定时间点的 heartbeat 运行快照。
func (monitor *HeartbeatMonitor) Snapshot(now time.Time) HeartbeatStatus {
	if monitor == nil {
		return HeartbeatStatus{}
	}
	if now.IsZero() {
		// 外部未传时间时统一使用当前 UTC，保持判死口径一致。
		now = time.Now().UTC()
	}

	monitor.mutex.Lock()
	defer monitor.mutex.Unlock()
	if monitor.startedAt.IsZero() {
		// 尚未启动 heartbeat 时返回零值快照。
		return HeartbeatStatus{}
	}

	referenceReceiveAt := monitor.startedAt
	if !monitor.lastReceivedAt.IsZero() {
		// 判死基准优先使用最近一次收到对端存活信号的时间。
		referenceReceiveAt = monitor.lastReceivedAt
	}
	nextSendBase := monitor.startedAt
	if !monitor.lastSentAt.IsZero() {
		// 下一次发送时机基于最近一次真实发送完成时间。
		nextSendBase = monitor.lastSentAt
	}

	status := HeartbeatStatus{
		StartedAt:       monitor.startedAt,
		LastSentAt:      monitor.lastSentAt,
		LastReceivedAt:  monitor.lastReceivedAt,
		NextSendAt:      nextSendBase.Add(monitor.policy.SendInterval),
		FailureDeadline: referenceReceiveAt.Add(monitor.policy.FailureTimeout()),
	}
	firstMissAt := referenceReceiveAt.Add(monitor.policy.SendInterval + monitor.policy.QueueDelayGrace)
	if !now.Before(firstMissAt) {
		// 超过首个 miss 窗口后，每跨过一个发送周期累计一次连续丢失。
		status.ConsecutiveMisses = 1 + int(now.Sub(firstMissAt)/monitor.policy.SendInterval)
	}
	status.Dead = status.ConsecutiveMisses >= monitor.policy.MissThreshold
	return status
}

// mutate 统一处理时间归一化与互斥更新逻辑。
func (monitor *HeartbeatMonitor) mutate(now time.Time, update func(time.Time)) {
	if monitor == nil {
		return
	}
	if now.IsZero() {
		// 所有状态写入统一使用 UTC 时间，避免混入本地时区。
		now = time.Now().UTC()
	}
	monitor.mutex.Lock()
	defer monitor.mutex.Unlock()
	update(now)
}
