package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	appauth "github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/auth"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/connectorproxy"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/ingress"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/routing"
	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

// newIngressGRPCServer 创建 Bridge gRPC ingress 监听器（h2c）。
func newIngressGRPCServer(runtime *Runtime, listenAddr string) *http.Server {
	normalizedListenAddr := strings.TrimSpace(listenAddr)
	if normalizedListenAddr == "" {
		return nil
	}
	return &http.Server{
		Addr: normalizedListenAddr,
		Handler: h2c.NewHandler(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			runtime.handleGRPCIngress(writer, request)
		}), &http2.Server{}),
	}
}

// handleGRPCIngress 执行 gRPC ingress 的 route resolve 与转发链路。
func (runtime *Runtime) handleGRPCIngress(writer http.ResponseWriter, request *http.Request) {
	if runtime == nil {
		writeIngressError(writer, http.StatusServiceUnavailable, "RUNTIME_UNAVAILABLE", "bridge runtime unavailable", "", "")
		return
	}
	if request == nil {
		writeIngressError(writer, http.StatusBadRequest, "INVALID_REQUEST", "request is required", "", "")
		return
	}
	trafficID := buildIngressTrafficID(time.Now().UTC())
	traceID := strings.TrimSpace(request.Header.Get("X-Request-Id"))
	if traceID == "" {
		traceID = trafficID
	}
	writer.Header().Set("X-DevBridge-Traffic-Id", trafficID)
	writer.Header().Set("X-DevBridge-Trace-Id", traceID)
	if !isGRPCContentType(request.Header.Get("Content-Type")) {
		writeIngressError(
			writer,
			http.StatusUnsupportedMediaType,
			ltfperrors.CodeUnsupportedValue,
			"grpc ingress requires content-type application/grpc",
			trafficID,
			traceID,
		)
		return
	}
	if runtime.dataPlane == nil || runtime.dataPlane.grpcGateway == nil || runtime.dataPlane.resolver == nil {
		writeIngressError(writer, http.StatusServiceUnavailable, "RUNTIME_DATAPLANE_UNAVAILABLE", "bridge runtime data plane unavailable", trafficID, traceID)
		return
	}
	requestScope, resolveScopeErr := resolveIngressScope(request.Header, runtime.cfg.DefaultScope)
	if resolveScopeErr != nil {
		errorCode := ltfperrors.ExtractCode(resolveScopeErr)
		if strings.TrimSpace(errorCode) == "" {
			errorCode = ltfperrors.CodeInvalidPayload
		}
		writeIngressError(writer, http.StatusBadRequest, errorCode, resolveScopeErr.Error(), trafficID, traceID)
		log.Printf(
			"bridge ingress grpc scope resolve failed path=%s code=%s traffic_id=%s trace_id=%s err=%v",
			request.URL.Path,
			errorCode,
			trafficID,
			traceID,
			resolveScopeErr,
		)
		return
	}
	authority := resolveIngressAuthority(request)

	lookupRequest, lookupErr := runtime.dataPlane.grpcGateway.BuildRouteLookupRequest(ingress.GRPCGatewayRequest{
		Authority:   authority,
		Path:        request.URL.Path,
		Namespace:   requestScope.Namespace,
		Environment: requestScope.Environment,
		Metadata: map[string]string{
			routing.RouteLookupMetadataTrafficIDKey: trafficID,
			routing.RouteLookupMetadataClientIPKey:  appauth.NormalizeSourceIP(request.RemoteAddr),
			"grpc_path":                             request.URL.Path,
		},
		Headers: cloneHTTPHeaderValues(request.Header),
		Queries: cloneURLQueryValues(request.URL.Query()),
	})
	if lookupErr != nil {
		mappedFailure := ingressHTTPFailureMapper.Map(lookupErr, routing.PathExecuteResult{})
		writeIngressError(writer, mappedFailure.HTTPStatus, mappedFailure.Code, mappedFailure.Message, trafficID, traceID)
		log.Printf(
			"bridge ingress grpc lookup failed authority=%s path=%s http=%d code=%s traffic_id=%s trace_id=%s err=%v",
			authority,
			request.URL.Path,
			mappedFailure.HTTPStatus,
			mappedFailure.Code,
			trafficID,
			traceID,
			lookupErr,
		)
		return
	}
	resolution, resolveErr := runtime.dataPlane.resolver.Resolve(lookupRequest)
	if resolveErr != nil {
		mappedFailure := ingressHTTPFailureMapper.Map(resolveErr, routing.PathExecuteResult{})
		writeIngressError(writer, mappedFailure.HTTPStatus, mappedFailure.Code, mappedFailure.Message, trafficID, traceID)
		log.Printf(
			"bridge ingress grpc resolve failed authority=%s path=%s http=%d code=%s traffic_id=%s trace_id=%s err=%v",
			authority,
			request.URL.Path,
			mappedFailure.HTTPStatus,
			mappedFailure.Code,
			trafficID,
			traceID,
			resolveErr,
		)
		return
	}
	resolvedLogicalServiceID := resolveTrafficServiceIDFromResolution(resolution)
	resolvedServiceKey := resolveTrafficServiceKeyFromResolution(resolution)
	resolvedServiceInstanceID := resolveTrafficServiceInstanceIDFromResolution(resolution)
	trafficOpen := pb.TrafficOpen{
		TrafficID:        trafficID,
		RouteID:          strings.TrimSpace(resolution.Route.RouteID),
		LogicalServiceID: resolvedLogicalServiceID,
		InstanceID:       resolvedServiceInstanceID,
		SourceAddr:       strings.TrimSpace(request.RemoteAddr),
		ProtocolHint:     "grpc",
		TraceID:          traceID,
		Metadata: map[string]string{
			"grpc_path": request.URL.Path,
		},
	}
	// 统一补齐服务身份元数据，便于后续日志链路直接复用。
	enrichTrafficOpenServiceIdentity(
		&trafficOpen,
		resolvedLogicalServiceID,
		resolvedServiceKey,
		resolvedServiceInstanceID,
	)
	pathExecuteResult := routing.PathExecuteResult{TargetKind: resolution.TargetKind}
	var proxyErr error
	switch resolution.TargetKind {
	case pb.RouteTargetTypeConnectorService:
		proxyErr = runtime.proxyGRPCIngressConnectorTarget(
			request.Context(),
			writer,
			request,
			resolution,
			trafficOpen,
			string(pb.RouteTargetTypeConnectorService),
			"",
			"",
		)
	case pb.RouteTargetTypeExternalService:
		proxyErr = runtime.proxyGRPCIngressExternalTarget(
			request.Context(),
			writer,
			request,
			*resolution.External,
			trafficOpen,
			string(pb.RouteTargetTypeExternalService),
			"",
			"",
		)
	default:
		proxyErr = ltfperrors.New(
			ltfperrors.CodeUnsupportedValue,
			fmt.Sprintf("unsupported grpc ingress target kind: %s", resolution.TargetKind),
		)
	}
	if proxyErr == nil {
		return
	}
	if errors.Is(proxyErr, errIngressResponseCommitted) {
		log.Printf(
			"bridge ingress grpc proxy post-commit error ignored authority=%s path=%s target_kind=%s traffic_id=%s trace_id=%s logical_service_id=%s instance_id=%s err=%v",
			authority,
			request.URL.Path,
			resolution.TargetKind,
			trafficID,
			traceID,
			resolvedLogicalServiceID,
			resolvedServiceInstanceID,
			proxyErr,
		)
		return
	}
	mappedFailure := ingressHTTPFailureMapper.Map(proxyErr, pathExecuteResult)
	writeIngressError(writer, mappedFailure.HTTPStatus, mappedFailure.Code, mappedFailure.Message, trafficID, traceID)
	log.Printf(
		"bridge ingress grpc proxy failed authority=%s path=%s target_kind=%s http=%d code=%s traffic_id=%s trace_id=%s logical_service_id=%s instance_id=%s err=%v",
		authority,
		request.URL.Path,
		resolution.TargetKind,
		mappedFailure.HTTPStatus,
		mappedFailure.Code,
		trafficID,
		traceID,
		resolvedLogicalServiceID,
		resolvedServiceInstanceID,
		proxyErr,
	)
}

