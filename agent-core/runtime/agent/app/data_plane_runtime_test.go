package app

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/agent-core/runtime/agent/service"
	"github.com/lifei6671/devbridge-loop/agent-core/runtime/agent/traffic"
	"github.com/lifei6671/devbridge-loop/agent-core/runtime/agent/tunnel"
	"github.com/lifei6671/devbridge-loop/ltfp/adapter"
	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
	"github.com/lifei6671/devbridge-loop/ltfp/transport"
)

type runtimeTrafficTestReadResult struct {
	payload pb.StreamPayload
	err     error
}

// runtimeTrafficTestTunnel 是 app data-plane 测试使用的 tunnel 假实现。
type runtimeTrafficTestTunnel struct {
	id string

	bindingType transport.BindingType

	readQueue  chan runtimeTrafficTestReadResult
	writeMutex sync.Mutex
	writes     []pb.StreamPayload

	closeMutex sync.Mutex
	closeCount int

	closeAckMutex      sync.Mutex
	closeAckObservedBy map[string]struct{}
	closeAckSentBy     map[string]struct{}
}

// newRuntimeTrafficTestTunnel 创建带读写缓冲的测试 tunnel。
func newRuntimeTrafficTestTunnel(tunnelID string) *runtimeTrafficTestTunnel {
	return &runtimeTrafficTestTunnel{
		id:                 tunnelID,
		readQueue:          make(chan runtimeTrafficTestReadResult, 8),
		closeAckObservedBy: make(map[string]struct{}),
		closeAckSentBy:     make(map[string]struct{}),
	}
}

// ID 返回 tunnel 标识。
func (tunnel *runtimeTrafficTestTunnel) ID() string {
	return tunnel.id
}

