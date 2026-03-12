package consistency

import "sync"

// ReplayWindow 维护固定容量的事件去重窗口。
type ReplayWindow struct {
	mu       sync.Mutex
	capacity int
	order    []string
	seen     map[string]struct{}
}

// NewReplayWindow 创建事件去重窗口。
func NewReplayWindow(capacity int) *ReplayWindow {
	if capacity <= 0 {
		// capacity 非法时回退到最小可用值，避免调用方零值配置导致失效。
		capacity = 1
	}
	return &ReplayWindow{
		capacity: capacity,
		order:    make([]string, 0, capacity),
		seen:     make(map[string]struct{}, capacity),
	}
}

// SeenOrAdd 判断去重键是否已存在，不存在时写入窗口。
func (window *ReplayWindow) SeenOrAdd(key string) bool {
	window.mu.Lock()
	defer window.mu.Unlock()

	// 已命中则直接返回 true，调用方应按 duplicate 语义处理。
	if _, exists := window.seen[key]; exists {
		return true
	}

	// 新 key 写入哈希集合和顺序队列。
	window.seen[key] = struct{}{}
	window.order = append(window.order, key)
	// 超出容量时移除最旧 key，保持窗口大小稳定。
	if len(window.order) > window.capacity {
		evicted := window.order[0]
		window.order = window.order[1:]
		delete(window.seen, evicted)
	}
	return false
}

// Size 返回当前窗口中记录的去重键数量。
func (window *ReplayWindow) Size() int {
	window.mu.Lock()
	defer window.mu.Unlock()
	// size 直接由 seen map 长度决定。
	return len(window.seen)
}
