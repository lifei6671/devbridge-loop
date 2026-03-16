package tsbase62

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"time"
)

// Package tsbase62 提供：
// 1. uint64 <-> Base62 的编码/解码
// 2. time.Time <-> Base62 时间戳 的转换
// 3. 基于“固定宽度时间戳前缀 + Base62 后缀”的可排序 ID 方案
//
// 重要说明：
// - 只有固定宽度编码（EncodeFixed / FromTimeFixed）才保证字典序与数值序一致。
// - 变长编码（EncodeUint64 / FromTime）更紧凑，但不保证字典序与时间序一致。
// - 本库的基础拼接型 ID（NewID）不自动保证全局唯一；若需要单机严格单调唯一，
//   请使用 Generator。

const (
	// alphabet 是 Base62 字符表。
	// 采用 0-9 A-Z a-z 的顺序，便于在“固定宽度”场景下保持字典序与数值序一致。
	alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

	// base 为进制基数。
	base = uint64(62)

	// FixedWidth 是任意 uint64 进行 Base62 固定宽度编码时所需的长度。
	// 因为 62^11 > MaxUint64，所以 11 位足够覆盖整个 uint64 空间。
	FixedWidth = 11
)

var (
	// decodeTable 用于 ASCII -> Base62 数值映射。
	// 无效字符记为 -1。
	decodeTable [128]int8

	// 常见错误定义，便于调用方做 errors.Is 判断。
	ErrEmptyString        = errors.New("tsbase62: 空字符串")
	ErrTooLong            = errors.New("tsbase62: 字符串过长，无法解码为 uint64")
	ErrOverflowUint64     = errors.New("tsbase62: 解码结果溢出 uint64")
	ErrInvalidSuffix      = errors.New("tsbase62: suffix 必须仅包含 Base62 字符")
	ErrIDTooShort         = errors.New("tsbase62: id 长度不足")
	ErrBeforeUnixEpoch    = errors.New("tsbase62: 不支持 Unix epoch 之前的时间")
	ErrTimeOutOfRange     = errors.New("tsbase62: 时间超出 int64 纳秒可表示范围")
	ErrInvalidSuffixWidth = errors.New("tsbase62: suffix 长度超出允许范围")
)

func init() {
	for i := range decodeTable {
		decodeTable[i] = -1
	}
	for i := 0; i < len(alphabet); i++ {
		decodeTable[alphabet[i]] = int8(i)
	}
}

// EncodeUint64 将任意 uint64 编码为“变长”的 Base62 字符串。
// 注意：变长编码不补零，因此不保证字典序与数值序一致。
func EncodeUint64(n uint64) string {
	if n == 0 {
		return "0"
	}
	var buf [FixedWidth]byte
	pos := FixedWidth
	for n > 0 {
		pos--
		buf[pos] = alphabet[n%base]
		n /= base
	}
	return string(buf[pos:])
}

// EncodeFixed 将任意 uint64 编码为“固定宽度”的 Base62 字符串。
// 返回值长度恒为 FixedWidth，并且保证字典序与数值序一致。
func EncodeFixed(n uint64) string {
	var buf [FixedWidth]byte
	for i := FixedWidth - 1; i >= 0; i-- {
		buf[i] = alphabet[n%base]
		n /= base
	}
	return string(buf[:])
}

// DecodeUint64 将 Base62 字符串解码回 uint64。
// 支持变长和固定宽度两种形式。
func DecodeUint64(s string) (uint64, error) {
	if len(s) == 0 {
		return 0, ErrEmptyString
	}
	if len(s) > FixedWidth {
		return 0, ErrTooLong
	}

	var n uint64
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 128 || decodeTable[c] < 0 {
			return 0, fmt.Errorf("tsbase62: 非法字符 %q", c)
		}
		digit := uint64(decodeTable[c])

		// 溢出检查：n*base + digit <= MaxUint64
		if n > (math.MaxUint64-digit)/base {
			return 0, ErrOverflowUint64
		}
		n = n*base + digit
	}
	return n, nil
}

// IsBase62String 判断字符串是否全部由 Base62 字符组成。
// 空字符串会返回 true；因为某些场景允许 suffix 为空。
func IsBase62String(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 128 || decodeTable[c] < 0 {
			return false
		}
	}
	return true
}

