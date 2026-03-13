package grpcbinding

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	transportgen "github.com/lifei6671/devbridge-loop/ltfp/pb/gen/devbridge/loop/v2/transport"
	"github.com/lifei6671/devbridge-loop/ltfp/runtime"
	"github.com/lifei6671/devbridge-loop/ltfp/transport"
	"github.com/lifei6671/devbridge-loop/ltfp/transport/tcpbinding"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

const (
	// parityTestTimeout 控制 parity/integration 场景的总超时。
	parityTestTimeout = 3 * time.Second
)

// parityScenarioMode 描述服务端的流量处理模式。
type parityScenarioMode string

const (
	// parityScenarioClose 表示服务端完成 echo 后回写 close。
	parityScenarioClose parityScenarioMode = "close"
	// parityScenarioReset 表示服务端读取数据后回写 reset。
	parityScenarioReset parityScenarioMode = "reset"
)

// parityScenarioResult 描述单次场景的关键观测结果。
type parityScenarioResult struct {
	OpenAckCode    string
	OpenAckMessage string
	EchoPayload    []byte
	ReachedEOF     bool
	ResetCode      string
	ResetMessage   string
}

// parityTunnelService 是用于 bufconn 场景的最小 gRPC TunnelStream 服务端。
type parityTunnelService struct {
	transportgen.UnimplementedGRPCH2TransportServiceServer

	mode              parityScenarioMode
	expectedPayloadSz int
	scenarioDone      chan error
}

// ControlChannel 在 parity 测试中不使用，保留最小实现即可。
func (service *parityTunnelService) ControlChannel(
	stream grpc.BidiStreamingServer[transportgen.ControlFrameEnvelope, transportgen.ControlFrameEnvelope],
) error {
	// 该测试只覆盖数据面 parity，控制面流不应该被调用。
	return fmt.Errorf("parity control channel is not used")
}

// TunnelStream 处理一条 grpc_h2 tunnel，并执行统一的服务端 traffic 流程。
func (service *parityTunnelService) TunnelStream(
	stream grpc.BidiStreamingServer[transportgen.TunnelEnvelope, transportgen.TunnelEnvelope],
) error {
	// 复用 binding 内部适配器，确保测试路径与生产路径一致。
	tunnelStream, err := newGRPCH2TunnelStreamWithOptions(
		&serverTunnelStreamAdapter{stream: stream},
		nil,
		tunnelStreamAdapterOptions{enableReadPayloadFastPath: true},
	)
	if err != nil {
		return fmt.Errorf("create grpc parity tunnel stream: %w", err)
	}
	// 使用标准 GRPCH2Tunnel，把 runtime 协议跑在真实 tunnel 抽象上。
	serverTunnel, err := NewGRPCH2Tunnel(tunnelStream, transport.TunnelMeta{
		TunnelID: "grpc-parity-server-tunnel",
	})
	if err != nil {
		return fmt.Errorf("create grpc parity tunnel: %w", err)
	}
	defer func() {
		// 测试结束时兜底关闭，避免流残留。
		_ = serverTunnel.Close()
	}()
	scenarioErr := runParityServerScenario(serverTunnel, service.mode, service.expectedPayloadSz)
	// 把服务端流程结果回传给测试主线程，避免仅靠 Serve 退出判断误报超时。
	select {
	case service.scenarioDone <- scenarioErr:
	default:
	}
	return scenarioErr
}

// TestBindingParityOpenEchoClose 验证同一组 open/ack/data/close 场景在两种 binding 下结果一致。
func TestBindingParityOpenEchoClose(testingObject *testing.T) {
	testPayload := bytes.Repeat([]byte("payload-"), 32)
	grpcResult := runGRPCH2ParityScenario(testingObject, parityScenarioClose, testPayload)
	tcpResult := runTCPParityScenario(testingObject, parityScenarioClose, testPayload)

	// 对齐 openAck 语义，确保两种 binding 没有行为分叉。
	if grpcResult.OpenAckCode != tcpResult.OpenAckCode || grpcResult.OpenAckMessage != tcpResult.OpenAckMessage {
		testingObject.Fatalf("openAck mismatch: grpc=%+v tcp=%+v", grpcResult, tcpResult)
	}
	// 对齐数据回环结果，确保 payload 与顺序一致。
	if !bytes.Equal(grpcResult.EchoPayload, tcpResult.EchoPayload) {
		testingObject.Fatalf("echo payload mismatch: grpc_len=%d tcp_len=%d", len(grpcResult.EchoPayload), len(tcpResult.EchoPayload))
	}
	// close 收敛后两边都应得到 EOF。
	if !grpcResult.ReachedEOF || !tcpResult.ReachedEOF {
		testingObject.Fatalf("expected EOF for both bindings: grpc=%v tcp=%v", grpcResult.ReachedEOF, tcpResult.ReachedEOF)
	}
}

