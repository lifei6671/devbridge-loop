package tcpbinding

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/transport"
)

const (
	controlFrameHeaderSize = 6
	tunnelFrameHeaderSize  = 4
)

// framedReadState 维护一条 framed TCP 连接的半包读取状态。
type framedReadState struct {
	header      []byte
	headerRead  int
	payload     []byte
	payloadRead int
	frameType   uint16
}

// newFramedReadState 创建指定头部大小的读取状态。
func newFramedReadState(headerSize int) framedReadState {
	return framedReadState{
		header: make([]byte, headerSize),
	}
}

// reset 在一帧读取完成后清理中间状态。
func (state *framedReadState) reset(headerSize int) {
	if state == nil {
		return
	}
	if cap(state.header) < headerSize {
		state.header = make([]byte, headerSize)
	} else {
		state.header = state.header[:headerSize]
		clear(state.header)
	}
	state.headerRead = 0
	state.payload = nil
	state.payloadRead = 0
	state.frameType = 0
}

// readControlFrameFromConn 从 TCP 连接读取一条控制面帧。
func readControlFrameFromConn(
	conn net.Conn,
	state *framedReadState,
	maxPayloadSize int,
) (transport.ControlFrame, error) {
	frameType, payload, err := readFrameFromConn(
		conn,
		state,
		controlFrameHeaderSize,
		maxPayloadSize,
		func(header []byte) (uint16, int, error) {
			return binary.BigEndian.Uint16(header[0:2]), int(binary.BigEndian.Uint32(header[2:6])), nil
		},
	)
	if err != nil {
		return transport.ControlFrame{}, err
	}
	return transport.ControlFrame{
		Type:    frameType,
		Payload: payload,
	}, nil
}

// readTunnelPayloadFromConn 从 TCP 连接读取一条数据面 payload 帧。
func readTunnelPayloadFromConn(conn net.Conn, state *framedReadState, maxPayloadSize int) ([]byte, error) {
	_, payload, err := readFrameFromConn(
		conn,
		state,
		tunnelFrameHeaderSize,
		maxPayloadSize,
		func(header []byte) (uint16, int, error) {
			return 0, int(binary.BigEndian.Uint32(header[0:4])), nil
		},
	)
	return payload, err
}

// readFrameFromConn 按指定头部格式从 framed TCP 连接读取一条完整帧。
func readFrameFromConn(
	conn net.Conn,
	state *framedReadState,
	headerSize int,
	maxPayloadSize int,
	decodeHeader func([]byte) (uint16, int, error),
) (uint16, []byte, error) {
	if conn == nil {
		return 0, nil, fmt.Errorf("read tcp frame: %w: nil conn", transport.ErrInvalidArgument)
	}
	if state == nil {
		return 0, nil, fmt.Errorf("read tcp frame: %w: nil state", transport.ErrInvalidArgument)
	}
	if decodeHeader == nil {
		return 0, nil, fmt.Errorf("read tcp frame: %w: nil decoder", transport.ErrInvalidArgument)
	}
	if state.header == nil || len(state.header) != headerSize {
		// 初始化或修复头部缓冲区，确保后续读取按固定格式解析。
		state.reset(headerSize)
	}

	for state.headerRead < headerSize {
		readSize, err := conn.Read(state.header[state.headerRead:])
		if readSize > 0 {
			// 半包头部需要累积保存，方便下一次继续读取。
			state.headerRead += readSize
		}
		if err != nil {
			return classifyReadError(err, state.headerRead == 0 && state.payload == nil)
		}
	}
	if state.payload == nil {
		frameType, payloadSize, err := decodeHeader(state.header)
		if err != nil {
			return 0, nil, err
		}
		if payloadSize < 0 {
			return 0, nil, fmt.Errorf("read tcp frame: %w: negative payload size", transport.ErrInvalidArgument)
		}
		if maxPayloadSize > 0 && payloadSize > maxPayloadSize {
			return 0, nil, fmt.Errorf(
				"read tcp frame: %w: payload_size=%d max=%d",
				transport.ErrInvalidArgument,
				payloadSize,
				maxPayloadSize,
			)
		}
		state.frameType = frameType
		state.payload = make([]byte, payloadSize)
		if payloadSize == 0 {
			// 零长度帧允许存在，但仍然保留 frame 边界语义。
			state.reset(headerSize)
			return frameType, []byte{}, nil
		}
	}
	for state.payloadRead < len(state.payload) {
		readSize, err := conn.Read(state.payload[state.payloadRead:])
		if readSize > 0 {
			// 载荷半包也需要缓存，避免 deadline 超时后丢失已读字节。
			state.payloadRead += readSize
		}
		if err != nil {
			return classifyReadError(err, false)
		}
	}
	frameType := state.frameType
	payload := state.payload
	state.reset(headerSize)
	return frameType, payload, nil
}

