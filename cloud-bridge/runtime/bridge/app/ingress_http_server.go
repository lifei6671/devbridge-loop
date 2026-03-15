package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/connectorproxy"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/ingress"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/routing"
	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

const (
	defaultIngressHTTPDispatchTimeout = 30 * time.Second
	ingressTunnelDataFrameMaxBytes    = 16 * 1024
	// 路由刚同步时可能存在短暂窗口，轻量重试避免瞬时 404。
	defaultIngressHTTPResolveRetryAttempts = 3
	defaultIngressHTTPResolveRetryInterval = 120 * time.Millisecond
	// connector 读响应阶段遇到瞬时 EOF 时，对幂等请求允许一次重试。
	defaultIngressHTTPConnectorReadAttempts = 2
)

var (
	ingressHTTPFailureMapper = routing.NewFailureMapper()
	ingressTrafficIDCounter  atomic.Uint64
)

var (
	// errIngressResponseCommitted 表示响应头/体已开始写出，不能再回写结构化错误。
	errIngressResponseCommitted = errors.New("ingress response already committed")
)

// newIngressHTTPServer 创建 Bridge HTTP ingress 监听器。
func newIngressHTTPServer(runtime *Runtime, listenAddr string) *http.Server {
	normalizedListenAddr := strings.TrimSpace(listenAddr)
	if normalizedListenAddr == "" {
		return nil
	}
	return &http.Server{
		Addr: normalizedListenAddr,
		Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			runtime.handleHTTPIngress(writer, request)
		}),
	}
}

