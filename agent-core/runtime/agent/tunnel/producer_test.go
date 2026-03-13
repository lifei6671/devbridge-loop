package tunnel

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// producerTestTunnel 是测试用 tunnel 实现。
type producerTestTunnel struct {
	id         string
	closeCount atomic.Int32
}

// ID 返回测试 tunnel ID。
func (tunnel *producerTestTunnel) ID() string {
	return tunnel.id
}

// Close 记录关闭次数。
func (tunnel *producerTestTunnel) Close() error {
	// 累计关闭次数，便于断言 cleanup 行为。
	tunnel.closeCount.Add(1)
	return nil
}

// producerTestOpener 模拟可观测并发的建连器。
type producerTestOpener struct {
	openDelay time.Duration

	mutex     sync.Mutex
	sequence  int
	active    int
	maxActive int
	tunnels   []*producerTestTunnel
}

// Open 创建测试 tunnel，并统计并发峰值。
func (opener *producerTestOpener) Open(ctx context.Context) (RuntimeTunnel, error) {
	opener.mutex.Lock()
	opener.active++
	if opener.active > opener.maxActive {
		// 记录建连并发峰值，验证 max_inflight 是否生效。
		opener.maxActive = opener.active
	}
	opener.sequence++
	tunnel := &producerTestTunnel{id: fmt.Sprintf("tunnel-%d", opener.sequence)}
	opener.tunnels = append(opener.tunnels, tunnel)
	opener.mutex.Unlock()

	if opener.openDelay > 0 {
		select {
		case <-ctx.Done():
			opener.mutex.Lock()
			opener.active--
			opener.mutex.Unlock()
			return nil, ctx.Err()
		case <-time.After(opener.openDelay):
		}
	}

	opener.mutex.Lock()
	opener.active--
	opener.mutex.Unlock()
	return tunnel, nil
}

// MaxActive 返回观测到的并发峰值。
func (opener *producerTestOpener) MaxActive() int {
	opener.mutex.Lock()
	defer opener.mutex.Unlock()
	return opener.maxActive
}

// CreatedTunnels 返回创建出的 tunnel 列表副本。
func (opener *producerTestOpener) CreatedTunnels() []*producerTestTunnel {
	opener.mutex.Lock()
	defer opener.mutex.Unlock()
	copied := make([]*producerTestTunnel, 0, len(opener.tunnels))
	copied = append(copied, opener.tunnels...)
	return copied
}

// TestProducerOpenBatchRespectsMaxInflight 验证批量建连受 max_inflight 约束。
func TestProducerOpenBatchRespectsMaxInflight(testingObject *testing.T) {
	testingObject.Parallel()
	opener := &producerTestOpener{openDelay: 20 * time.Millisecond}
	producer, err := NewProducer(opener, ProducerConfig{
		MaxInflight: 2,
		RateLimit:   1000,
		Burst:       1000,
	})
	if err != nil {
		testingObject.Fatalf("new producer failed: %v", err)
	}

	result := producer.OpenBatch(context.Background(), 8, nil)
	if result.Failed != 0 {
		testingObject.Fatalf("unexpected failed count: %d first_error=%v", result.Failed, result.FirstError)
	}
	if result.Opened != 8 {
		testingObject.Fatalf("unexpected opened count: %d", result.Opened)
	}
	if opener.MaxActive() > 2 {
		testingObject.Fatalf("max active exceeded: %d", opener.MaxActive())
	}
}

// TestProducerOpenBatchCleanupOnHandlerError 验证入池失败时会关闭已创建 tunnel。
func TestProducerOpenBatchCleanupOnHandlerError(testingObject *testing.T) {
	testingObject.Parallel()
	opener := &producerTestOpener{}
	producer, err := NewProducer(opener, ProducerConfig{
		MaxInflight: 1,
		RateLimit:   100,
		Burst:       1,
	})
	if err != nil {
		testingObject.Fatalf("new producer failed: %v", err)
	}

	handlerError := errors.New("put idle failed")
	result := producer.OpenBatch(context.Background(), 1, func(RuntimeTunnel) error {
		// 模拟 registry 入池失败。
		return handlerError
	})
	if result.Opened != 0 {
		testingObject.Fatalf("unexpected opened count: %d", result.Opened)
	}
	if result.Failed != 1 {
		testingObject.Fatalf("unexpected failed count: %d", result.Failed)
	}
	if result.FirstError == nil {
		testingObject.Fatalf("expected first error")
	}
	createdTunnels := opener.CreatedTunnels()
	if len(createdTunnels) != 1 {
		testingObject.Fatalf("unexpected tunnel count: %d", len(createdTunnels))
	}
	if createdTunnels[0].closeCount.Load() != 1 {
		testingObject.Fatalf("expected cleanup close once, got %d", createdTunnels[0].closeCount.Load())
	}
}