func isGRPCContentType(rawContentType string) bool {
	normalizedContentType := strings.ToLower(strings.TrimSpace(rawContentType))
	return strings.HasPrefix(normalizedContentType, "application/grpc")
}

func (runtime *Runtime) proxyGRPCIngressConnectorTarget(
	ctx context.Context,
	writer http.ResponseWriter,
	request *http.Request,
	resolution routing.ResolveResult,
	trafficOpen pb.TrafficOpen,
	targetKindHeader string,
	hybridPath string,
	hybridFallbackStage string,
) error {
	if runtime == nil || runtime.dataPlane == nil {
		return ErrRuntimeDataPlaneDependencyMissing
	}
	if resolution.Connector == nil {
		return ErrRuntimeDataPlaneDependencyMissing
	}
	connectorID := strings.TrimSpace(resolution.Connector.Session.ConnectorID)
	if connectorID == "" {
		return ErrRuntimeDataPlaneDependencyMissing
	}
	responseCommitted := false
	_, dispatchErr := runtime.dispatchConnectorIngressWithRelay(
		ctx,
		connectorID,
		trafficOpen,
		connectorproxy.RelayFunc(func(relayContext context.Context, tunnel registry.RuntimeTunnel, trafficID string) error {
			upstreamConnection := newIngressTunnelDataConn(relayContext, tunnel, trafficID)
			response, roundTripErr := roundTripGRPCOverConn(relayContext, request, upstreamConnection)
			if roundTripErr != nil {
				return roundTripErr
			}
			writer.Header().Set("X-DevBridge-Route-Id", strings.TrimSpace(trafficOpen.RouteID))
			writer.Header().Set("X-DevBridge-Target-Kind", strings.TrimSpace(targetKindHeader))
			if strings.TrimSpace(hybridPath) != "" {
				writer.Header().Set("X-DevBridge-Hybrid-Path", strings.TrimSpace(hybridPath))
			}
			if strings.TrimSpace(hybridFallbackStage) != "" {
				writer.Header().Set("X-DevBridge-Hybrid-Fallback-Stage", strings.TrimSpace(hybridFallbackStage))
			}
			committed, proxyErr := writeUpstreamHTTPResponse(writer, response)
			if committed {
				responseCommitted = true
			}
			if proxyErr != nil {
				if committed {
					return markIngressResponseCommitted(proxyErr)
				}
				return proxyErr
			}
			postCommitContext := detachedPostCommitContext(relayContext)
			if closeErr := runtime.writeTunnelCloseAndAwaitAck(postCommitContext, tunnel, trafficID, "grpc_response_complete"); closeErr != nil {
				// close_ack 闭环未完成时把 tunnel 标记为不可安全回收，后续由 dispatcher 直接关闭。
				recycleErrorCode := ltfperrors.ExtractTunnelRecycleCode(closeErr)
				log.Printf(
					"bridge ingress grpc close-ack wait failed, fallback close traffic_id=%s logical_service_id=%s instance_id=%s recycle_error_code=%s err=%v",
					strings.TrimSpace(trafficID),
					strings.TrimSpace(trafficOpen.LogicalServiceID),
					strings.TrimSpace(trafficOpen.InstanceID),
					recycleErrorCode,
					closeErr,
				)
			}
			return nil
		}),
	)
	if dispatchErr != nil {
		return wrapIngressConnectorDispatchError(
			"proxy grpc ingress: dispatch connector path",
			dispatchErr,
			responseCommitted,
		)
	}
	return nil
}

