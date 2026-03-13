package control

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/lifei6671/devbridge-loop/agent-core/runtime/agent/tunnel"
)

var (
	// ErrTunnelReporterDependencyMissing 表示 reporter 关键依赖缺失。
	ErrTunnelReporterDependencyMissing = errors.New("tunnel reporter dependency missing")
)

// TunnelPoolReport 描述 Agent 侧 tunnel 池状态上报载荷。
type TunnelPoolReport struct {
	SessionID       string
	SessionEpoch    uint64
	IdleCount       int
	InUseCount      int
	TargetIdleCount int
	Trigger         string
	Timestamp       time.Time
}

// TunnelPoolSnapshotSource 提供 tunnel 池快照读取能力。
type TunnelPoolSnapshotSource interface {
	// Snapshot 返回当前 tunnel 池统计快照。
	Snapshot() tunnel.Snapshot
}

// TunnelPoolReportSender 定义 TunnelPoolReport 发送接口。
type TunnelPoolReportSender interface {
	// SendTunnelPoolReport 发送池状态上报。
	SendTunnelPoolReport(ctx context.Context, report TunnelPoolReport) error
}

// TunnelReporterConfig 定义 TunnelReporter 运行参数。
type TunnelReporterConfig struct {
	Period         time.Duration
	EventBuffer    int
	TargetIdleHint int
}

// DefaultTunnelReporterConfig 返回默认上报策略。
func DefaultTunnelReporterConfig() TunnelReporterConfig {
	return TunnelReporterConfig{
		Period:         10 * time.Second,
		EventBuffer:    16,
		TargetIdleHint: 8,
	}
}

// Normalize 回填并校验 reporter 配置。
func (config TunnelReporterConfig) Normalize() TunnelReporterConfig {
	normalizedConfig := config
	defaultConfig := DefaultTunnelReporterConfig()
	if normalizedConfig.Period <= 0 {
		// 未配置周期时按文档默认 10s 纠偏。
		normalizedConfig.Period = defaultConfig.Period
	}
	if normalizedConfig.EventBuffer <= 0 {
		// 事件队列最小给 1，避免非阻塞通知丢失过多。
		normalizedConfig.EventBuffer = defaultConfig.EventBuffer
	}
	if normalizedConfig.TargetIdleHint < 0 {
		// 目标 idle 不允许负值。
		normalizedConfig.TargetIdleHint = 0
	}
	return normalizedConfig
}

// TunnelReporter 负责 tunnel 池事件驱动上报与周期纠偏上报。
type TunnelReporter struct {
	config TunnelReporterConfig
	source TunnelPoolSnapshotSource
	sender TunnelPoolReportSender

	eventChannel chan string

	mutex        sync.RWMutex
	sessionID    string
	sessionEpoch uint64
}

// NewTunnelReporter 创建 tunnel 池状态上报器。
func NewTunnelReporter(source TunnelPoolSnapshotSource, sender TunnelPoolReportSender, config TunnelReporterConfig) (*TunnelReporter, error) {
	if source == nil {
		// 没有快照源时无法构造 report。
		return nil, fmt.Errorf("new tunnel reporter: %w: nil source", ErrTunnelReporterDependencyMissing)
	}
	if sender == nil {
		// 没有发送器时无法真正上报。
		return nil, fmt.Errorf("new tunnel reporter: %w: nil sender", ErrTunnelReporterDependencyMissing)
	}
	normalizedConfig := config.Normalize()
	return &TunnelReporter{
		config:       normalizedConfig,
		source:       source,
		sender:       sender,
		eventChannel: make(chan string, normalizedConfig.EventBuffer),
	}, nil
}

// SetSession 更新 report 附带的 session 信息。
func (reporter *TunnelReporter) SetSession(sessionID string, sessionEpoch uint64) {
	reporter.mutex.Lock()
	defer reporter.mutex.Unlock()
	// 重连后需要更新会话字段，避免污染新会话视图。
	reporter.sessionID = strings.TrimSpace(sessionID)
	reporter.sessionEpoch = sessionEpoch
}

