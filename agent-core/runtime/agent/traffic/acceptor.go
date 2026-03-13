package traffic

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

var (
	// ErrTrafficAcceptorDependencyMissing 表示 Acceptor 关键依赖缺失。
	ErrTrafficAcceptorDependencyMissing = errors.New("traffic acceptor dependency missing")
	// ErrTunnelReaderAlreadyOwned 表示 tunnel 已有 reader 所有权。
	ErrTunnelReaderAlreadyOwned = errors.New("tunnel reader already owned")
	// ErrFirstFrameNotTrafficOpen 表示首帧不是 TrafficOpen。
	ErrFirstFrameNotTrafficOpen = errors.New("first frame is not traffic open")
)

// PayloadReader 定义数据面消息读取能力。
type PayloadReader interface {
	// ReadPayload 读取一条 StreamPayload。
	ReadPayload(ctx context.Context) (pb.StreamPayload, error)
}

// OpenHandoff 描述 acceptor 向 runtime 的移交结果。
type OpenHandoff struct {
	TunnelID string
	Open     pb.TrafficOpen
	Lease    *ReaderLease
}

// ReaderLease 表示 tunnel reader 的所有权租约。
type ReaderLease struct {
	once     sync.Once
	acceptor *Acceptor
	tunnelID string
}

// Release 释放 reader 所有权。
func (lease *ReaderLease) Release() {
	if lease == nil || lease.acceptor == nil {
		return
	}
	lease.once.Do(func() {
		lease.acceptor.releaseReader(lease.tunnelID)
	})
}

// Acceptor 负责 idle tunnel 首帧接收与 reader 所有权管理。
type Acceptor struct {
	mutex            sync.Mutex
	ownedTunnelReads map[string]struct{}
}

// NewAcceptor 创建 traffic acceptor。
func NewAcceptor() *Acceptor {
	return &Acceptor{
		ownedTunnelReads: make(map[string]struct{}),
	}
}

// WaitTrafficOpen 独占读取首帧并移交 TrafficOpen 给 runtime。
func (acceptor *Acceptor) WaitTrafficOpen(ctx context.Context, tunnelID string, reader PayloadReader) (OpenHandoff, error) {
	if acceptor == nil {
		return OpenHandoff{}, fmt.Errorf("wait traffic open: %w", ErrTrafficAcceptorDependencyMissing)
	}
	if reader == nil {
		return OpenHandoff{}, fmt.Errorf("wait traffic open: %w: nil payload reader", ErrTrafficAcceptorDependencyMissing)
	}
	normalizedTunnelID := strings.TrimSpace(tunnelID)
	if normalizedTunnelID == "" {
		return OpenHandoff{}, fmt.Errorf("wait traffic open: %w: empty tunnel id", ErrTrafficAcceptorDependencyMissing)
	}

	lease, err := acceptor.claimReader(normalizedTunnelID)
	if err != nil {
		return OpenHandoff{}, err
	}
	normalizedContext := ctx
	if normalizedContext == nil {
		// nil context 时回落 Background，避免阻塞调用 panic。
		normalizedContext = context.Background()
	}

	payload, err := reader.ReadPayload(normalizedContext)
	if err != nil {
		lease.Release()
		return OpenHandoff{}, fmt.Errorf("wait traffic open: read first frame: %w", err)
	}
	if payload.ActivePayloadCount() != 1 || payload.OpenReq == nil {
		lease.Release()
		return OpenHandoff{}, fmt.Errorf(
			"wait traffic open: %w: tunnel=%s payload_count=%d",
			ErrFirstFrameNotTrafficOpen,
			normalizedTunnelID,
			payload.ActivePayloadCount(),
		)
	}
	return OpenHandoff{
		TunnelID: normalizedTunnelID,
		Open:     *payload.OpenReq,
		Lease:    lease,
	}, nil
}

// IsReaderOwned 返回 tunnel reader 是否仍被占用。
func (acceptor *Acceptor) IsReaderOwned(tunnelID string) bool {
	normalizedTunnelID := strings.TrimSpace(tunnelID)
	if normalizedTunnelID == "" {
		return false
	}
	acceptor.mutex.Lock()
	defer acceptor.mutex.Unlock()
	_, owned := acceptor.ownedTunnelReads[normalizedTunnelID]
	return owned
}

func (acceptor *Acceptor) claimReader(tunnelID string) (*ReaderLease, error) {
	acceptor.mutex.Lock()
	defer acceptor.mutex.Unlock()
	if _, exists := acceptor.ownedTunnelReads[tunnelID]; exists {
		return nil, fmt.Errorf("claim tunnel reader: %w: tunnel=%s", ErrTunnelReaderAlreadyOwned, tunnelID)
	}
	acceptor.ownedTunnelReads[tunnelID] = struct{}{}
	return &ReaderLease{
		acceptor: acceptor,
		tunnelID: tunnelID,
	}, nil
}

func (acceptor *Acceptor) releaseReader(tunnelID string) {
	acceptor.mutex.Lock()
	defer acceptor.mutex.Unlock()
	delete(acceptor.ownedTunnelReads, tunnelID)
}
