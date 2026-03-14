package app

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/agent-core/runtime/agent/traffic"
	"github.com/lifei6671/devbridge-loop/agent-core/runtime/agent/tunnel"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

type runtimeTrafficTestReadResult struct {
	payload pb.StreamPayload
	err     error
}

// runtimeTrafficTestTunnel 是 app data-plane 测试使用的 tunnel 假实现。
type runtimeTrafficTestTunnel struct {
	id string

	readQueue  chan runtimeTrafficTestReadResult
	writeMutex sync.Mutex
	writes     []pb.StreamPayload

	closeMutex sync.Mutex
	closeCount int
}

// newRuntimeTrafficTestTunnel 创建带读写缓冲的测试 tunnel。
func newRuntimeTrafficTestTunnel(tunnelID string) *runtimeTrafficTestTunnel {
	return &runtimeTrafficTestTunnel{
		id:        tunnelID,
		readQueue: make(chan runtimeTrafficTestReadResult, 8),
	}
}

// ID 返回 tunnel 标识。
func (tunnel *runtimeTrafficTestTunnel) ID() string {
	return tunnel.id
}

// ReadPayload 从预置队列读取 payload，模拟 Agent 侧接流量输入。
func (tunnel *runtimeTrafficTestTunnel) ReadPayload(ctx context.Context) (pb.StreamPayload, error) {
	select {
	case <-ctx.Done():
		return pb.StreamPayload{}, ctx.Err()
	case result := <-tunnel.readQueue:
		return result.payload, result.err
	}
}

// WritePayload 记录 Agent 写回 Bridge 的 payload。
func (tunnel *runtimeTrafficTestTunnel) WritePayload(ctx context.Context, payload pb.StreamPayload) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	tunnel.writeMutex.Lock()
	defer tunnel.writeMutex.Unlock()
	tunnel.writes = append(tunnel.writes, payload)
	return nil
}

// Close 记录关闭次数，便于断言回收路径。
func (tunnel *runtimeTrafficTestTunnel) Close() error {
	tunnel.closeMutex.Lock()
	defer tunnel.closeMutex.Unlock()
	tunnel.closeCount++
	return nil
}

// EnqueueReadPayload 预置下一次 ReadPayload 结果。
func (tunnel *runtimeTrafficTestTunnel) EnqueueReadPayload(payload pb.StreamPayload) {
	tunnel.readQueue <- runtimeTrafficTestReadResult{payload: payload}
}

// Writes 返回写入 payload 的副本。
func (tunnel *runtimeTrafficTestTunnel) Writes() []pb.StreamPayload {
	tunnel.writeMutex.Lock()
	defer tunnel.writeMutex.Unlock()
	copied := make([]pb.StreamPayload, 0, len(tunnel.writes))
	copied = append(copied, tunnel.writes...)
	return copied
}

// CloseCount 返回 tunnel 被关闭次数。
func (tunnel *runtimeTrafficTestTunnel) CloseCount() int {
	tunnel.closeMutex.Lock()
	defer tunnel.closeMutex.Unlock()
	return tunnel.closeCount
}

// runtimeTrafficTestManagerOpener 仅用于满足 NewManager 构造依赖，测试中不会触发真实建连。
type runtimeTrafficTestManagerOpener struct{}

func (opener *runtimeTrafficTestManagerOpener) Open(ctx context.Context) (tunnel.RuntimeTunnel, error) {
	_ = ctx
	return nil, errors.New("test manager opener should not be called")
}

// runtimeTrafficTestSelector 固定返回一个 endpoint，避免依赖 ServiceCatalog。
type runtimeTrafficTestSelector struct{}

func (selector *runtimeTrafficTestSelector) SelectEndpoint(
	ctx context.Context,
	serviceID string,
	hint map[string]string,
) (traffic.Endpoint, error) {
	_ = selector
	_ = ctx
	_ = serviceID
	_ = hint
	return traffic.Endpoint{
		ID:   "endpoint-1",
		Addr: "127.0.0.1:18080",
	}, nil
}

// runtimeTrafficTestUpstream 模拟 opener 中被 dial 出来的 upstream 连接。
type runtimeTrafficTestUpstream struct{}