// TestBindingParityOpenDataReset 验证同一组 open/ack/data/reset 场景在两种 binding 下结果一致。
func TestBindingParityOpenDataReset(testingObject *testing.T) {
	testPayload := bytes.Repeat([]byte("reset-case-"), 24)
	grpcResult := runGRPCH2ParityScenario(testingObject, parityScenarioReset, testPayload)
	tcpResult := runTCPParityScenario(testingObject, parityScenarioReset, testPayload)

	// reset 码与 message 应保持一致，避免 binding 特判。
	if grpcResult.ResetCode != tcpResult.ResetCode || grpcResult.ResetMessage != tcpResult.ResetMessage {
		testingObject.Fatalf("reset mismatch: grpc=%+v tcp=%+v", grpcResult, tcpResult)
	}
}

// runGRPCH2ParityScenario 在 bufconn gRPC 环境执行统一 parity 场景。
func runGRPCH2ParityScenario(
	testingObject *testing.T,
	mode parityScenarioMode,
	payload []byte,
) parityScenarioResult {
	testingObject.Helper()
	testContext, cancelTest := context.WithTimeout(context.Background(), parityTestTimeout)
	defer cancelTest()

	// 使用 bufconn 避免真实网络依赖，同时保持 gRPC 协议栈完整。
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	scenarioDone := make(chan error, 1)
	transportgen.RegisterGRPCH2TransportServiceServer(server, &parityTunnelService{
		mode:              mode,
		expectedPayloadSz: len(payload),
		scenarioDone:      scenarioDone,
	})
	go func() {
		// 服务生命周期由测试函数通过 defer Stop 控制，这里忽略退出错误。
		_ = server.Serve(listener)
	}()
	defer func() {
		// 停止服务端并关闭 listener，避免 goroutine 泄漏。
		server.Stop()
		_ = listener.Close()
	}()

	clientConnection, err := grpc.DialContext(
		testContext,
		"bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, address string) (net.Conn, error) {
			// bufconn 使用内存连接，address 参数在测试中不参与路由。
			return listener.Dial()
		}),
	)
	if err != nil {
		testingObject.Fatalf("dial grpc parity server failed: %v", err)
	}
	defer func() {
		_ = clientConnection.Close()
	}()

	transportBinding := NewTransport()
	client := transportgen.NewGRPCH2TransportServiceClient(clientConnection)
	tunnelStream, err := transportBinding.OpenTunnelStream(testContext, client)
	if err != nil {
		testingObject.Fatalf("open grpc tunnel stream failed: %v", err)
	}
	clientTunnel, err := NewGRPCH2Tunnel(tunnelStream, transport.TunnelMeta{
		TunnelID: "grpc-parity-client-tunnel",
	})
	if err != nil {
		testingObject.Fatalf("create grpc parity client tunnel failed: %v", err)
	}
	defer func() {
		// 客户端侧也要主动 close，保证双端都能收敛。
		_ = clientTunnel.Close()
	}()

	result := runParityClientScenario(testingObject, clientTunnel, mode, payload)
	select {
	case serverErr := <-scenarioDone:
		if serverErr != nil {
			testingObject.Fatalf("grpc parity server failed: %v", serverErr)
		}
	case <-time.After(parityTestTimeout):
		testingObject.Fatalf("grpc parity server scenario did not finish in time")
	}
	return result
}

