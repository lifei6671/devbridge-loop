package tcpbinding

import (
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// newTCPTestListener 创建绑定到本地回环地址的测试 listener。
func newTCPTestListener() (*net.TCPListener, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	tcpListener, ok := listener.(*net.TCPListener)
	if !ok {
		_ = listener.Close()
		return nil, fmt.Errorf("listener is not tcp listener")
	}
	return tcpListener, nil
}

// mustCreateTCPConnPair 创建一对互联的真实 TCPConn。
func mustCreateTCPConnPair(testingObject *testing.T) (*net.TCPConn, *net.TCPConn, func()) {
	testingObject.Helper()
	listener, err := newTCPTestListener()
	if err != nil {
		testingObject.Fatalf("create test listener failed: %v", err)
	}

	serverConnChannel := make(chan *net.TCPConn, 1)
	serverErrChannel := make(chan error, 1)
	go func() {
		conn, acceptErr := listener.AcceptTCP()
		if acceptErr != nil {
			serverErrChannel <- acceptErr
			return
		}
		serverConnChannel <- conn
	}()

	clientConn, err := net.DialTCP("tcp", nil, listener.Addr().(*net.TCPAddr))
	if err != nil {
		_ = listener.Close()
		testingObject.Fatalf("dial test tcp conn failed: %v", err)
	}

	var serverConn *net.TCPConn
	select {
	case serverConn = <-serverConnChannel:
	case acceptErr := <-serverErrChannel:
		_ = clientConn.Close()
		_ = listener.Close()
		testingObject.Fatalf("accept test tcp conn failed: %v", acceptErr)
	}

	cleanup := func() {
		_ = clientConn.Close()
		_ = serverConn.Close()
		_ = listener.Close()
	}
	return clientConn, serverConn, cleanup
}

// testingErrorf 生成带格式化内容的 error，便于 goroutine 回传测试失败原因。
func testingErrorf(format string, arguments ...any) error {
	return fmt.Errorf(format, arguments...)
}

type mockAddr string

func (address mockAddr) Network() string {
	return "mock"
}

func (address mockAddr) String() string {
	return string(address)
}

type mockConn struct {
	mutex sync.Mutex

	readChunks [][]byte
	readError  error

	recordedWrites      [][]byte
	writeReadyChannel   chan struct{}
	writeReleaseChannel chan struct{}

	closeCount   int
	closed       bool
	closedSignal chan struct{}
}

func newMockConn() *mockConn {
	return &mockConn{
		readChunks:     make([][]byte, 0),
		recordedWrites: make([][]byte, 0),
		closedSignal:   make(chan struct{}),
	}
}

func (conn *mockConn) Read(payload []byte) (int, error) {
	conn.mutex.Lock()
	defer conn.mutex.Unlock()
	if conn.closed {
		return 0, net.ErrClosed
	}
	if len(conn.readChunks) > 0 {
		chunk := conn.readChunks[0]
		readSize := copy(payload, chunk)
		if readSize == len(chunk) {
			conn.readChunks = conn.readChunks[1:]
		} else {
			conn.readChunks[0] = append([]byte(nil), chunk[readSize:]...)
		}
		return readSize, nil
	}
	if conn.readError != nil {
		return 0, conn.readError
	}
	return 0, io.EOF
}

func (conn *mockConn) Write(payload []byte) (int, error) {
	conn.mutex.Lock()
	if conn.closed {
		conn.mutex.Unlock()
		return 0, net.ErrClosed
	}
	conn.recordedWrites = append(conn.recordedWrites, append([]byte(nil), payload...))
	writeReadyChannel := conn.writeReadyChannel
	writeReleaseChannel := conn.writeReleaseChannel
	closedSignal := conn.closedSignal
	conn.mutex.Unlock()

	if writeReadyChannel != nil {
		select {
		case writeReadyChannel <- struct{}{}:
		default:
		}
	}
	if writeReleaseChannel != nil {
		select {
		case <-writeReleaseChannel:
		case <-closedSignal:
			return 0, net.ErrClosed
		}
	}
	conn.mutex.Lock()
	defer conn.mutex.Unlock()
	if conn.closed {
		return 0, net.ErrClosed
	}
	return len(payload), nil
}

func (conn *mockConn) Close() error {
	conn.mutex.Lock()
	defer conn.mutex.Unlock()
	conn.closeCount++
	if conn.closed {
		return nil
	}
	conn.closed = true
	close(conn.closedSignal)
	return nil
}

func (conn *mockConn) LocalAddr() net.Addr {
	return mockAddr("local")
}

func (conn *mockConn) RemoteAddr() net.Addr {
	return mockAddr("remote")
}

func (conn *mockConn) SetDeadline(time.Time) error {
	return nil
}

func (conn *mockConn) SetReadDeadline(time.Time) error {
	return nil
}

func (conn *mockConn) SetWriteDeadline(time.Time) error {
	return nil
}

func (conn *mockConn) EnqueueReadChunk(payload []byte) {
	conn.mutex.Lock()
	defer conn.mutex.Unlock()
	conn.readChunks = append(conn.readChunks, append([]byte(nil), payload...))
}

func (conn *mockConn) SetReadError(err error) {
	conn.mutex.Lock()
	defer conn.mutex.Unlock()
	conn.readError = err
}

func (conn *mockConn) Writes() [][]byte {
	conn.mutex.Lock()
	defer conn.mutex.Unlock()
	writes := make([][]byte, len(conn.recordedWrites))
	for index, write := range conn.recordedWrites {
		writes[index] = append([]byte(nil), write...)
	}
	return writes
}

func (conn *mockConn) CloseCount() int {
	conn.mutex.Lock()
	defer conn.mutex.Unlock()
	return conn.closeCount
}