// BindingType 返回测试 tunnel 的承载类型（默认按 tcp 处理）。
func (tunnel *runtimeTrafficTestTunnel) BindingType() transport.BindingType {
	if tunnel == nil {
		return ""
	}
	if tunnel.bindingType == "" {
		return transport.BindingTypeTCPFramed
	}
	return tunnel.bindingType
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

// EnqueueReadError 预置下一次 ReadPayload 返回错误。
func (tunnel *runtimeTrafficTestTunnel) EnqueueReadError(err error) {
	tunnel.readQueue <- runtimeTrafficTestReadResult{err: err}
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

// MarkCloseAckObserved 记录某条 traffic 已观测到 close_ack。
func (tunnel *runtimeTrafficTestTunnel) MarkCloseAckObserved(trafficID string) {
	normalizedTrafficID := strings.TrimSpace(trafficID)
	if normalizedTrafficID == "" {
		return
	}
	tunnel.closeAckMutex.Lock()
	defer tunnel.closeAckMutex.Unlock()
	if tunnel.closeAckObservedBy == nil {
		tunnel.closeAckObservedBy = make(map[string]struct{})
	}
	tunnel.closeAckObservedBy[normalizedTrafficID] = struct{}{}
}

// TryMarkCloseAckSent 记录某条 traffic 的 close_ack 是否已发送。
func (tunnel *runtimeTrafficTestTunnel) TryMarkCloseAckSent(trafficID string) bool {
	normalizedTrafficID := strings.TrimSpace(trafficID)
	if normalizedTrafficID == "" {
		return false
	}
	tunnel.closeAckMutex.Lock()
	defer tunnel.closeAckMutex.Unlock()
	if tunnel.closeAckSentBy == nil {
		tunnel.closeAckSentBy = make(map[string]struct{})
	}
	if _, exists := tunnel.closeAckSentBy[normalizedTrafficID]; exists {
		return false
	}
	tunnel.closeAckSentBy[normalizedTrafficID] = struct{}{}
	return true
}

// HasCloseAckObserved 判断某条 traffic 是否已观测到 close_ack。
func (tunnel *runtimeTrafficTestTunnel) HasCloseAckObserved(trafficID string) bool {
	normalizedTrafficID := strings.TrimSpace(trafficID)
	if normalizedTrafficID == "" {
		return false
	}
	tunnel.closeAckMutex.Lock()
	defer tunnel.closeAckMutex.Unlock()
	_, observed := tunnel.closeAckObservedBy[normalizedTrafficID]
	return observed
}

// ClearCloseAckObserved 清理某条 traffic 的 close_ack 观测状态。
func (tunnel *runtimeTrafficTestTunnel) ClearCloseAckObserved(trafficID string) {
	normalizedTrafficID := strings.TrimSpace(trafficID)
	if normalizedTrafficID == "" {
		return
	}
	tunnel.closeAckMutex.Lock()
	defer tunnel.closeAckMutex.Unlock()
	delete(tunnel.closeAckObservedBy, normalizedTrafficID)
	delete(tunnel.closeAckSentBy, normalizedTrafficID)
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
			TrafficID:        "traffic-1",
			LogicalServiceID: "ls-1",
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

func TestRuntimeHintEndpointSelectorFallsBackToServiceCatalog(testingObject *testing.T) {
	testingObject.Parallel()

	catalog := service.NewCatalog()
	catalog.Upsert(time.Now().UTC(), adapter.LocalRegistration{
		LogicalServiceID: "ls-1",
		InstanceID:       "inst-1",
		Scope: pb.Scope{
			Namespace:   "dev",
			Environment: "demo",
		},
		ServiceName: "jelly",
		ServiceType: "http",
		Endpoints: []pb.ServiceEndpoint{
			{
				EndpointID: "ep-1",
				Protocol:   "http",
				Host:       "127.0.0.1",
				Port:       8080,
			},
		},
	})
	selector := &runtimeHintEndpointSelector{serviceCatalog: catalog}

	selectedEndpoint, err := selector.SelectEndpoint(context.Background(), "ls-1", map[string]string{})
	if err != nil {
		testingObject.Fatalf("select endpoint from catalog failed: %v", err)
	}
	if selectedEndpoint.ID != "ep-1" {
		testingObject.Fatalf("unexpected endpoint id: got=%s want=%s", selectedEndpoint.ID, "ep-1")
	}
	if selectedEndpoint.Addr != "127.0.0.1:8080" {
		testingObject.Fatalf("unexpected endpoint addr: got=%s want=%s", selectedEndpoint.Addr, "127.0.0.1:8080")
	}
	if selectedEndpoint.Protocol != "http" {
		testingObject.Fatalf("unexpected endpoint protocol: got=%s want=%s", selectedEndpoint.Protocol, "http")
	}
}

// TestHandleTunnelRecyclePhaseAcceptsGRPCTunnelIDMismatch 验证 grpc recycle 阶段允许对端 tunnel_id 与本地不一致。
func TestHandleTunnelRecyclePhaseAcceptsGRPCTunnelIDMismatch(testingObject *testing.T) {
	testingObject.Parallel()

	runtime, registry := newRuntimeWithTrafficWorkerDependenciesForTest(testingObject)
	testTunnel := newRuntimeTrafficTestTunnel("agent-tunnel-1")
	testTunnel.bindingType = transport.BindingTypeGRPCH2

	added, addErr := registry.TryAddOpenedAsIdle(time.Now().UTC(), testTunnel, 2)
	if addErr != nil {
		testingObject.Fatalf("add idle tunnel failed: %v", addErr)
	}
	if !added {
		testingObject.Fatalf("expected idle tunnel added")
	}
	if activateErr := runtime.tunnelManager.ActivateIdle(testTunnel.ID()); activateErr != nil {
		testingObject.Fatalf("activate tunnel failed: %v", activateErr)
	}

	testTunnel.EnqueueReadPayload(pb.StreamPayload{
		CloseAck: &pb.TrafficCloseAck{
			TrafficID: "traffic-1",
			Accepted:  true,
		},
	})
	testTunnel.EnqueueReadPayload(pb.StreamPayload{
		Recycle: &pb.TunnelRecycle{
			TunnelID:   "bridge-tunnel-9",
			RecycleSeq: 1,
			IsFinal:    false,
		},
	})

	recycled, finalClose, recycleErr := runtime.handleTunnelRecyclePhase(
		context.Background(),
		testTunnel.ID(),
		"traffic-1",
		testTunnel,
	)
	if recycleErr != nil {
		testingObject.Fatalf("handle recycle phase failed: %v", recycleErr)
	}
	if !recycled || finalClose {
		testingObject.Fatalf("unexpected recycle result recycled=%t finalClose=%t", recycled, finalClose)
	}

	writes := testTunnel.Writes()
	if len(writes) != 1 || writes[0].RecycleAck == nil {
		testingObject.Fatalf("expected one recycle_ack write, got=%+v", writes)
	}
	if writes[0].RecycleAck.TunnelID != "bridge-tunnel-9" || !writes[0].RecycleAck.Accepted {
		testingObject.Fatalf("unexpected recycle_ack payload: %+v", writes[0].RecycleAck)
	}
}

// TestHandleTunnelRecyclePhaseRejectsRecycleWithoutCloseAck 验证未观测到 close_ack 时会拒绝 recycle。
func TestHandleTunnelRecyclePhaseRejectsRecycleWithoutCloseAck(testingObject *testing.T) {
	testingObject.Parallel()

	runtime, registry := newRuntimeWithTrafficWorkerDependenciesForTest(testingObject)
	testTunnel := newRuntimeTrafficTestTunnel("agent-tunnel-no-close-ack")

	added, addErr := registry.TryAddOpenedAsIdle(time.Now().UTC(), testTunnel, 2)
	if addErr != nil {
		testingObject.Fatalf("add idle tunnel failed: %v", addErr)
	}
	if !added {
		testingObject.Fatalf("expected idle tunnel added")
	}
	if activateErr := runtime.tunnelManager.ActivateIdle(testTunnel.ID()); activateErr != nil {
		testingObject.Fatalf("activate tunnel failed: %v", activateErr)
	}

	testTunnel.EnqueueReadPayload(pb.StreamPayload{
		Recycle: &pb.TunnelRecycle{
			TunnelID:   testTunnel.ID(),
			RecycleSeq: 1,
			IsFinal:    false,
		},
	})

	recycled, finalClose, recycleErr := runtime.handleTunnelRecyclePhase(
		context.Background(),
		testTunnel.ID(),
		"traffic-1",
		testTunnel,
	)
	if recycleErr == nil {
		testingObject.Fatalf("expected recycle phase to fail without close ack")
	}
	if recycled || finalClose {
		testingObject.Fatalf("unexpected recycle result recycled=%t finalClose=%t", recycled, finalClose)
	}
	if ltfperrors.ExtractTunnelRecycleCode(recycleErr) != ltfperrors.CodeTunnelRecycleCloseAckRequired {
		testingObject.Fatalf("unexpected recycle error code: got=%s err=%v", ltfperrors.ExtractTunnelRecycleCode(recycleErr), recycleErr)
	}

	writes := testTunnel.Writes()
	if len(writes) != 1 || writes[0].RecycleAck == nil {
		testingObject.Fatalf("expected one reject recycle_ack write, got=%+v", writes)
	}
	if writes[0].RecycleAck.Accepted {
		testingObject.Fatalf("expected recycle_ack rejected when close ack missing")
	}
	if strings.TrimSpace(writes[0].RecycleAck.ErrorCode) != ltfperrors.CodeTunnelRecycleCloseAckRequired {
		testingObject.Fatalf("unexpected recycle_ack error code: %+v", writes[0].RecycleAck)
	}
}

// TestHandleTunnelRecyclePhaseAcceptsRecycleWhenCloseAckObservedInRelay 验证 relay 阶段已观测 close_ack 时可直接进入 recycle。
func TestHandleTunnelRecyclePhaseAcceptsRecycleWhenCloseAckObservedInRelay(testingObject *testing.T) {
	testingObject.Parallel()

	runtime, registry := newRuntimeWithTrafficWorkerDependenciesForTest(testingObject)
	testTunnel := newRuntimeTrafficTestTunnel("agent-tunnel-close-ack-observed")

	added, addErr := registry.TryAddOpenedAsIdle(time.Now().UTC(), testTunnel, 2)
	if addErr != nil {
		testingObject.Fatalf("add idle tunnel failed: %v", addErr)
	}
	if !added {
		testingObject.Fatalf("expected idle tunnel added")
	}
	if activateErr := runtime.tunnelManager.ActivateIdle(testTunnel.ID()); activateErr != nil {
		testingObject.Fatalf("activate tunnel failed: %v", activateErr)
	}

	testTunnel.MarkCloseAckObserved("traffic-1")
	testTunnel.EnqueueReadPayload(pb.StreamPayload{
		Recycle: &pb.TunnelRecycle{
			TunnelID:   testTunnel.ID(),
			RecycleSeq: 1,
			IsFinal:    false,
		},
	})

	recycled, finalClose, recycleErr := runtime.handleTunnelRecyclePhase(
		context.Background(),
		testTunnel.ID(),
		"traffic-1",
		testTunnel,
	)
	if recycleErr != nil {
		testingObject.Fatalf("expected recycle success when close_ack already observed, got=%v", recycleErr)
	}
	if !recycled || finalClose {
		testingObject.Fatalf("unexpected recycle result recycled=%t finalClose=%t", recycled, finalClose)
	}
	if testTunnel.HasCloseAckObserved("traffic-1") {
		testingObject.Fatalf("expected close_ack observed state cleared after recycle phase")
	}

	writes := testTunnel.Writes()
	if len(writes) != 1 || writes[0].RecycleAck == nil || !writes[0].RecycleAck.Accepted {
		testingObject.Fatalf("expected one accepted recycle_ack write, got=%+v", writes)
	}
}

// TestHandleTunnelRecyclePhaseReturnsDeadlineHitCode 验证等待 recycle 载荷超时时会返回统一 deadline_hit 错误码。
func TestHandleTunnelRecyclePhaseReturnsDeadlineHitCode(testingObject *testing.T) {
	testingObject.Parallel()

	runtime, registry := newRuntimeWithTrafficWorkerDependenciesForTest(testingObject)
	testTunnel := newRuntimeTrafficTestTunnel("agent-tunnel-recycle-timeout")

	added, addErr := registry.TryAddOpenedAsIdle(time.Now().UTC(), testTunnel, 2)
	if addErr != nil {
		testingObject.Fatalf("add idle tunnel failed: %v", addErr)
	}
	if !added {
		testingObject.Fatalf("expected idle tunnel added")
	}
	if activateErr := runtime.tunnelManager.ActivateIdle(testTunnel.ID()); activateErr != nil {
		testingObject.Fatalf("activate tunnel failed: %v", activateErr)
	}

	testTunnel.EnqueueReadError(context.DeadlineExceeded)

	recycled, finalClose, recycleErr := runtime.handleTunnelRecyclePhase(
		context.Background(),
		testTunnel.ID(),
		"traffic-timeout",
		testTunnel,
	)
	if recycleErr == nil {
		testingObject.Fatalf("expected recycle phase timeout error")
	}
	if recycled || finalClose {
		testingObject.Fatalf("unexpected recycle result recycled=%t finalClose=%t", recycled, finalClose)
	}
	if ltfperrors.ExtractTunnelRecycleCode(recycleErr) != ltfperrors.CodeTunnelRecycleDeadlineHit {
		testingObject.Fatalf("unexpected recycle error code: got=%s err=%v", ltfperrors.ExtractTunnelRecycleCode(recycleErr), recycleErr)
	}
	if len(testTunnel.Writes()) != 0 {
		testingObject.Fatalf("expected no recycle ack writes on read timeout, got=%+v", testTunnel.Writes())
	}
}

// TestHandleTunnelRecyclePhaseDoesNotRepeatCloseAck 验证 recycle 阶段再次收到 close 时不会重复回 ACK。
func TestHandleTunnelRecyclePhaseDoesNotRepeatCloseAck(testingObject *testing.T) {
	testingObject.Parallel()

	runtime, registry := newRuntimeWithTrafficWorkerDependenciesForTest(testingObject)
	testTunnel := newRuntimeTrafficTestTunnel("agent-tunnel-close-ack-once")

	added, addErr := registry.TryAddOpenedAsIdle(time.Now().UTC(), testTunnel, 2)
	if addErr != nil {
		testingObject.Fatalf("add idle tunnel failed: %v", addErr)
	}
	if !added {
		testingObject.Fatalf("expected idle tunnel added")
	}
	if activateErr := runtime.tunnelManager.ActivateIdle(testTunnel.ID()); activateErr != nil {
		testingObject.Fatalf("activate tunnel failed: %v", activateErr)
	}
	if !testTunnel.TryMarkCloseAckSent("traffic-1") {
		testingObject.Fatalf("expected initial close_ack sent mark success")
	}

	testTunnel.EnqueueReadPayload(pb.StreamPayload{
		Close: &pb.TrafficClose{
			TrafficID: "traffic-1",
			Reason:    "bridge_close_retry",
		},
	})
	testTunnel.EnqueueReadPayload(pb.StreamPayload{
		Recycle: &pb.TunnelRecycle{
			TunnelID:   testTunnel.ID(),
			RecycleSeq: 1,
			IsFinal:    false,
		},
	})

	recycled, finalClose, recycleErr := runtime.handleTunnelRecyclePhase(
		context.Background(),
		testTunnel.ID(),
		"traffic-1",
		testTunnel,
	)
	if recycleErr != nil {
		testingObject.Fatalf("expected recycle success after duplicate close, got=%v", recycleErr)
	}
	if !recycled || finalClose {
		testingObject.Fatalf("unexpected recycle result recycled=%t finalClose=%t", recycled, finalClose)
	}

	writes := testTunnel.Writes()
	if len(writes) != 1 || writes[0].RecycleAck == nil || !writes[0].RecycleAck.Accepted {
		testingObject.Fatalf("expected only recycle_ack write after duplicate close, got=%+v", writes)
	}
}

func TestRuntimeHintEndpointSelectorPrefersHintOverCatalog(testingObject *testing.T) {
	testingObject.Parallel()

	catalog := service.NewCatalog()
	catalog.Upsert(time.Now().UTC(), adapter.LocalRegistration{
		LogicalServiceID: "ls-1",
		InstanceID:       "inst-1",
		Scope: pb.Scope{
			Namespace:   "dev",
			Environment: "demo",
		},
		ServiceName: "jelly",
		ServiceType: "http",
		Endpoints: []pb.ServiceEndpoint{
			{
				EndpointID: "ep-1",
				Protocol:   "http",
				Host:       "127.0.0.1",
				Port:       8080,
			},
		},
	})
	selector := &runtimeHintEndpointSelector{serviceCatalog: catalog}

	selectedEndpoint, err := selector.SelectEndpoint(context.Background(), "ls-1", map[string]string{
		trafficOpenHintEndpointIDKey:   "ep-hint",
		trafficOpenHintEndpointAddrKey: "127.0.0.1:9090",
	})
	if err != nil {
		testingObject.Fatalf("select endpoint with hint failed: %v", err)
	}
	if selectedEndpoint.ID != "ep-hint" {
		testingObject.Fatalf("unexpected endpoint id: got=%s want=%s", selectedEndpoint.ID, "ep-hint")
	}
	if selectedEndpoint.Addr != "127.0.0.1:9090" {
		testingObject.Fatalf("unexpected endpoint addr: got=%s want=%s", selectedEndpoint.Addr, "127.0.0.1:9090")
	}
}

// TestRuntimeHintEndpointSelectorReturnsHTTPSMetadata 验证目录回退可保留 endpoint protocol/sni。
func TestRuntimeHintEndpointSelectorReturnsHTTPSMetadata(testingObject *testing.T) {
	testingObject.Parallel()

	catalog := service.NewCatalog()
	catalog.Upsert(time.Now().UTC(), adapter.LocalRegistration{
		LogicalServiceID: "ls-https-1",
		InstanceID:       "inst-https-1",
		Scope: pb.Scope{
			Namespace:   "dev",
			Environment: "demo",
		},
		ServiceName: "deepin-service",
		ServiceType: "https",
		Endpoints: []pb.ServiceEndpoint{
			{
				EndpointID: "ep-https-1",
				Protocol:   "https",
				Host:       "127.0.0.1",
				Port:       8443,
				ServerName: "deepin.disign.me",
			},
		},
	})
	selector := &runtimeHintEndpointSelector{serviceCatalog: catalog}

	selectedEndpoint, err := selector.SelectEndpoint(context.Background(), "ls-https-1", map[string]string{})
	if err != nil {
		testingObject.Fatalf("select https endpoint from catalog failed: %v", err)
	}
	if selectedEndpoint.Protocol != "https" {
		testingObject.Fatalf("unexpected endpoint protocol: got=%s want=%s", selectedEndpoint.Protocol, "https")
	}
	if selectedEndpoint.ServerName != "deepin.disign.me" {
		testingObject.Fatalf("unexpected endpoint server_name: got=%s want=%s", selectedEndpoint.ServerName, "deepin.disign.me")
	}
}

// TestRuntimeNetUpstreamDialerUsesTLSForHTTPS 验证 https endpoint 会走 TLS 拨号。
func TestRuntimeNetUpstreamDialerUsesTLSForHTTPS(testingObject *testing.T) {
	testingObject.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		testingObject.Fatalf("listen failed: %v", err)
	}
	defer listener.Close()

	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer connection.Close()
		_, _ = connection.Write([]byte("not-tls"))
	}()

	dialer := &runtimeNetUpstreamDialer{dialTimeout: 2 * time.Second}
	_, err = dialer.Dial(context.Background(), traffic.Endpoint{
		ID:         "ep-https-1",
		Addr:       listener.Addr().String(),
		Protocol:   "https",
		ServerName: "deepin.disign.me",
	})
	if err == nil {
		testingObject.Fatalf("expected tls dial error for non-tls upstream")
	}
	normalizedError := strings.ToLower(err.Error())
	if !strings.Contains(normalizedError, "tls") {
		testingObject.Fatalf("expected tls dial path error, got: %v", err)
	}
	<-serverDone
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
		TunnelID:         "tunnel-1",
		TrafficID:        "traffic-1",
		LogicalServiceID: "ls-1",
		InstanceID:       "inst-1",
		UpdatedAt:        firstUpdatedAt,
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
	if association.TrafficID != "traffic-1" || association.LogicalServiceID != "ls-1" || association.InstanceID != "inst-1" {
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