// Read 返回 EOF，模拟无上行数据。
func (upstream *runtimeTrafficTestUpstream) Read(payload []byte) (int, error) {
	_ = upstream
	_ = payload
	return 0, io.EOF
}

// Write 直接吞掉写入字节，模拟上游可写。
func (upstream *runtimeTrafficTestUpstream) Write(payload []byte) (int, error) {
	_ = upstream
	return len(payload), nil
}

// Close 关闭测试 upstream。
func (upstream *runtimeTrafficTestUpstream) Close() error {
	_ = upstream
	return nil
}

// runtimeTrafficTestDialer 返回固定 upstream，避免真实网络依赖。
type runtimeTrafficTestDialer struct{}

func (dialer *runtimeTrafficTestDialer) Dial(
	ctx context.Context,
	endpoint traffic.Endpoint,
) (io.ReadWriteCloser, error) {
	_ = dialer
	_ = ctx
	_ = endpoint
	return &runtimeTrafficTestUpstream{}, nil
}

// newRuntimeWithTrafficWorkerDependenciesForTest 构造可直接执行 worker 的 runtime。
func newRuntimeWithTrafficWorkerDependenciesForTest(testingObject *testing.T) (*Runtime, *tunnel.Registry) {
	testingObject.Helper()
	registry := tunnel.NewRegistry()
	manager, err := tunnel.NewManager(tunnel.ManagerOptions{
		Config: tunnel.ManagerConfig{
			MinIdle:           1,
			MaxIdle:           2,
			IdleTTL:           0,
			MaxInflightOpens:  1,
			TunnelOpenRate:    10,
			TunnelOpenBurst:   10,
			ReconcileInterval: time.Second,
		},
		Registry: registry,
		Opener:   &runtimeTrafficTestManagerOpener{},
	})
	if err != nil {
		testingObject.Fatalf("new manager failed: %v", err)
	}
	opener, err := traffic.NewOpener(traffic.OpenerOptions{
		Selector: &runtimeTrafficTestSelector{},
		Dialer:   &runtimeTrafficTestDialer{},
		Relay: traffic.RelayFunc(func(
			ctx context.Context,
			tunnelIO traffic.TunnelIO,
			upstream io.ReadWriteCloser,
			trafficID string,
		) error {
			_ = ctx
			_ = tunnelIO
			_ = upstream
			_ = trafficID
			return nil
		}),
	})
	if err != nil {
		testingObject.Fatalf("new opener failed: %v", err)
	}
	runtime := &Runtime{
		tunnelRegistry:       registry,
		tunnelManager:        manager,
		trafficAcceptor:      traffic.NewAcceptor(),
		trafficOpener:        opener,
		trafficWakeupChannel: make(chan struct{}, 1),
		trafficWorkers:       make(map[string]struct{}),
	}
	return runtime, registry
}

// TestRunTrafficAcceptorWorkerConsumesOpenAndClosesTunnel 验证 worker 能消费 open 并回写 ack/close。
func TestRunTrafficAcceptorWorkerConsumesOpenAndClosesTunnel(testingObject *testing.T) {
	testingObject.Parallel()
	runtime, registry := newRuntimeWithTrafficWorkerDependenciesForTest(testingObject)
	testTunnel := newRuntimeTrafficTestTunnel("tunnel-1")
	added, addErr := registry.TryAddOpenedAsIdle(time.Now().UTC(), testTunnel, 2)
	if addErr != nil {
		testingObject.Fatalf("add idle tunnel failed: %v", addErr)
	}
	if !added {
		testingObject.Fatalf("expected idle tunnel added")
	}
	testTunnel.EnqueueReadPayload(pb.StreamPayload{
		OpenReq: &pb.TrafficOpen{
			TrafficID: "traffic-1",
			ServiceID: "svc-1",
		},
	})

	runtime.runTrafficAcceptorWorker(context.Background(), testTunnel.ID(), testTunnel)

	writes := testTunnel.Writes()
	if len(writes) != 2 {
		testingObject.Fatalf("expected two payload writes(open_ack+close), got=%d", len(writes))
	}
	if writes[0].OpenAck == nil || !writes[0].OpenAck.Success {
		testingObject.Fatalf("expected first payload to be open_ack success")
	}
	if writes[0].OpenAck.TrafficID != "traffic-1" {
		testingObject.Fatalf("unexpected open_ack traffic id: %s", writes[0].OpenAck.TrafficID)
	}
	if writes[1].Close == nil || writes[1].Close.TrafficID != "traffic-1" {
		testingObject.Fatalf("expected second payload to be close for traffic-1")
	}
	if testTunnel.CloseCount() != 1 {
		testingObject.Fatalf("expected tunnel close once, got=%d", testTunnel.CloseCount())
	}
	if _, exists := registry.Get(testTunnel.ID()); exists {
		testingObject.Fatalf("expected tunnel record removed after traffic completion")
	}
}