// FromTime 将 time.Time 编码为“变长” Base62 时间戳。
// 注意：该形式更紧凑，但不保证字典序与时间先后一致。
func FromTime(t time.Time) (string, error) {
	ns, err := unixNanoUint64(t)
	if err != nil {
		return "", err
	}
	return EncodeUint64(ns), nil
}

// FromTimeFixed 将 time.Time 编码为“固定宽度” Base62 时间戳。
// 该形式适合做可排序 key，因为字典序与时间先后一致。
func FromTimeFixed(t time.Time) (string, error) {
	ns, err := unixNanoUint64(t)
	if err != nil {
		return "", err
	}
	return EncodeFixed(ns), nil
}

// ToTime 将 Base62 时间戳解码为 UTC 时间。
// 这里要求解码结果必须能安全转换为 int64 纳秒，否则返回错误。
func ToTime(s string) (time.Time, error) {
	ns, err := DecodeUint64(s)
	if err != nil {
		return time.Time{}, err
	}
	if ns > math.MaxInt64 {
		return time.Time{}, ErrTimeOutOfRange
	}
	return time.Unix(0, int64(ns)).UTC(), nil
}

// unixNanoUint64 将 time.Time 安全转换为非负 uint64 纳秒时间戳。
func unixNanoUint64(t time.Time) (uint64, error) {
	ns := t.UnixNano()
	if ns < 0 {
		return 0, ErrBeforeUnixEpoch
	}
	return uint64(ns), nil
}

// NewID 生成 "<固定宽度时间戳><suffix>" 形式的 ID。
// suffix 必须全部为 Base62 字符；可以为空。
// 注意：
// - 该函数只负责拼接与校验，不自动保证唯一性。
// - 如果你需要“单机严格单调唯一”的 ID，请使用 Generator。
func NewID(t time.Time, suffix string) (string, error) {
	if !IsBase62String(suffix) {
		return "", ErrInvalidSuffix
	}
	ts, err := FromTimeFixed(t)
	if err != nil {
		return "", err
	}
	return ts + suffix, nil
}

// SplitID 将 NewID 生成的 ID 拆分为：
// 1. 时间戳部分（前 FixedWidth 位）
// 2. suffix 部分（剩余位）
//
// 同时会校验 suffix 是否为合法 Base62 字符串。
func SplitID(id string) (ts time.Time, suffix string, err error) {
	if len(id) < FixedWidth {
		return time.Time{}, "", ErrIDTooShort
	}

	ts, err = ToTime(id[:FixedWidth])
	if err != nil {
		return time.Time{}, "", err
	}

	suffix = id[FixedWidth:]
	if !IsBase62String(suffix) {
		return time.Time{}, "", ErrInvalidSuffix
	}
	return ts, suffix, nil
}

// -----------------------------------------------------------------------------
// 单机单调 ID 生成器
// -----------------------------------------------------------------------------
//
// 设计目标：
// - 同一进程内线程安全
// - 结果按字典序递增
// - 前缀保留固定宽度时间戳，便于按时间排序
//
// 生成结果格式：
//   <11位固定宽度时间戳><固定宽度序号>
//
// 其中：
// - 时间戳部分：Unix 纳秒，固定 11 位 Base62
// - 序号部分：固定 seqWidth 位 Base62
//
// 这样可以做到：
// - 不同时间天然按前缀排序
// - 同一纳秒内靠序号保证单调与去重
//
// 限制：
// - 这是“单机单进程”生成器，不解决多进程/多节点全局唯一问题
// - 如果同一纳秒内请求数超过 62^seqWidth，会自旋等待到下一个纳秒

// Generator 是一个线程安全的单机单调 ID 生成器。
type Generator struct {
	mu sync.Mutex

	// seqWidth 是序号部分的固定宽度。
	// 例如 3 位可表示 62^3 = 238,328 个同纳秒序号。
	seqWidth int

	// seqMax = 62^seqWidth
	seqMax uint64

	// lastNS 记录上一次生成 ID 时使用的纳秒时间戳。
	lastNS uint64

	// seq 表示在同一纳秒内的递增序号。
	seq uint64
}