func (runtime *Runtime) proxyGRPCIngressExternalTarget(
	ctx context.Context,
	writer http.ResponseWriter,
	request *http.Request,
	target pb.ExternalServiceTarget,
	trafficOpen pb.TrafficOpen,
	targetKindHeader string,
	hybridPath string,
	hybridFallbackStage string,
) error {
	endpointAddress, endpointErr := resolveExternalEndpointAddress(target)
	if endpointErr != nil {
		return endpointErr
	}
	upstreamConnection, dialErr := dialExternalEndpoint(ctx, endpointAddress)
	if dialErr != nil {
		return ltfperrors.New(ltfperrors.CodeDirectProxyDialFailed, dialErr.Error())
	}
	defer upstreamConnection.Close()

	response, roundTripErr := roundTripGRPCOverConn(ctx, request, upstreamConnection)
	if roundTripErr != nil {
		return roundTripErr
	}
	writer.Header().Set("X-DevBridge-Route-Id", strings.TrimSpace(trafficOpen.RouteID))
	writer.Header().Set("X-DevBridge-Target-Kind", strings.TrimSpace(targetKindHeader))
	if strings.TrimSpace(hybridPath) != "" {
		writer.Header().Set("X-DevBridge-Hybrid-Path", strings.TrimSpace(hybridPath))
	}
	if strings.TrimSpace(hybridFallbackStage) != "" {
		writer.Header().Set("X-DevBridge-Hybrid-Fallback-Stage", strings.TrimSpace(hybridFallbackStage))
	}
	committed, proxyErr := writeUpstreamHTTPResponse(writer, response)
	if proxyErr != nil {
		if committed {
			return markIngressResponseCommitted(proxyErr)
		}
		return proxyErr
	}
	return nil
}