// SetTargetIdleHint 更新上报中的目标 idle 提示值。
func (reporter *TunnelReporter) SetTargetIdleHint(targetIdle int) {
	reporter.mutex.Lock()
	defer reporter.mutex.Unlock()
	if targetIdle < 0 {
		// 负值统一归零，避免上报非法容量。
		targetIdle = 0
	}
	reporter.config.TargetIdleHint = targetIdle
}

// NotifyEvent 触发一次事件驱动上报（实现 tunnel.PoolEventNotifier）。
func (reporter *TunnelReporter) NotifyEvent(trigger string) {
	normalizedTrigger := strings.TrimSpace(trigger)
	if normalizedTrigger == "" {
		// 空 trigger 统一回落到通用事件标签。
		normalizedTrigger = "pool_event"
	}
	select {
	case reporter.eventChannel <- normalizedTrigger:
		// 成功入队后由 Run 协程异步发送。
	default:
		// 队列满时丢弃本次通知，避免阻塞业务主路径。
	}
}

// Run 启动上报循环：事件立即上报 + 周期纠偏上报。
func (reporter *TunnelReporter) Run(ctx context.Context) error {
	normalizedContext := ctx
	if normalizedContext == nil {
		// nil context 时回落到 Background。
		normalizedContext = context.Background()
	}
	ticker := time.NewTicker(reporter.config.Period)
	defer ticker.Stop()

	for {
		select {
		case <-normalizedContext.Done():
			// 上层取消时结束上报循环。
			return normalizedContext.Err()
		case <-ticker.C:
			// 周期纠偏：低频快照用于状态对账。
			if err := reporter.ReportNow(normalizedContext, "periodic"); err != nil {
				return err
			}
		case trigger := <-reporter.eventChannel:
			// 事件驱动：发生池变化时尽快上报。
			effectiveTrigger := trigger
			for {
				select {
				case pendingTrigger := <-reporter.eventChannel:
					// 连续事件合并为一次发送，保留最后一个触发原因。
					effectiveTrigger = pendingTrigger
				default:
					goto sendEvent
				}
			}
		sendEvent:
			if err := reporter.ReportNow(normalizedContext, "event:"+effectiveTrigger); err != nil {
				return err
			}
		}
	}
}

// ReportNow 立即读取快照并发送一次上报。
func (reporter *TunnelReporter) ReportNow(ctx context.Context, trigger string) error {
	normalizedContext := ctx
	if normalizedContext == nil {
		// 调用方未传 context 时使用 Background。
		normalizedContext = context.Background()
	}
	snapshot := reporter.source.Snapshot()
	report := reporter.buildReport(snapshot, trigger, time.Now().UTC())
	if err := reporter.sender.SendTunnelPoolReport(normalizedContext, report); err != nil {
		// 发送失败直接返回，交由上层决定重试策略。
		return fmt.Errorf("report tunnel pool: %w", err)
	}
	return nil
}

// buildReport 根据快照构造上报体。
func (reporter *TunnelReporter) buildReport(snapshot tunnel.Snapshot, trigger string, now time.Time) TunnelPoolReport {
	reporter.mutex.RLock()
	sessionID := reporter.sessionID
	sessionEpoch := reporter.sessionEpoch
	targetIdleHint := reporter.config.TargetIdleHint
	reporter.mutex.RUnlock()

	normalizedTrigger := strings.TrimSpace(trigger)
	if normalizedTrigger == "" {
		// 未指定 trigger 时统一按 periodic 处理。
		normalizedTrigger = "periodic"
	}
	inUseCount := snapshot.OpeningCount + snapshot.ReservedCount + snapshot.ActiveCount + snapshot.ClosingCount
	return TunnelPoolReport{
		SessionID:       sessionID,
		SessionEpoch:    sessionEpoch,
		IdleCount:       snapshot.IdleCount,
		InUseCount:      inUseCount,
		TargetIdleCount: targetIdleHint,
		Trigger:         normalizedTrigger,
		Timestamp:       now,
	}
}
