package control

import (
	"sync"
	"time"
)

// AckDeduper 防止 ACK 重放产生重复副作用。
type AckDeduper struct {
	mu   sync.Mutex
	ttl  time.Duration
	seen map[string]time.Time
}

// NewAckDeduper 创建去重器，ttl 为缓存保留时间。
func NewAckDeduper(ttl time.Duration) *AckDeduper {
	if ttl <= 0 {
		// 默认 TTL 避免无限增长。
		ttl = time.Minute
	}
	return &AckDeduper{
		ttl:  ttl,
		seen: make(map[string]time.Time),
	}
}

// Seen 判断 eventID 是否重复；返回 true 表示已处理过。
func (d *AckDeduper) Seen(eventID string, now time.Time) bool {
	if eventID == "" {
		// 空事件不参与去重。
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	// 先清理过期项，避免误判。
	d.sweepLocked(now)
	if ts, ok := d.seen[eventID]; ok && now.Sub(ts) <= d.ttl {
		return true
	}
	d.seen[eventID] = now
	return false
}

// Sweep 清理过期 eventID。
func (d *AckDeduper) Sweep(now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	// 外部触发清理，控制内存上界。
	d.sweepLocked(now)
}

// sweepLocked 在持锁情况下清理过期 eventID。
func (d *AckDeduper) sweepLocked(now time.Time) {
	for id, ts := range d.seen {
		// 过期后删除条目。
		if now.Sub(ts) > d.ttl {
			delete(d.seen, id)
		}
	}
}