func roundTripGRPCOverConn(ctx context.Context, request *http.Request, connection net.Conn) (*http.Response, error) {
	if request == nil || connection == nil {
		return nil, ErrRuntimeDataPlaneDependencyMissing
	}
	transport := &http2.Transport{}
	clientConn, clientConnErr := transport.NewClientConn(connection)
	if clientConnErr != nil {
		return nil, ltfperrors.New(ltfperrors.CodeDirectProxyRelayFailed, clientConnErr.Error())
	}
	upstreamRequest := cloneGRPCProxyRequest(ctx, request)
	response, roundTripErr := clientConn.RoundTrip(upstreamRequest)
	if roundTripErr != nil {
		_ = clientConn.Close()
		return nil, ltfperrors.New(ltfperrors.CodeDirectProxyRelayFailed, roundTripErr.Error())
	}
	response.Body = &upstreamResponseBodyCloser{
		ReadCloser: response.Body,
		onClose: func() error {
			return clientConn.Close()
		},
	}
	return response, nil
}

func cloneGRPCProxyRequest(ctx context.Context, request *http.Request) *http.Request {
	normalizedContext := ctx
	if normalizedContext == nil {
		normalizedContext = request.Context()
	}
	cloned := request.Clone(normalizedContext)
	cloned.URL = cloneGRPCProxyURL(request.URL, request.Host)
	cloned.RequestURI = ""
	cloned.Host = request.Host
	return cloned
}

func cloneGRPCProxyURL(rawURL *url.URL, host string) *url.URL {
	if rawURL == nil {
		return &url.URL{
			Scheme: "http",
			Host:   strings.TrimSpace(host),
			Path:   "/",
		}
	}
	cloned := *rawURL
	cloned.Scheme = "http"
	cloned.Host = strings.TrimSpace(host)
	if strings.TrimSpace(cloned.Path) == "" {
		cloned.Path = "/"
	}
	return &cloned
}

func writeUpstreamHTTPResponse(writer http.ResponseWriter, response *http.Response) (bool, error) {
	if writer == nil || response == nil {
		return false, ErrRuntimeDataPlaneDependencyMissing
	}
	defer response.Body.Close()
	copyHTTPHeaders(writer.Header(), response.Header)
	for _, trailerKey := range collectResponseTrailerKeys(response) {
		writer.Header().Add("Trailer", trailerKey)
	}
	statusCode := response.StatusCode
	if statusCode <= 0 {
		statusCode = http.StatusOK
	}
	writer.WriteHeader(statusCode)
	if _, copyErr := io.Copy(writer, response.Body); copyErr != nil {
		return true, copyErr
	}
	for trailerKey, trailerValues := range response.Trailer {
		if len(trailerValues) == 0 {
			continue
		}
		writer.Header()[trailerKey] = append([]string(nil), trailerValues...)
	}
	return true, nil
}

func collectResponseTrailerKeys(response *http.Response) []string {
	if response == nil {
		return nil
	}
	uniqueKeys := map[string]struct{}{}
	for trailerKey := range response.Trailer {
		normalizedKey := strings.TrimSpace(trailerKey)
		if normalizedKey != "" {
			uniqueKeys[normalizedKey] = struct{}{}
		}
	}
	for _, declaredValue := range response.Header.Values("Trailer") {
		for _, trailerKey := range strings.Split(declaredValue, ",") {
			normalizedKey := strings.TrimSpace(trailerKey)
			if normalizedKey != "" {
				uniqueKeys[normalizedKey] = struct{}{}
			}
		}
	}
	result := make([]string, 0, len(uniqueKeys))
	for trailerKey := range uniqueKeys {
		result = append(result, trailerKey)
	}
	return result
}

type upstreamResponseBodyCloser struct {
	io.ReadCloser
	once    sync.Once
	onClose func() error
}

func (closer *upstreamResponseBodyCloser) Close() error {
	if closer == nil {
		return nil
	}
	closeErr := closer.ReadCloser.Close()
	callbackErr := error(nil)
	closer.once.Do(func() {
		if closer.onClose != nil {
			callbackErr = closer.onClose()
		}
	})
	if closeErr != nil || callbackErr != nil {
		return errors.Join(closeErr, callbackErr)
	}
	return nil
}