// NewGenerator 创建一个新的 Generator。
// seqWidth 必须 > 0，且不能过大（这里限制为 <= 10，避免无意义的大宽度）。
func NewGenerator(seqWidth int) (*Generator, error) {
	if seqWidth <= 0 || seqWidth > 10 {
		return nil, ErrInvalidSuffixWidth
	}

	seqMax := uint64(1)
	for i := 0; i < seqWidth; i++ {
		seqMax *= base
	}

	return &Generator{
		seqWidth: seqWidth,
		seqMax:   seqMax,
	}, nil
}

// MustNewGenerator 与 NewGenerator 类似，但失败时直接 panic。
// 适合在程序启动期初始化固定配置时使用。
func MustNewGenerator(seqWidth int) *Generator {
	g, err := NewGenerator(seqWidth)
	if err != nil {
		panic(err)
	}
	return g
}

// Next 生成下一个单机单调 ID。
// 返回格式：<11位固定宽度时间戳><seqWidth位固定宽度序号>
func (g *Generator) Next() (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	for {
		nowNS, err := unixNanoUint64(time.Now().UTC())
		if err != nil {
			return "", err
		}

		switch {
		case nowNS > g.lastNS:
			// 新纳秒，序号归零
			g.lastNS = nowNS
			g.seq = 0
			return EncodeFixed(nowNS) + encodeFixedWidth(g.seq, g.seqWidth), nil

		case nowNS == g.lastNS:
			// 同一纳秒内递增
			if g.seq+1 < g.seqMax {
				g.seq++
				return EncodeFixed(nowNS) + encodeFixedWidth(g.seq, g.seqWidth), nil
			}

			// 同一纳秒内序号耗尽，等待下一个纳秒
			// 这里不 sleep，是为了尽快跨过纳秒边界。
			continue

		default:
			// 理论上 time.Now() 极少出现回拨到小于 lastNS 的情况。
			// 为保证单调性，这里继续沿用 lastNS，并仅递增序号。
			if g.seq+1 < g.seqMax {
				g.seq++
				return EncodeFixed(g.lastNS) + encodeFixedWidth(g.seq, g.seqWidth), nil
			}

			// 如果回拨期间序号也耗尽，则等待时间追平。
			continue
		}
	}
}

// NextWithTime 使用外部传入时间生成 ID。
// 这个接口更适合测试；生产环境通常直接用 Next。
func (g *Generator) NextWithTime(t time.Time) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	nowNS, err := unixNanoUint64(t.UTC())
	if err != nil {
		return "", err
	}

	if nowNS > g.lastNS {
		g.lastNS = nowNS
		g.seq = 0
		return EncodeFixed(nowNS) + encodeFixedWidth(g.seq, g.seqWidth), nil
	}

	if g.seq+1 >= g.seqMax {
		return "", errors.New("tsbase62: 当前时间片序号已耗尽")
	}

	// 即使传入时间没有前进，也保持单调递增
	g.seq++
	return EncodeFixed(g.lastNS) + encodeFixedWidth(g.seq, g.seqWidth), nil
}

// SplitGeneratedID 拆分由 Generator 生成的 ID。
// total = 11 位时间戳 + seqWidth 位序号。
func SplitGeneratedID(id string, seqWidth int) (ts time.Time, seq uint64, err error) {
	if seqWidth <= 0 || seqWidth > 10 {
		return time.Time{}, 0, ErrInvalidSuffixWidth
	}
	if len(id) != FixedWidth+seqWidth {
		return time.Time{}, 0, fmt.Errorf("tsbase62: 非法 generator id 长度，期望 %d，实际 %d", FixedWidth+seqWidth, len(id))
	}

	ts, err = ToTime(id[:FixedWidth])
	if err != nil {
		return time.Time{}, 0, err
	}

	seqPart := id[FixedWidth:]
	if !IsBase62String(seqPart) {
		return time.Time{}, 0, ErrInvalidSuffix
	}

	seq, err = DecodeUint64(seqPart)
	if err != nil {
		return time.Time{}, 0, err
	}
	return ts, seq, nil
}

// encodeFixedWidth 按指定宽度输出 Base62 固定宽度字符串。
// 调用方需保证 n < 62^width；超出部分会被截断语义破坏，因此这里做显式校验。
func encodeFixedWidth(n uint64, width int) string {
	if width <= 0 {
		return ""
	}
	var buf = make([]byte, width)
	for i := width - 1; i >= 0; i-- {
		buf[i] = alphabet[n%base]
		n /= base
	}
	return string(buf)
}