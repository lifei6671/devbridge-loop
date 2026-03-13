package tunnel

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"
)

var (
	// ErrProducerDependencyMissing 表示 Producer 关键依赖缺失。
	ErrProducerDependencyMissing = errors.New("producer dependency missing")
	// ErrInvalidProducerConfig 表示 Producer 配置不合法。
	ErrInvalidProducerConfig = errors.New("invalid producer config")
)

// TunnelOpener 定义底层 tunnel 建连能力。
type TunnelOpener interface {
	// Open 创建一条新的 tunnel。
	Open(ctx context.Context) (RuntimeTunnel, error)
}

// ProducerConfig 定义建连平滑控制参数。
type ProducerConfig struct {
	MaxInflight int
	RateLimit   float64
	Burst       int
}

// DefaultProducerConfig 返回默认建连参数。
func DefaultProducerConfig() ProducerConfig {
	return ProducerConfig{
		MaxInflight: 4,
		RateLimit:   10,
		Burst:       20,
	}
}

// Normalize 校验并回填 Producer 配置。
func (config ProducerConfig) Normalize() (ProducerConfig, error) {
	normalizedConfig := config
	if normalizedConfig.MaxInflight <= 0 {
		// 未配置并发上限时回落到保守默认值。
		normalizedConfig.MaxInflight = DefaultProducerConfig().MaxInflight
	}
	if normalizedConfig.RateLimit <= 0 {
		// 建连速率必须为正数。
		return ProducerConfig{}, fmt.Errorf("normalize producer config: %w: rate_limit=%v", ErrInvalidProducerConfig, normalizedConfig.RateLimit)
	}
	if normalizedConfig.Burst <= 0 {
		// 突发窗口必须为正数，避免 limiter 永远不可用。
		return ProducerConfig{}, fmt.Errorf("normalize producer config: %w: burst=%d", ErrInvalidProducerConfig, normalizedConfig.Burst)
	}
	return normalizedConfig, nil
}

// ProduceResult 描述一次批量建连结果。
type ProduceResult struct {
	Requested  int
	Opened     int
	Failed     int
	FirstError error
}

// Producer 负责按并发与速率约束平滑建连。
type Producer struct {
	opener TunnelOpener
	config ProducerConfig

	inflightSemaphore chan struct{}
	rateLimiter       *tokenBucket
}

// NewProducer 创建 tunnel 建连器。
func NewProducer(opener TunnelOpener, config ProducerConfig) (*Producer, error) {
	if opener == nil {
		// opener 是建连核心依赖，不能为空。
		return nil, fmt.Errorf("new producer: %w", ErrProducerDependencyMissing)
	}
	normalizedConfig, err := config.Normalize()
	if err != nil {
		return nil, err
	}
	producer := &Producer{
		opener:            opener,
		config:            normalizedConfig,
		inflightSemaphore: make(chan struct{}, normalizedConfig.MaxInflight),
		rateLimiter:       newTokenBucket(normalizedConfig.RateLimit, normalizedConfig.Burst),
	}
	return producer, nil
}

// Config 返回 Producer 归一化后的配置快照。
func (producer *Producer) Config() ProducerConfig {
	return producer.config
}

// Open 在约束下打开一条 tunnel。
func (producer *Producer) Open(ctx context.Context) (RuntimeTunnel, error) {
	normalizedContext := ctx
	if normalizedContext == nil {
		// 调用方传 nil 时回落到 Background，避免 panic。
		normalizedContext = context.Background()
	}
	if err := producer.acquireInflightSlot(normalizedContext); err != nil {
		return nil, err
	}
	defer producer.releaseInflightSlot()

	if err := producer.rateLimiter.Wait(normalizedContext); err != nil {
		// 被取消时直接返回，避免继续消耗建连资源。
		return nil, err
	}
	tunnel, err := producer.opener.Open(normalizedContext)
	if err != nil {
		// 建连失败由上层决定是否重试。
		return nil, err
	}
	return tunnel, nil
}