// runTCPParityScenario 在真实 TCP listener 上执行统一 parity 场景。
func runTCPParityScenario(
	testingObject *testing.T,
	mode parityScenarioMode,
	payload []byte,
) parityScenarioResult {
	testingObject.Helper()
	testContext, cancelTest := context.WithTimeout(context.Background(), parityTestTimeout)
	defer cancelTest()

	tcpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		testingObject.Fatalf("listen tcp parity server failed: %v", err)
	}
	defer func() {
		_ = tcpListener.Close()
	}()

	transportBinding := tcpbinding.NewTransport()
	serverErrorChannel := make(chan error, 1)
	go func() {
		serverTunnel, acceptErr := transportBinding.AcceptTunnel(testContext, tcpListener, transport.TunnelMeta{
			TunnelID: "tcp-parity-server-tunnel",
		})
		if acceptErr != nil {
			serverErrorChannel <- acceptErr
			return
		}
		defer func() {
			// 服务端 tunnel 处理完成后统一关闭连接。
			_ = serverTunnel.Close()
		}()
		serverErrorChannel <- runParityServerScenario(serverTunnel, mode, len(payload))
	}()

	clientTunnel, err := transportBinding.DialTunnel(testContext, tcpListener.Addr().String(), transport.TunnelMeta{
		TunnelID: "tcp-parity-client-tunnel",
	})
	if err != nil {
		testingObject.Fatalf("dial tcp parity tunnel failed: %v", err)
	}
	defer func() {
		// 客户端收尾时关闭 tunnel，避免 fd 泄漏。
		_ = clientTunnel.Close()
	}()

	result := runParityClientScenario(testingObject, clientTunnel, mode, payload)
	select {
	case serverErr := <-serverErrorChannel:
		if serverErr != nil {
			testingObject.Fatalf("tcp parity server failed: %v", serverErr)
		}
	case <-time.After(parityTestTimeout):
		testingObject.Fatalf("tcp parity server did not stop in time")
	}
	return result
}

// runParityClientScenario 执行客户端侧 open/ack/data/close|reset 流程。
func runParityClientScenario(
	testingObject *testing.T,
	tunnel transport.Tunnel,
	mode parityScenarioMode,
	payload []byte,
) parityScenarioResult {
	testingObject.Helper()
	// 给测试流量操作设置统一 deadline，避免单测被意外挂死。
	if err := tunnel.SetDeadline(time.Now().Add(parityTestTimeout)); err != nil {
		testingObject.Fatalf("set parity tunnel deadline failed: %v", err)
	}
	protocol := runtime.NewLengthPrefixedTrafficProtocol()
	trafficMeta := runtime.TrafficMeta{
		TrafficID: "parity-traffic-id",
		ServiceID: "svc-parity",
	}
	if err := protocol.WriteFrame(tunnel, runtime.TrafficFrame{
		Type: runtime.TrafficFrameOpen,
		Open: &trafficMeta,
	}); err != nil {
		testingObject.Fatalf("write open frame failed: %v", err)
	}
	openAckFrame, err := protocol.ReadFrame(tunnel)
	if err != nil {
		testingObject.Fatalf("read openAck frame failed: %v", err)
	}
	if openAckFrame.Type != runtime.TrafficFrameOpenAck || openAckFrame.OpenAck == nil {
		testingObject.Fatalf("unexpected openAck frame: %+v", openAckFrame)
	}
	dataStream, err := runtime.NewTrafficDataStreamWithConfig(tunnel, protocol, runtime.TrafficDataStreamConfig{
		MaxDataFrameSize: 32,
	})
	if err != nil {
		testingObject.Fatalf("create traffic data stream failed: %v", err)
	}
	if writtenSize, writeErr := dataStream.Write(payload); writeErr != nil {
		testingObject.Fatalf("write parity payload failed: %v", writeErr)
	} else if writtenSize != len(payload) {
		testingObject.Fatalf("unexpected parity write size: %d", writtenSize)
	}

	result := parityScenarioResult{
		OpenAckCode:    openAckFrame.OpenAck.Code,
		OpenAckMessage: openAckFrame.OpenAck.Message,
	}
	switch mode {
	case parityScenarioClose:
		// close 模式下应先读到完整 echo，再收到 EOF。
		echoPayload, readErr := readFixedSizeFromStream(dataStream, len(payload))
		if readErr != nil {
			testingObject.Fatalf("read echoed payload failed: %v", readErr)
		}
		result.EchoPayload = echoPayload
		if err := protocol.WriteFrame(tunnel, runtime.TrafficFrame{
			Type:  runtime.TrafficFrameClose,
			Close: &runtime.TrafficClose{Code: "CLIENT_DONE", Message: "done"},
		}); err != nil {
			testingObject.Fatalf("write close frame failed: %v", err)
		}
		readBuffer := make([]byte, 16)
		readSize, readErr := dataStream.Read(readBuffer)
		if !errors.Is(readErr, io.EOF) || readSize != 0 {
			testingObject.Fatalf("expected EOF after close, size=%d err=%v", readSize, readErr)
		}
		result.ReachedEOF = true
	case parityScenarioReset:
		// reset 模式下 read 应返回显式 reset 错误。
		readBuffer := make([]byte, 32)
		readSize, readErr := dataStream.Read(readBuffer)
		if readSize != 0 {
			testingObject.Fatalf("expected zero bytes before reset, got %d", readSize)
		}
		if !errors.Is(readErr, runtime.ErrTrafficReset) {
			testingObject.Fatalf("expected ErrTrafficReset, got %v", readErr)
		}
		var resetError *runtime.TrafficResetError
		if !errors.As(readErr, &resetError) {
			testingObject.Fatalf("expected TrafficResetError, got %T", readErr)
		}
		result.ResetCode = resetError.Code
		result.ResetMessage = resetError.Message
	default:
		testingObject.Fatalf("unsupported parity mode: %s", mode)
	}
	return result
}

