package serviceregistry

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	// ErrWatcherClosed 表示 watcher 已关闭。
	ErrWatcherClosed = errors.New("watcher is closed")
)

// SearchFunc 抽象 watcher 所需的查询能力。
type SearchFunc func(ctx context.Context, in SearchInput) ([]Service, error)

type pollingWatcher struct {
	mu       sync.Mutex
	ctx      context.Context
	cancel   context.CancelFunc
	key      string
	interval time.Duration
	searchFn SearchFunc
	closed   bool
	lastHash string
}

// NewPollingWatcher 创建一个基于轮询的 Watcher。
func NewPollingWatcher(ctx context.Context, key string, interval time.Duration, searchFn SearchFunc) (Watcher, error) {
	if searchFn == nil {
		return nil, fmt.Errorf("search function is nil")
	}
	if interval <= 0 {
		interval = 2 * time.Second
	}
	watchCtx, cancel := context.WithCancel(ctx)
	return &pollingWatcher{
		ctx:      watchCtx,
		cancel:   cancel,
		key:      strings.TrimSpace(key),
		interval: interval,
		searchFn: searchFn,
	}, nil
}

// Proceed 以阻塞方式等待变化；有变更时返回完整服务列表。
func (w *pollingWatcher) Proceed() (services []Service, err error) {
	if w == nil {
		return nil, ErrWatcherClosed
	}

	for {
		if err := w.ensureOpen(); err != nil {
			return nil, err
		}

		result, err := w.searchFn(w.ctx, SearchInput{Prefix: w.key})
		if err != nil {
			// context 取消视为正常终止，避免把关闭动作误报为业务错误。
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil, ErrWatcherClosed
			}
			return nil, fmt.Errorf("watch search failed: %w", err)
		}

		currentHash := hashServices(result)
		w.mu.Lock()
		firstRound := w.lastHash == ""
		changed := currentHash != w.lastHash
		if firstRound || changed {
			w.lastHash = currentHash
			w.mu.Unlock()
			return cloneServices(result), nil
		}
		w.mu.Unlock()

		timer := time.NewTimer(w.interval)
		select {
		case <-w.ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return nil, ErrWatcherClosed
		case <-timer.C:
		}
	}
}

// Close 关闭 watcher。
func (w *pollingWatcher) Close() error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil
	}
	w.closed = true
	cancel := w.cancel
	w.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	return nil
}

func (w *pollingWatcher) ensureOpen() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return ErrWatcherClosed
	}
	select {
	case <-w.ctx.Done():
		w.closed = true
		return ErrWatcherClosed
	default:
		return nil
	}
}

func hashServices(services []Service) string {
	if len(services) == 0 {
		return ""
	}
	signatures := make([]string, 0, len(services))
	for _, service := range services {
		if service == nil {
			continue
		}
		signatures = append(signatures, service.GetKey()+"|"+service.GetValue())
	}
	sort.Strings(signatures)
	return strings.Join(signatures, "||")
}

func cloneServices(services []Service) []Service {
	if len(services) == 0 {
		return []Service{}
	}
	cloned := make([]Service, 0, len(services))
	for _, service := range services {
		if service == nil {
			continue
		}
		cloned = append(cloned, NewBasicService(
			service.GetName(),
			WithServiceVersion(service.GetVersion()),
			WithServiceMetadata(service.GetMetadata()),
			WithServiceEndpoints(service.GetEndpoints()),
			WithServiceKey(service.GetKey()),
			WithServiceValue(service.GetValue()),
			WithServicePrefix(service.GetPrefix()),
		))
	}
	return cloned
}