// OpenBatch 批量打开 tunnel，并把成功结果交给 handler 入池。
func (producer *Producer) OpenBatch(ctx context.Context, count int, handler func(RuntimeTunnel) error) ProduceResult {
	result := ProduceResult{Requested: count}
	if count <= 0 {
		// 请求数量非正数时无需执行。
		return result
	}
	normalizedContext := ctx
	if normalizedContext == nil {
		// 批量操作同样兜底 context。
		normalizedContext = context.Background()
	}

	jobChannel := make(chan struct{}, count)
	for index := 0; index < count; index++ {
		// 预填充任务，worker 固定读取即可。
		jobChannel <- struct{}{}
	}
	close(jobChannel)

	workerCount := count
	if workerCount > producer.config.MaxInflight {
		// worker 数量受 max_inflight 上限约束。
		workerCount = producer.config.MaxInflight
	}

	var resultMutex sync.Mutex
	recordFailure := func(err error) {
		resultMutex.Lock()
		defer resultMutex.Unlock()
		// 每次失败都累计计数，并保留首个错误用于告警。
		result.Failed++
		if result.FirstError == nil {
			result.FirstError = err
		}
	}
	recordOpened := func() {
		resultMutex.Lock()
		defer resultMutex.Unlock()
		// 成功入池后累计 opened 数。
		result.Opened++
	}

	var workerGroup sync.WaitGroup
	for workerIndex := 0; workerIndex < workerCount; workerIndex++ {
		workerGroup.Add(1)
		go func() {
			defer workerGroup.Done()
			for range jobChannel {
				tunnel, err := producer.Open(normalizedContext)
				if err != nil {
					recordFailure(err)
					continue
				}
				if handler != nil {
					if handleErr := handler(tunnel); handleErr != nil {
						// 入池失败时主动关闭，避免资源泄漏。
						cleanupErr := tunnel.Close()
						if cleanupErr != nil {
							recordFailure(errors.Join(handleErr, cleanupErr))
						} else {
							recordFailure(handleErr)
						}
						continue
					}
				}
				recordOpened()
			}
		}()
	}
	workerGroup.Wait()
	return result
}

// acquireInflightSlot 申请并发建连配额。
func (producer *Producer) acquireInflightSlot(ctx context.Context) error {
	select {
	case <-ctx.Done():
		// 上下文取消时不再等待配额。
		return ctx.Err()
	case producer.inflightSemaphore <- struct{}{}:
		return nil
	}
}

// releaseInflightSlot 释放并发建连配额。
func (producer *Producer) releaseInflightSlot() {
	select {
	case <-producer.inflightSemaphore:
		// 正常释放。
	default:
		// 理论上不应发生，防止误释放导致阻塞。
	}
}

// tokenBucket 是一个轻量级令牌桶实现，用于平滑建连速率。
type tokenBucket struct {
	mutex sync.Mutex

	rate       float64
	burst      float64
	tokens     float64
	lastRefill time.Time
}

// newTokenBucket 创建令牌桶，并用满桶支持冷启动突发。
func newTokenBucket(rateLimit float64, burst int) *tokenBucket {
	initialBurst := float64(burst)
	return &tokenBucket{
		rate:       rateLimit,
		burst:      initialBurst,
		tokens:     initialBurst,
		lastRefill: time.Now(),
	}
}

// Wait 等待直到可以消费一个令牌或上下文取消。
func (bucket *tokenBucket) Wait(ctx context.Context) error {
	normalizedContext := ctx
	if normalizedContext == nil {
		// 兜底 context，避免 nil 导致 select panic。
		normalizedContext = context.Background()
	}
	for {
		waitDuration := bucket.consumeOrDelay(time.Now())
		if waitDuration <= 0 {
			// 已成功消费令牌，可继续建连。
			return nil
		}
		timer := time.NewTimer(waitDuration)
		select {
		case <-normalizedContext.Done():
			// 调用方取消时停止等待。
			if !timer.Stop() {
				<-timer.C
			}
			return normalizedContext.Err()
		case <-timer.C:
			// 到达下一次尝试时间，继续循环取令牌。
		}
	}
}

// consumeOrDelay 尝试消费一个令牌，返回需要等待的时长。
func (bucket *tokenBucket) consumeOrDelay(now time.Time) time.Duration {
	bucket.mutex.Lock()
	defer bucket.mutex.Unlock()

	elapsedSeconds := now.Sub(bucket.lastRefill).Seconds()
	if elapsedSeconds > 0 {
		// 按时间增量补充令牌，并限制在 burst 上限内。
		bucket.tokens = math.Min(bucket.burst, bucket.tokens+elapsedSeconds*bucket.rate)
		bucket.lastRefill = now
	}
	if bucket.tokens >= 1 {
		// 令牌充足时直接扣减。
		bucket.tokens -= 1
		return 0
	}
	missingTokens := 1 - bucket.tokens
	waitSeconds := missingTokens / bucket.rate
	if waitSeconds <= 0 {
		return 0
	}
	// 向上取整为纳秒，避免短时间抖动导致忙等。
	return time.Duration(math.Ceil(waitSeconds * float64(time.Second)))
}
