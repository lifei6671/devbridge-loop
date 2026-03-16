package protodetect

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net"
	"time"
)

// Protocol 表示探测结果。
type Protocol string

const (
	ProtoUnknown Protocol = "unknown"
	ProtoHTTP    Protocol = "http"
	ProtoHTTPS   Protocol = "https"
)

// DetectResult 表示探测结果及命中的原始前缀。
type DetectResult struct {
	Protocol Protocol
	Peeked   []byte
}

// 常见 HTTP 方法前缀。
// 这里只做“客户端首包”探测，因此判断请求行即可。
var httpMethods = [][]byte{
	[]byte("GET "),
	[]byte("POST "),
	[]byte("PUT "),
	[]byte("DELETE "),
	[]byte("PATCH "),
	[]byte("HEAD "),
	[]byte("OPTIONS "),
	[]byte("CONNECT "),
	[]byte("TRACE "),
}

// DetectBytes 基于前缀字节探测 HTTP / HTTPS。
// 这是纯函数，不会有任何副作用。
func DetectBytes(b []byte) Protocol {
	// 先判断 HTTPS（TLS ClientHello）
	if looksLikeTLSClientHello(b) {
		return ProtoHTTPS
	}

	// 再判断 HTTP 明文请求
	if looksLikeHTTPRequest(b) {
		return ProtoHTTP
	}

	return ProtoUnknown
}

// DetectReader 基于 bufio.Reader 做无副作用探测。
// 它只使用 Peek，不消费底层流。
func DetectReader(r *bufio.Reader) (DetectResult, error) {
	// 先尽量 peek 一小段。32 字节对 HTTP/TLS 首包通常够用。
	const maxPeek = 32

	b, err := r.Peek(maxPeek)
	if err != nil {
		// bufio.Reader 在缓冲不足但已有数据时，可能返回 ErrBufferFull；
		// 也可能在数据不足时返回 EOF。这里不能简单失败。
		//
		// 所以如果拿到了一部分数据，也要尝试探测。
		if !errors.Is(err, bufio.ErrBufferFull) && !errors.Is(err, io.EOF) {
			// 再尝试拿最少 1 字节看看
			b1, err1 := r.Peek(1)
			if err1 != nil {
				return DetectResult{}, err
			}
			return DetectResult{
				Protocol: DetectBytes(b1),
				Peeked:   append([]byte(nil), b1...),
			}, nil
		}
	}

	// 即使 err != nil，只要 b 非空，仍然可以做探测。
	copied := append([]byte(nil), b...)
	return DetectResult{
		Protocol: DetectBytes(copied),
		Peeked:   copied,
	}, nil
}

// DetectConn 基于 net.Conn 做探测，并返回一个“可继续正常读取”的连接。
// 它会实际从 conn 读取少量字节，因此必须把这些字节回灌给后续读取者。
func DetectConn(conn net.Conn, timeout time.Duration) (DetectResult, net.Conn, error) {
	const maxPeek = 32

	// 设置短超时，避免一直阻塞在首字节上。
	if timeout > 0 {
		_ = conn.SetReadDeadline(time.Now().Add(timeout))
		defer conn.SetReadDeadline(time.Time{})
	}

	buf := make([]byte, maxPeek)

	// 注意：
	// 这里不是 ReadAtLeast(maxPeek)，因为我们不想强等满 32 字节。
	// 只要首包来了若干字节，就足够判断 HTTP / HTTPS。
	n, err := conn.Read(buf)
	if err != nil {
		return DetectResult{}, nil, err
	}

	buf = buf[:n]
	result := DetectResult{
		Protocol: DetectBytes(buf),
		Peeked:   append([]byte(nil), buf...),
	}

	// 用 PeekedConn 把探测时读出的字节“塞回去”。
	pc := &PeekedConn{
		Conn: conn,
		buf:  append([]byte(nil), buf...),
	}
	return result, pc, nil
}

// PeekedConn 是一个包装连接。
// 它会先把内部 buf 中的数据读完，再继续读底层 Conn。
type PeekedConn struct {
	net.Conn
	buf []byte
}

// Read 先返回已经探测读出的字节，再继续读底层连接。
func (c *PeekedConn) Read(p []byte) (int, error) {
	if len(c.buf) > 0 {
		n := copy(p, c.buf)
		c.buf = c.buf[n:]
		return n, nil
	}
	return c.Conn.Read(p)
}

// looksLikeHTTPRequest 判断是否像 HTTP 明文请求首行。
func looksLikeHTTPRequest(b []byte) bool {
	if len(b) < 4 {
		return false
	}

	for _, m := range httpMethods {
		if bytes.HasPrefix(b, m) {
			return true
		}
	}

	return false
}

// looksLikeTLSClientHello 判断是否像 TLS ClientHello。
// 这里只做“外层 HTTPS”探测，不深入解析 TLS 扩展。
func looksLikeTLSClientHello(b []byte) bool {
	// TLS Record Header 至少 5 字节：
	// byte 0: ContentType
	// byte 1: Version major
	// byte 2: Version minor
	// byte 3-4: Length
	if len(b) < 5 {
		return false
	}

	// 0x16 = Handshake
	if b[0] != 0x16 {
		return false
	}

	// TLS major version 一般为 0x03
	if b[1] != 0x03 {
		return false
	}

	// 次版本常见：
	// 0x00 = SSL 3.0
	// 0x01 = TLS 1.0
	// 0x02 = TLS 1.1
	// 0x03 = TLS 1.2
	// 0x04 = TLS 1.3（record layer 仍常见 0x03 0x03，但这里放宽一点）
	if b[2] > 0x04 {
		return false
	}

	// 记录长度做一个基础校验，避免过于宽松误判。
	recordLen := int(b[3])<<8 | int(b[4])
	if recordLen <= 0 || recordLen > 16384+2048 {
		return false
	}

	// 如果拿到了第 6 个字节，再进一步校验 handshake type。
	// 0x01 = ClientHello
	if len(b) >= 6 && b[5] != 0x01 {
		return false
	}

	return true
}