// writeControlFrameToConn 将一条控制面帧写入 TCP 连接。
func writeControlFrameToConn(conn net.Conn, maxPayloadSize int, frame transport.ControlFrame) (bool, error) {
	return writeFrameToConn(
		conn,
		maxPayloadSize,
		frame.Payload,
		func(payloadSize int) []byte {
			header := make([]byte, controlFrameHeaderSize)
			binary.BigEndian.PutUint16(header[0:2], frame.Type)
			binary.BigEndian.PutUint32(header[2:6], uint32(payloadSize))
			return header
		},
	)
}

// writeTunnelPayloadToConn 将一条数据面 payload 帧写入 TCP 连接。
func writeTunnelPayloadToConn(conn net.Conn, maxPayloadSize int, payload []byte) (bool, error) {
	return writeFrameToConn(
		conn,
		maxPayloadSize,
		payload,
		func(payloadSize int) []byte {
			header := make([]byte, tunnelFrameHeaderSize)
			binary.BigEndian.PutUint32(header[0:4], uint32(payloadSize))
			return header
		},
	)
}

// writeFrameToConn 按长度前缀格式把一条完整帧写入 TCP 连接。
func writeFrameToConn(
	conn net.Conn,
	maxPayloadSize int,
	payload []byte,
	buildHeader func(int) []byte,
) (bool, error) {
	if conn == nil {
		return false, fmt.Errorf("write tcp frame: %w: nil conn", transport.ErrInvalidArgument)
	}
	if buildHeader == nil {
		return false, fmt.Errorf("write tcp frame: %w: nil header builder", transport.ErrInvalidArgument)
	}
	if maxPayloadSize > 0 && len(payload) > maxPayloadSize {
		return false, fmt.Errorf(
			"write tcp frame: %w: payload_size=%d max=%d",
			transport.ErrInvalidArgument,
			len(payload),
			maxPayloadSize,
		)
	}
	header := buildHeader(len(payload))
	framePayload := make([]byte, 0, len(header)+len(payload))
	framePayload = append(framePayload, header...)
	framePayload = append(framePayload, payload...)

	writtenSize := 0
	for writtenSize < len(framePayload) {
		currentWrittenSize, err := conn.Write(framePayload[writtenSize:])
		if currentWrittenSize > 0 {
			// 连续写直到整帧完成，避免把一条帧拆成多次上层 Write 结果。
			writtenSize += currentWrittenSize
		}
		if err != nil {
			return writtenSize > 0, classifyWriteError(err)
		}
		if currentWrittenSize == 0 {
			return writtenSize > 0, io.ErrUnexpectedEOF
		}
	}
	return false, nil
}

// classifyReadError 按 framed TCP 语义归类读取错误。
func classifyReadError(err error, frameBoundary bool) (uint16, []byte, error) {
	if err == nil {
		return 0, nil, nil
	}
	if isTimeoutError(err) {
		// 读超时不破坏已缓存半包，允许调用方重试。
		return 0, nil, transport.ErrTimeout
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) || errors.Is(err, net.ErrClosed) {
		if frameBoundary {
			// 正好在帧边界关闭时，按正常关闭处理。
			return 0, nil, transport.ErrClosed
		}
		// 半帧 EOF 说明对端异常断流，应视为损坏连接。
		return 0, nil, io.ErrUnexpectedEOF
	}
	return 0, nil, err
}

// classifyWriteError 按 framed TCP 语义归类写入错误。
func classifyWriteError(err error) error {
	if err == nil {
		return nil
	}
	if isTimeoutError(err) {
		// 写超时由上层决定是重试还是直接判定连接损坏。
		return transport.ErrTimeout
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) || errors.Is(err, net.ErrClosed) {
		return transport.ErrClosed
	}
	return err
}

// isTimeoutError 判断错误是否属于 deadline/timeout。
func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	networkErr, ok := err.(net.Error)
	return ok && networkErr.Timeout()
}

// setConnReadDeadlineTemporarily 临时设置连接读 deadline，并返回恢复函数。
func setConnReadDeadlineTemporarily(conn net.Conn, deadline time.Time) (func(), error) {
	if conn == nil || deadline.IsZero() {
		return func() {}, nil
	}
	if err := conn.SetReadDeadline(deadline); err != nil {
		return nil, err
	}
	return func() {
		// 控制面每次操作结束后清理临时 deadline，避免影响后续请求。
		_ = conn.SetReadDeadline(time.Time{})
	}, nil
}

// setConnWriteDeadlineTemporarily 临时设置连接写 deadline，并返回恢复函数。
func setConnWriteDeadlineTemporarily(conn net.Conn, deadline time.Time) (func(), error) {
	if conn == nil || deadline.IsZero() {
		return func() {}, nil
	}
	if err := conn.SetWriteDeadline(deadline); err != nil {
		return nil, err
	}
	return func() {
		// 控制面每次操作结束后清理临时 deadline，避免污染后续写操作。
		_ = conn.SetWriteDeadline(time.Time{})
	}, nil
}