type ingressTunnelDataConn struct {
	ctx       context.Context
	tunnel    registry.RuntimeTunnel
	trafficID string

	readMu  sync.Mutex
	writeMu sync.Mutex
	readBuf []byte
	closed  bool
}

func newIngressTunnelDataConn(ctx context.Context, tunnel registry.RuntimeTunnel, trafficID string) net.Conn {
	return &ingressTunnelDataConn{
		ctx:       ctx,
		tunnel:    tunnel,
		trafficID: strings.TrimSpace(trafficID),
	}
}

func (conn *ingressTunnelDataConn) Read(buffer []byte) (int, error) {
	if conn == nil || conn.tunnel == nil {
		return 0, ErrRuntimeDataPlaneDependencyMissing
	}
	if len(buffer) == 0 {
		return 0, nil
	}
	conn.readMu.Lock()
	defer conn.readMu.Unlock()
	if conn.closed {
		return 0, net.ErrClosed
	}
	for len(conn.readBuf) == 0 {
		payload, readErr := conn.tunnel.ReadPayload(conn.ctx)
		if readErr != nil {
			return 0, readErr
		}
		if payload.Close != nil && strings.TrimSpace(payload.Close.TrafficID) == conn.trafficID {
			_ = conn.tunnel.WritePayload(conn.ctx, pb.StreamPayload{
				CloseAck: &pb.TrafficCloseAck{
					TrafficID: conn.trafficID,
					Accepted:  true,
				},
			})
			return 0, io.EOF
		}
		if payload.Reset != nil && strings.TrimSpace(payload.Reset.TrafficID) == conn.trafficID {
			return 0, &connectorproxy.RelayResetError{
				TrafficID:    conn.trafficID,
				ResetCode:    strings.TrimSpace(payload.Reset.ErrorCode),
				ResetMessage: strings.TrimSpace(payload.Reset.ErrorMessage),
			}
		}
		if len(payload.Data) == 0 {
			continue
		}
		conn.readBuf = append(conn.readBuf[:0], payload.Data...)
	}
	copiedSize := copy(buffer, conn.readBuf)
	conn.readBuf = conn.readBuf[copiedSize:]
	return copiedSize, nil
}

func (conn *ingressTunnelDataConn) Write(payload []byte) (int, error) {
	if conn == nil || conn.tunnel == nil {
		return 0, ErrRuntimeDataPlaneDependencyMissing
	}
	if len(payload) == 0 {
		return 0, nil
	}
	conn.writeMu.Lock()
	defer conn.writeMu.Unlock()
	if conn.closed {
		return 0, net.ErrClosed
	}
	writtenSize := 0
	for writtenSize < len(payload) {
		endOffset := writtenSize + ingressTunnelDataFrameMaxBytes
		if endOffset > len(payload) {
			endOffset = len(payload)
		}
		chunk := append([]byte(nil), payload[writtenSize:endOffset]...)
		if writeErr := conn.tunnel.WritePayload(conn.ctx, pb.StreamPayload{Data: chunk}); writeErr != nil {
			return writtenSize, writeErr
		}
		writtenSize = endOffset
	}
	return writtenSize, nil
}

func (conn *ingressTunnelDataConn) Close() error {
	if conn == nil {
		return nil
	}
	conn.readMu.Lock()
	conn.closed = true
	conn.readBuf = nil
	conn.readMu.Unlock()
	return nil
}

func (conn *ingressTunnelDataConn) LocalAddr() net.Addr {
	return ingressTunnelNetAddr("ingress-tunnel-local")
}

func (conn *ingressTunnelDataConn) RemoteAddr() net.Addr {
	return ingressTunnelNetAddr("ingress-tunnel-remote")
}

func (conn *ingressTunnelDataConn) SetDeadline(time.Time) error {
	return nil
}

func (conn *ingressTunnelDataConn) SetReadDeadline(time.Time) error {
	return nil
}

func (conn *ingressTunnelDataConn) SetWriteDeadline(time.Time) error {
	return nil
}

type ingressTunnelNetAddr string

func (address ingressTunnelNetAddr) Network() string {
	return "ltfp-tunnel"
}

func (address ingressTunnelNetAddr) String() string {
	return string(address)
}