// runParityServerScenario 执行服务端统一流程，并按 mode 回写 close 或 reset。
func runParityServerScenario(tunnel transport.Tunnel, mode parityScenarioMode, expectedPayloadSz int) error {
	// 服务端也设置 deadline，避免单测异常路径下无限等待。
	if err := tunnel.SetDeadline(time.Now().Add(parityTestTimeout)); err != nil {
		return fmt.Errorf("set parity server deadline: %w", err)
	}
	protocol := runtime.NewLengthPrefixedTrafficProtocol()
	openFrame, err := protocol.ReadFrame(tunnel)
	if err != nil {
		return fmt.Errorf("read open frame: %w", err)
	}
	if openFrame.Type != runtime.TrafficFrameOpen || openFrame.Open == nil {
		return fmt.Errorf("unexpected open frame: %+v", openFrame)
	}
	if err := protocol.WriteFrame(tunnel, runtime.TrafficFrame{
		Type: runtime.TrafficFrameOpenAck,
		OpenAck: &runtime.TrafficOpenAck{
			Accepted: true,
			Code:     "OK",
			Message:  "accepted",
		},
	}); err != nil {
		return fmt.Errorf("write openAck frame: %w", err)
	}
	dataStream, err := runtime.NewTrafficDataStreamWithConfig(tunnel, protocol, runtime.TrafficDataStreamConfig{
		MaxDataFrameSize: 32,
	})
	if err != nil {
		return fmt.Errorf("new traffic data stream: %w", err)
	}

	// 先把客户端发来的 Data 流读完，再根据模式返回 close/reset。
	receivedPayload, err := readFixedSizeFromStream(dataStream, expectedPayloadSz)
	if err != nil {
		return fmt.Errorf("read parity payload: %w", err)
	}
	switch mode {
	case parityScenarioClose:
		// close 模式：先回写 echo，再消费 close，并给出 close 回包。
		if _, err := dataStream.Write(receivedPayload); err != nil {
			return fmt.Errorf("write echoed payload: %w", err)
		}
		closeFrame, err := protocol.ReadFrame(tunnel)
		if err != nil {
			return fmt.Errorf("read close frame: %w", err)
		}
		if closeFrame.Type != runtime.TrafficFrameClose {
			return fmt.Errorf("unexpected frame before close ack: %+v", closeFrame)
		}
		if err := protocol.WriteFrame(tunnel, runtime.TrafficFrame{
			Type:  runtime.TrafficFrameClose,
			Close: &runtime.TrafficClose{Code: "SERVER_DONE", Message: "done"},
		}); err != nil {
			return fmt.Errorf("write close frame: %w", err)
		}
	case parityScenarioReset:
		// reset 模式：直接返回 reset，让客户端路径收敛到显式错误。
		if err := protocol.WriteFrame(tunnel, runtime.TrafficFrame{
			Type: runtime.TrafficFrameReset,
			Reset: &runtime.TrafficReset{
				Code:    "UPSTREAM_BROKEN",
				Message: "upstream reset",
			},
		}); err != nil {
			return fmt.Errorf("write reset frame: %w", err)
		}
	default:
		return fmt.Errorf("unsupported parity mode: %s", mode)
	}
	return nil
}

// readFixedSizeFromStream 从 stream 中读取固定长度 payload。
func readFixedSizeFromStream(reader io.Reader, expectedSize int) ([]byte, error) {
	// 统一按固定长度读取，确保跨 binding 的分片行为不会影响测试断言。
	payload := make([]byte, expectedSize)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, err
	}
	return payload, nil
}