// TestRunTrafficAcceptorWorkerRejectsNonOpenFirstFrame 验证首帧非 open 时 worker 会回收 tunnel。
func TestRunTrafficAcceptorWorkerRejectsNonOpenFirstFrame(testingObject *testing.T) {
	testingObject.Parallel()
	runtime, registry := newRuntimeWithTrafficWorkerDependenciesForTest(testingObject)
	testTunnel := newRuntimeTrafficTestTunnel("tunnel-2")
	added, addErr := registry.TryAddOpenedAsIdle(time.Now().UTC(), testTunnel, 2)
	if addErr != nil {
		testingObject.Fatalf("add idle tunnel failed: %v", addErr)
	}
	if !added {
		testingObject.Fatalf("expected idle tunnel added")
	}
	testTunnel.EnqueueReadPayload(pb.StreamPayload{
		Data: []byte("invalid-first-frame"),
	})

	runtime.runTrafficAcceptorWorker(context.Background(), testTunnel.ID(), testTunnel)

	if len(testTunnel.Writes()) != 0 {
		testingObject.Fatalf("expected no payload writes for invalid first frame")
	}
	if testTunnel.CloseCount() != 1 {
		testingObject.Fatalf("expected tunnel close once, got=%d", testTunnel.CloseCount())
	}
	if _, exists := registry.Get(testTunnel.ID()); exists {
		testingObject.Fatalf("expected invalid tunnel removed from registry")
	}
}

// TestTunnelAssociationLifecycle 验证 tunnel 关联信息的写入、覆盖与清理。
func TestTunnelAssociationLifecycle(testingObject *testing.T) {
	testingObject.Parallel()
	runtime := &Runtime{
		tunnelAssociations: make(map[string]tunnelAssociation),
	}
	firstUpdatedAt := time.Unix(1700002000, 0).UTC()
	runtime.upsertTunnelAssociation(tunnelAssociation{
		TunnelID:  "tunnel-1",
		TrafficID: "traffic-1",
		ServiceID: "svc-1",
		UpdatedAt: firstUpdatedAt,
	})
	// 第二次只补充地址与延迟，原有 traffic/service 信息应保留。
	runtime.upsertTunnelAssociation(tunnelAssociation{
		TunnelID:              "tunnel-1",
		LocalAddr:             "127.0.0.1:18080",
		OpenAckLatencyMS:      18,
		UpstreamDialLatencyMS: 6,
		UpdatedAt:             firstUpdatedAt.Add(time.Second),
	})

	association, exists := runtime.tunnelAssociationByID("tunnel-1")
	if !exists {
		testingObject.Fatalf("expected tunnel association exists")
	}
	if association.TrafficID != "traffic-1" || association.ServiceID != "svc-1" {
		testingObject.Fatalf("unexpected association identity: %+v", association)
	}
	if association.LocalAddr != "127.0.0.1:18080" {
		testingObject.Fatalf("unexpected local addr: %s", association.LocalAddr)
	}
	if association.OpenAckLatencyMS != 18 || association.UpstreamDialLatencyMS != 6 {
		testingObject.Fatalf("unexpected latency fields: %+v", association)
	}

	runtime.removeTunnelAssociation("tunnel-1")
	if _, exists := runtime.tunnelAssociationByID("tunnel-1"); exists {
		testingObject.Fatalf("expected association removed")
	}
}