// handleHTTPIngress 把外部 HTTP 请求桥接到统一 ingress 分发链路。
func (runtime *Runtime) handleHTTPIngress(writer http.ResponseWriter, request *http.Request) {
	if runtime == nil {
		writeIngressError(writer, http.StatusServiceUnavailable, "RUNTIME_UNAVAILABLE", "bridge runtime unavailable", "", "")
		return
	}
	if request == nil {
		writeIngressError(writer, http.StatusBadRequest, "INVALID_REQUEST", "request is required", "", "")
		return
	}

	now := time.Now().UTC()
	trafficID := buildIngressTrafficID(now)
	traceID := strings.TrimSpace(request.Header.Get("X-Request-Id"))
	if traceID == "" {
		traceID = trafficID
	}
	protocol := resolveIngressHTTPProtocol(request)
	authority := resolveIngressAuthority(request)
	namespace := firstNonEmptyHeader(request.Header, "X-DevBridge-Namespace", "X-Namespace")
	environment := firstNonEmptyHeader(request.Header, "X-DevBridge-Environment", "X-Environment", "X-Env")

	if runtime.dataPlane == nil || runtime.dataPlane.httpGateway == nil || runtime.dataPlane.resolver == nil {
		writeIngressError(writer, http.StatusServiceUnavailable, "RUNTIME_DATAPLANE_UNAVAILABLE", "bridge runtime data plane unavailable", trafficID, traceID)
		return
	}

	dispatchContext, cancel := context.WithTimeout(request.Context(), defaultIngressHTTPDispatchTimeout)
	defer cancel()

	lookupRequest, lookupErr := runtime.dataPlane.httpGateway.BuildRouteLookupRequest(ingress.HTTPGatewayRequest{
		Protocol:    protocol,
		Host:        authority,
		Authority:   authority,
		Path:        request.URL.Path,
		Namespace:   namespace,
		Environment: environment,
		Metadata: map[string]string{
			"http_method":      strings.TrimSpace(request.Method),
			"http_request_uri": request.URL.RequestURI(),
			"http_user_agent":  strings.TrimSpace(request.UserAgent()),
		},
	})
	if lookupErr != nil {
		mappedFailure := ingressHTTPFailureMapper.Map(lookupErr, routing.PathExecuteResult{})
		writeIngressError(writer, mappedFailure.HTTPStatus, mappedFailure.Code, mappedFailure.Message, trafficID, traceID)
		log.Printf(
			"bridge ingress http lookup failed host=%s path=%s http=%d code=%s traffic_id=%s trace_id=%s err=%v",
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
	resolution, resolveErr := runtime.resolveHTTPIngressRouteWithRetry(dispatchContext, lookupRequest)
	if resolveErr != nil {
		mappedFailure := ingressHTTPFailureMapper.Map(resolveErr, routing.PathExecuteResult{})
		writeIngressError(writer, mappedFailure.HTTPStatus, mappedFailure.Code, mappedFailure.Message, trafficID, traceID)
		log.Printf(
			"bridge ingress http resolve failed host=%s path=%s http=%d code=%s traffic_id=%s trace_id=%s err=%v",
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

	trafficOpen := pb.TrafficOpen{
		TrafficID:    trafficID,
		RouteID:      strings.TrimSpace(resolution.Route.RouteID),
		ServiceID:    resolveTrafficServiceIDFromResolution(resolution),
		SourceAddr:   strings.TrimSpace(request.RemoteAddr),
		ProtocolHint: protocol,
		TraceID:      traceID,
		Metadata: map[string]string{
			"http_method":      strings.TrimSpace(request.Method),
			"http_request_uri": request.URL.RequestURI(),
		},
	}

	writer.Header().Set("X-DevBridge-Traffic-Id", trafficID)
	writer.Header().Set("X-DevBridge-Trace-Id", traceID)

	pathExecuteResult := routing.PathExecuteResult{TargetKind: resolution.TargetKind}
	var proxyErr error
	switch resolution.TargetKind {
	case pb.RouteTargetTypeConnectorService:
		proxyErr = runtime.proxyHTTPIngressConnector(dispatchContext, writer, request, resolution, trafficOpen)
	case pb.RouteTargetTypeExternalService:
		proxyErr = runtime.proxyHTTPIngressExternal(dispatchContext, writer, request, resolution, trafficOpen)
	case pb.RouteTargetTypeHybridGroup:
		proxyErr = runtime.proxyHTTPIngressHybrid(dispatchContext, writer, request, resolution, trafficOpen)
	default:
		proxyErr = ltfperrors.New(
			ltfperrors.CodeUnsupportedValue,
			fmt.Sprintf("unsupported http ingress target kind: %s", resolution.TargetKind),
		)
	}
	if proxyErr != nil {
		if errors.Is(proxyErr, errIngressResponseCommitted) {
			log.Printf(
				"bridge ingress http proxy post-commit error ignored host=%s path=%s target_kind=%s traffic_id=%s trace_id=%s err=%v",
				authority,
				request.URL.Path,
				resolution.TargetKind,
				trafficID,
				traceID,
				proxyErr,
			)
			return
		}
		mappedFailure := ingressHTTPFailureMapper.Map(proxyErr, pathExecuteResult)
		writeIngressError(writer, mappedFailure.HTTPStatus, mappedFailure.Code, mappedFailure.Message, trafficID, traceID)
		log.Printf(
			"bridge ingress http proxy failed host=%s path=%s target_kind=%s http=%d code=%s traffic_id=%s trace_id=%s err=%v",
			authority,
			request.URL.Path,
			resolution.TargetKind,
			mappedFailure.HTTPStatus,
			mappedFailure.Code,
			trafficID,
			traceID,
			proxyErr,
		)
	}
}

// proxyHTTPIngressConnector 通过 connector tunnel 执行一次 HTTP 请求-响应透传。
func (runtime *Runtime) proxyHTTPIngressConnector(
	ctx context.Context,
	writer http.ResponseWriter,
	request *http.Request,
	resolution routing.ResolveResult,
	trafficOpen pb.TrafficOpen,
) error {
	if runtime == nil || runtime.dataPlane == nil || runtime.dataPlane.tunnelRegistry == nil {
		return ErrRuntimeDataPlaneDependencyMissing
	}
	if request == nil || writer == nil {
		return fmt.Errorf("proxy connector ingress: invalid request or response writer")
	}
	if resolution.Connector == nil {
		return ErrRuntimeDataPlaneDependencyMissing
	}
	connectorID := strings.TrimSpace(resolution.Connector.Session.ConnectorID)
	if connectorID == "" {
		return ErrRuntimeDataPlaneDependencyMissing
	}
	retryAttempts := defaultIngressHTTPConnectorReadAttempts
	if retryAttempts <= 0 {
		retryAttempts = 1
	}
	var lastReadErr error
	for attempt := 1; attempt <= retryAttempts; attempt++ {
		tunnelAcquirer, acquirerErr := connectorproxy.NewTunnelAcquirer(connectorproxy.TunnelAcquirerOptions{
			Registry:       runtime.dataPlane.tunnelRegistry,
			WaitHint:       defaultBridgeAcquireWaitHint,
			PollInterval:   defaultBridgeAcquirePollInterval,
			EnableNoIdleWT: true,
		})
		if acquirerErr != nil {
			return fmt.Errorf("proxy connector ingress: new tunnel acquirer: %w", acquirerErr)
		}
		acquiredTunnel, acquireErr := tunnelAcquirer.AcquireIdleTunnel(ctx, connectorID)
		if acquireErr != nil {
			return fmt.Errorf("proxy connector ingress: acquire idle tunnel: %w", acquireErr)
		}
		if markActiveErr := runtime.dataPlane.tunnelRegistry.MarkActive(time.Now().UTC(), acquiredTunnel.TunnelID, trafficOpen.TrafficID); markActiveErr != nil {
			_ = runtime.recycleIngressTunnelBroken(acquiredTunnel, markActiveErr.Error())
			return fmt.Errorf("proxy connector ingress: mark tunnel active: %w", markActiveErr)
		}

		openHandshake := connectorproxy.NewOpenHandshake(connectorproxy.OpenHandshakeOptions{
			OpenTimeout:         defaultBridgeTrafficOpenTimeout,
			LateAckDrainTimeout: defaultBridgeLateAckDrainTimeout,
		})
		if _, openErr := openHandshake.Execute(ctx, acquiredTunnel.Tunnel, trafficOpen); openErr != nil {
			_ = runtime.recycleIngressTunnelBroken(acquiredTunnel, openErr.Error())
			return fmt.Errorf("proxy connector ingress: open handshake: %w", openErr)
		}

		serializedRequest, serializeErr := serializeHTTPRequestForTunnel(request)
		if serializeErr != nil {
			_ = runtime.recycleIngressTunnelBroken(acquiredTunnel, serializeErr.Error())
			return fmt.Errorf("proxy connector ingress: serialize http request: %w", serializeErr)
		}
		if writeErr := writeTunnelDataFrames(ctx, acquiredTunnel.Tunnel, serializedRequest); writeErr != nil {
			_ = runtime.recycleIngressTunnelBroken(acquiredTunnel, writeErr.Error())
			return fmt.Errorf("proxy connector ingress: write request frames: %w", writeErr)
		}

		upstreamResponse, responseErr := readHTTPResponseFromTunnel(ctx, acquiredTunnel.Tunnel, trafficOpen.TrafficID, request)
		if responseErr != nil {
			lastReadErr = responseErr
			_ = runtime.recycleIngressTunnelBroken(acquiredTunnel, responseErr.Error())
			if attempt < retryAttempts && shouldRetryConnectorIngressRead(request, responseErr) {
				log.Printf(
					"bridge ingress http connector retry traffic_id=%s route_id=%s attempt=%d/%d err=%v",
					strings.TrimSpace(trafficOpen.TrafficID),
					strings.TrimSpace(trafficOpen.RouteID),
					attempt+1,
					retryAttempts,
					responseErr,
				)
				continue
			}
			return fmt.Errorf("proxy connector ingress: read upstream response: %w", responseErr)
		}

		copyHTTPHeaders(writer.Header(), upstreamResponse.Header)
		writer.Header().Set("X-DevBridge-Traffic-Id", strings.TrimSpace(trafficOpen.TrafficID))
		writer.Header().Set("X-DevBridge-Trace-Id", strings.TrimSpace(trafficOpen.TraceID))
		writer.Header().Set("X-DevBridge-Route-Id", strings.TrimSpace(trafficOpen.RouteID))
		writer.Header().Set("X-DevBridge-Target-Kind", string(pb.RouteTargetTypeConnectorService))
		writer.WriteHeader(upstreamResponse.StatusCode)
		if _, copyErr := io.Copy(writer, upstreamResponse.Body); copyErr != nil {
			_ = upstreamResponse.Body.Close()
			_ = runtime.recycleIngressTunnelBroken(acquiredTunnel, copyErr.Error())
			return markIngressResponseCommitted(fmt.Errorf("proxy connector ingress: copy upstream body: %w", copyErr))
		}
		_ = upstreamResponse.Body.Close()

		_ = writeTunnelCloseFrame(ctx, acquiredTunnel.Tunnel, trafficOpen.TrafficID, "http_response_complete")
		if recycleErr := runtime.recycleIngressTunnelClosed(acquiredTunnel); recycleErr != nil {
			return markIngressResponseCommitted(fmt.Errorf("proxy connector ingress: recycle tunnel closed: %w", recycleErr))
		}
		return nil
	}
	if lastReadErr != nil {
		return fmt.Errorf("proxy connector ingress: read upstream response: %w", lastReadErr)
	}
	return fmt.Errorf("proxy connector ingress: connector retries exhausted")
}

func (runtime *Runtime) proxyHTTPIngressExternal(
	ctx context.Context,
	writer http.ResponseWriter,
	request *http.Request,
	resolution routing.ResolveResult,
	trafficOpen pb.TrafficOpen,
) error {
	if resolution.External == nil {
		return ErrRuntimeDataPlaneDependencyMissing
	}
	return runtime.proxyHTTPIngressExternalTarget(
		ctx,
		writer,
		request,
		*resolution.External,
		trafficOpen,
		string(pb.RouteTargetTypeExternalService),
		"",
	)
}

func (runtime *Runtime) proxyHTTPIngressHybrid(
	ctx context.Context,
	writer http.ResponseWriter,
	request *http.Request,
	resolution routing.ResolveResult,
	trafficOpen pb.TrafficOpen,
) error {
	if resolution.Hybrid == nil {
		return ErrRuntimeDataPlaneDependencyMissing
	}
	primaryResolution := routing.ResolveResult{
		Route:       resolution.Route,
		TargetKind:  pb.RouteTargetTypeConnectorService,
		IngressMode: resolution.IngressMode,
		Connector:   &resolution.Hybrid.Primary,
	}
	primaryErr := runtime.proxyHTTPIngressConnector(ctx, writer, request, primaryResolution, trafficOpen)
	if primaryErr == nil {
		writer.Header().Set("X-DevBridge-Target-Kind", string(pb.RouteTargetTypeHybridGroup))
		writer.Header().Set("X-DevBridge-Hybrid-Path", "primary")
		return nil
	}
	fallbackStage, allowFallback := classifyIngressHybridFallback(primaryErr)
	if !allowFallback {
		return primaryErr
	}
	fallbackErr := runtime.proxyHTTPIngressExternalTarget(
		ctx,
		writer,
		request,
		resolution.Hybrid.Fallback,
		trafficOpen,
		string(pb.RouteTargetTypeHybridGroup),
		fallbackStage,
	)
	if fallbackErr != nil {
		return errors.Join(primaryErr, fallbackErr)
	}
	return nil
}

func classifyIngressHybridFallback(proxyErr error) (string, bool) {
	if proxyErr == nil {
		return "", false
	}
	if errors.Is(proxyErr, connectorproxy.ErrNoIdleTunnel) {
		return routing.HybridFallbackStagePreOpenNoTunnel, true
	}
	if errors.Is(proxyErr, connectorproxy.ErrTrafficOpenRejected) || errors.Is(proxyErr, connectorproxy.ErrOpenAckTimeout) {
		return routing.HybridFallbackStagePreOpenWithTunnel, true
	}
	return "", false
}

func (runtime *Runtime) proxyHTTPIngressExternalTarget(
	ctx context.Context,
	writer http.ResponseWriter,
	request *http.Request,
	target pb.ExternalServiceTarget,
	trafficOpen pb.TrafficOpen,
	targetKindHeader string,
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

	serializedRequest, serializeErr := serializeHTTPRequestForTunnel(request)
	if serializeErr != nil {
		return ltfperrors.New(ltfperrors.CodeDirectProxyRelayFailed, serializeErr.Error())
	}
	if writeErr := writeAllToUpstream(ctx, upstreamConnection, serializedRequest); writeErr != nil {
		return ltfperrors.New(ltfperrors.CodeDirectProxyRelayFailed, writeErr.Error())
	}
	requestClone := request.Clone(request.Context())
	requestClone.URL = cloneRequestURLForTunnel(request.URL)
	requestClone.RequestURI = ""
	upstreamResponse, readErr := http.ReadResponse(bufio.NewReader(upstreamConnection), requestClone)
	if readErr != nil {
		return ltfperrors.New(ltfperrors.CodeDirectProxyRelayFailed, readErr.Error())
	}
	defer upstreamResponse.Body.Close()

	copyHTTPHeaders(writer.Header(), upstreamResponse.Header)
	writer.Header().Set("X-DevBridge-Traffic-Id", strings.TrimSpace(trafficOpen.TrafficID))
	writer.Header().Set("X-DevBridge-Trace-Id", strings.TrimSpace(trafficOpen.TraceID))
	writer.Header().Set("X-DevBridge-Route-Id", strings.TrimSpace(trafficOpen.RouteID))
	writer.Header().Set("X-DevBridge-Target-Kind", strings.TrimSpace(targetKindHeader))
	if strings.TrimSpace(hybridFallbackStage) != "" {
		writer.Header().Set("X-DevBridge-Hybrid-Path", "fallback")
		writer.Header().Set("X-DevBridge-Hybrid-Fallback-Stage", strings.TrimSpace(hybridFallbackStage))
	}
	writer.WriteHeader(upstreamResponse.StatusCode)
	if _, copyErr := io.Copy(writer, upstreamResponse.Body); copyErr != nil {
		return markIngressResponseCommitted(ltfperrors.New(ltfperrors.CodeDirectProxyRelayFailed, copyErr.Error()))
	}
	return nil
}

func resolveExternalEndpointAddress(target pb.ExternalServiceTarget) (string, error) {
	endpointAddresses := parseSelectorEndpointAddresses(target.Selector)
	if len(endpointAddresses) == 0 {
		return "", ltfperrors.New(
			ltfperrors.CodeDiscoveryNoEndpoint,
			"external_service.selector.endpoint(s) is required for http ingress",
		)
	}
	return strings.TrimSpace(endpointAddresses[0]), nil
}

func dialExternalEndpoint(ctx context.Context, address string) (net.Conn, error) {
	normalizedAddress := strings.TrimSpace(address)
	if normalizedAddress == "" {
		return nil, fmt.Errorf("empty endpoint address")
	}
	normalizedContext := ctx
	if normalizedContext == nil {
		normalizedContext = context.Background()
	}
	if _, hasDeadline := normalizedContext.Deadline(); !hasDeadline && defaultBridgeDirectDialTimeout > 0 {
		var cancel context.CancelFunc
		normalizedContext, cancel = context.WithTimeout(normalizedContext, defaultBridgeDirectDialTimeout)
		defer cancel()
	}
	connection, err := (&net.Dialer{}).DialContext(normalizedContext, "tcp", normalizedAddress)
	if err != nil {
		return nil, fmt.Errorf("dial endpoint %s: %w", normalizedAddress, err)
	}
	return connection, nil
}

func writeAllToUpstream(ctx context.Context, writer io.Writer, payload []byte) error {
	if len(payload) == 0 {
		return nil
	}
	writtenSize := 0
	for writtenSize < len(payload) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		currentWritten, err := writer.Write(payload[writtenSize:])
		if currentWritten > 0 {
			writtenSize += currentWritten
		}
		if err != nil {
			return err
		}
		if currentWritten == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

func markIngressResponseCommitted(err error) error {
	if err == nil {
		return errIngressResponseCommitted
	}
	return errors.Join(errIngressResponseCommitted, err)
}

func serializeHTTPRequestForTunnel(request *http.Request) ([]byte, error) {
	if request == nil {
		return nil, fmt.Errorf("request is required")
	}
	requestClone := request.Clone(request.Context())
	requestClone.URL = cloneRequestURLForTunnel(request.URL)
	requestClone.RequestURI = ""
	requestClone.Close = true
	requestClone.Header = requestClone.Header.Clone()
	requestClone.Header.Set("Connection", "close")

	buffer := bytes.NewBuffer(nil)
	if err := requestClone.Write(buffer); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func cloneRequestURLForTunnel(rawURL *url.URL) *url.URL {
	if rawURL == nil {
		return &url.URL{Path: "/"}
	}
	clonedURL := *rawURL
	clonedURL.Scheme = ""
	clonedURL.Host = ""
	if strings.TrimSpace(clonedURL.Path) == "" {
		clonedURL.Path = "/"
	}
	return &clonedURL
}

func writeTunnelDataFrames(ctx context.Context, tunnel registry.RuntimeTunnel, payload []byte) error {
	if tunnel == nil {
		return ErrRuntimeDataPlaneDependencyMissing
	}
	if len(payload) == 0 {
		return nil
	}
	frameSize := ingressTunnelDataFrameMaxBytes
	if frameSize <= 0 {
		frameSize = len(payload)
	}
	for offset := 0; offset < len(payload); offset += frameSize {
		frameEnd := offset + frameSize
		if frameEnd > len(payload) {
			frameEnd = len(payload)
		}
		chunk := append([]byte(nil), payload[offset:frameEnd]...)
		if err := tunnel.WritePayload(ctx, pb.StreamPayload{Data: chunk}); err != nil {
			return err
		}
	}
	return nil
}

func readHTTPResponseFromTunnel(
	ctx context.Context,
	tunnel registry.RuntimeTunnel,
	trafficID string,
	request *http.Request,
) (*http.Response, error) {
	if tunnel == nil {
		return nil, ErrRuntimeDataPlaneDependencyMissing
	}
	responseReader := bufio.NewReader(&tunnelTrafficReader{
		ctx:       ctx,
		tunnel:    tunnel,
		trafficID: strings.TrimSpace(trafficID),
	})
	requestClone := request.Clone(request.Context())
	requestClone.URL = cloneRequestURLForTunnel(request.URL)
	requestClone.RequestURI = ""
	return http.ReadResponse(responseReader, requestClone)
}

func writeTunnelCloseFrame(ctx context.Context, tunnel registry.RuntimeTunnel, trafficID string, reason string) error {
	if tunnel == nil {
		return ErrRuntimeDataPlaneDependencyMissing
	}
	closePayload := pb.StreamPayload{
		Close: &pb.TrafficClose{
			TrafficID: strings.TrimSpace(trafficID),
			Reason:    strings.TrimSpace(reason),
		},
	}
	return tunnel.WritePayload(ctx, closePayload)
}

func shouldRetryConnectorIngressRead(request *http.Request, err error) bool {
	if request == nil || err == nil {
		return false
	}
	if !isSafeHTTPMethodForRetry(request.Method) {
		return false
	}
	if request.ContentLength > 0 {
		return false
	}
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || strings.Contains(strings.ToLower(err.Error()), "unexpected eof")
}

func isSafeHTTPMethodForRetry(method string) bool {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}

func (runtime *Runtime) recycleIngressTunnelClosed(runtimeTunnel registry.TunnelRuntime) error {
	if runtime == nil || runtime.dataPlane == nil || runtime.dataPlane.tunnelRegistry == nil || runtimeTunnel.Tunnel == nil {
		return ErrRuntimeDataPlaneDependencyMissing
	}
	closeErr := runtimeTunnel.Tunnel.Close()
	markClosedErr := runtime.dataPlane.tunnelRegistry.MarkClosed(time.Now().UTC(), runtimeTunnel.TunnelID)
	_, removeErr := runtime.dataPlane.tunnelRegistry.RemoveTerminal(runtimeTunnel.TunnelID)
	if closeErr != nil || markClosedErr != nil || removeErr != nil {
		return errors.Join(closeErr, markClosedErr, removeErr)
	}
	return nil
}

func (runtime *Runtime) recycleIngressTunnelBroken(runtimeTunnel registry.TunnelRuntime, reason string) error {
	if runtime == nil || runtime.dataPlane == nil || runtime.dataPlane.tunnelRegistry == nil || runtimeTunnel.Tunnel == nil {
		return ErrRuntimeDataPlaneDependencyMissing
	}
	closeErr := runtimeTunnel.Tunnel.Close()
	markBrokenErr := runtime.dataPlane.tunnelRegistry.MarkBroken(time.Now().UTC(), runtimeTunnel.TunnelID, strings.TrimSpace(reason))
	_, removeErr := runtime.dataPlane.tunnelRegistry.RemoveTerminal(runtimeTunnel.TunnelID)
	if closeErr != nil || markBrokenErr != nil || removeErr != nil {
		return errors.Join(closeErr, markBrokenErr, removeErr)
	}
	return nil
}

func copyHTTPHeaders(destination http.Header, source http.Header) {
	for headerName, values := range source {
		if len(values) == 0 {
			continue
		}
		destination.Del(headerName)
		for _, value := range values {
			destination.Add(headerName, value)
		}
	}
}

type tunnelTrafficReader struct {
	ctx          context.Context
	tunnel       registry.RuntimeTunnel
	trafficID    string
	bufferedData []byte
}

func (reader *tunnelTrafficReader) Read(buffer []byte) (int, error) {
	if reader == nil || reader.tunnel == nil {
		return 0, ErrRuntimeDataPlaneDependencyMissing
	}
	if len(buffer) == 0 {
		return 0, nil
	}
	for len(reader.bufferedData) == 0 {
		payload, err := reader.tunnel.ReadPayload(reader.ctx)
		if err != nil {
			return 0, err
		}
		if payload.Close != nil && strings.TrimSpace(payload.Close.TrafficID) == reader.trafficID {
			return 0, io.EOF
		}
		if payload.Reset != nil && strings.TrimSpace(payload.Reset.TrafficID) == reader.trafficID {
			return 0, &connectorproxy.RelayResetError{
				TrafficID:    reader.trafficID,
				ResetCode:    strings.TrimSpace(payload.Reset.ErrorCode),
				ResetMessage: strings.TrimSpace(payload.Reset.ErrorMessage),
			}
		}
		if len(payload.Data) == 0 {
			continue
		}
		reader.bufferedData = append(reader.bufferedData[:0], payload.Data...)
	}
	copiedSize := copy(buffer, reader.bufferedData)
	reader.bufferedData = reader.bufferedData[copiedSize:]
	return copiedSize, nil
}

func resolveIngressHTTPProtocol(request *http.Request) string {
	if request == nil {
		return "http"
	}
	if request.TLS != nil {
		return "https"
	}
	if forwardedProto := strings.ToLower(strings.TrimSpace(request.Header.Get("X-Forwarded-Proto"))); forwardedProto != "" {
		return forwardedProto
	}
	return "http"
}

func (runtime *Runtime) resolveHTTPIngressRouteWithRetry(
	ctx context.Context,
	lookupRequest ingress.RouteLookupRequest,
) (routing.ResolveResult, error) {
	if runtime == nil || runtime.dataPlane == nil || runtime.dataPlane.resolver == nil {
		return routing.ResolveResult{}, ErrRuntimeDataPlaneDependencyMissing
	}
	retryAttempts := defaultIngressHTTPResolveRetryAttempts
	if retryAttempts <= 0 {
		retryAttempts = 1
	}
	retryInterval := defaultIngressHTTPResolveRetryInterval
	if retryInterval <= 0 {
		retryInterval = 100 * time.Millisecond
	}

	var lastErr error
	for attempt := 1; attempt <= retryAttempts; attempt++ {
		resolution, resolveErr := runtime.dataPlane.resolver.Resolve(lookupRequest)
		if resolveErr == nil {
			return resolution, nil
		}
		lastErr = resolveErr
		if ltfperrors.ExtractCode(resolveErr) != ltfperrors.CodeIngressRouteMismatch || attempt == retryAttempts {
			return routing.ResolveResult{}, resolveErr
		}

		retryTimer := time.NewTimer(retryInterval)
		select {
		case <-ctx.Done():
			if !retryTimer.Stop() {
				select {
				case <-retryTimer.C:
				default:
				}
			}
			return routing.ResolveResult{}, ctx.Err()
		case <-retryTimer.C:
		}
	}
	if lastErr != nil {
		return routing.ResolveResult{}, lastErr
	}
	return routing.ResolveResult{}, ltfperrors.New(ltfperrors.CodeIngressRouteMismatch, "no route matches current ingress request")
}

func resolveIngressAuthority(request *http.Request) string {
	if request == nil {
		return ""
	}
	authority := strings.TrimSpace(request.Host)
	if authority != "" {
		return authority
	}
	if request.URL != nil {
		return strings.TrimSpace(request.URL.Host)
	}
	return ""
}

func firstNonEmptyHeader(headers http.Header, keys ...string) string {
	for _, key := range keys {
		value := strings.TrimSpace(headers.Get(key))
		if value != "" {
			return value
		}
	}
	return ""
}

func buildIngressTrafficID(now time.Time) string {
	return fmt.Sprintf("ingress-http-%d-%d", now.UnixNano(), ingressTrafficIDCounter.Add(1))
}

func writeIngressJSON(writer http.ResponseWriter, statusCode int, payload any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(statusCode)
	_ = json.NewEncoder(writer).Encode(payload)
}

func writeIngressError(
	writer http.ResponseWriter,
	statusCode int,
	code string,
	message string,
	trafficID string,
	traceID string,
) {
	writeIngressJSON(writer, statusCode, map[string]any{
		"traffic_id": strings.TrimSpace(trafficID),
		"trace_id":   strings.TrimSpace(traceID),
		"error": map[string]any{
			"code":    strings.TrimSpace(code),
			"message": strings.TrimSpace(message),
		},
	})
}
