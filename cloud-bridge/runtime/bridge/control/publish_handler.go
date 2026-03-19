package control

import (
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/admission"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/obs"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
	"github.com/lifei6671/devbridge-loop/ltfp/consistency"
	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
	"github.com/lifei6671/devbridge-loop/ltfp/validate"
)

// PublishHandlerOptions 定义 PublishHandler 构造参数。
type PublishHandlerOptions struct {
	Guard               *consistency.ResourceEventGuard
	SessionRegistry     *registry.SessionRegistry
	ServiceRegistry     *registry.ServiceRegistry
	RouteRegistry       *registry.RouteRegistry
	Metrics             *obs.Metrics
	Admission           RouteAdmission
	HostDeriver         HostDeriver
	Now                 func() time.Time
	ServiceIDGenerator  func(now time.Time, logicalServiceKey string) string
	InstanceIDGenerator func(now time.Time, logicalServiceID string, connectorID string) string
}

// HostDeriver 定义 Host 自动派生能力。
type HostDeriver interface {
	Derive(serviceName string, scope pb.Scope) (string, error)
}

// PublishHandler 处理服务发布与下线事件。
type PublishHandler struct {
	guard               *consistency.ResourceEventGuard
	sessionRegistry     *registry.SessionRegistry
	serviceRegistry     *registry.ServiceRegistry
	routeRegistry       *registry.RouteRegistry
	metrics             *obs.Metrics
	admission           RouteAdmission
	hostDeriver         HostDeriver
	now                 func() time.Time
	serviceIDGenerator  func(now time.Time, logicalServiceKey string) string
	instanceIDGenerator func(now time.Time, logicalServiceID string, connectorID string) string
}

var publishServiceIDSequence uint64
var publishInstanceIDSequence uint64

// NewPublishHandler 创建发布处理器。
func NewPublishHandler(options PublishHandlerOptions) *PublishHandler {
	nowFunc := options.Now
	if nowFunc == nil {
		nowFunc = func() time.Time { return time.Now().UTC() }
	}
	guard := options.Guard
	if guard == nil {
		guard = consistency.NewResourceEventGuard(4096)
	}
	serviceRegistry := options.ServiceRegistry
	if serviceRegistry == nil {
		serviceRegistry = registry.NewServiceRegistry()
	}
	routeRegistry := options.RouteRegistry
	if routeRegistry == nil {
		routeRegistry = registry.NewRouteRegistry()
	}
	serviceIDGenerator := options.ServiceIDGenerator
	if serviceIDGenerator == nil {
		serviceIDGenerator = defaultPublishLogicalServiceIDGenerator
	}
	instanceIDGenerator := options.InstanceIDGenerator
	if instanceIDGenerator == nil {
		instanceIDGenerator = defaultPublishInstanceIDGenerator
	}
	metrics := options.Metrics
	if metrics == nil {
		metrics = obs.DefaultMetrics
	}
	routeAdmission := options.Admission
	if routeAdmission == nil {
		routeAdmission = admission.NewRouteConflictAdmission(routeRegistry, serviceRegistry)
	}
	return &PublishHandler{
		guard:               guard,
		sessionRegistry:     options.SessionRegistry,
		serviceRegistry:     serviceRegistry,
		routeRegistry:       routeRegistry,
		metrics:             metrics,
		admission:           routeAdmission,
		hostDeriver:         options.HostDeriver,
		now:                 nowFunc,
		serviceIDGenerator:  serviceIDGenerator,
		instanceIDGenerator: instanceIDGenerator,
	}
}

// HandlePublish 处理 PublishService 控制消息。
func (handler *PublishHandler) HandlePublish(envelope pb.ControlEnvelope, message pb.PublishService) pb.PublishServiceAck {
	if err := validate.ValidateControlEnvelope(envelope); err != nil {
		return handler.rejectPublishAck(message, err)
	}
	if err := validate.ValidateNoLegacyFields(envelope.Payload, "serviceKey", "serviceId", "namespace", "environment"); err != nil {
		return handler.rejectPublishAck(message, err)
	}
	if err := validate.ValidatePublishService(message); err != nil {
		return handler.rejectPublishAck(message, err)
	}
	if err := handler.validateSessionEpoch(envelope); err != nil {
		return handler.rejectPublishAck(message, err)
	}
	normalizedMessage, err := handler.normalizePublishMessage(message)
	if err != nil {
		return handler.rejectPublishAck(message, err)
	}

	resolvedConnectorID := handler.resolveConnectorID(envelope)
	logicalService := handler.resolveOrCreateLogicalService(normalizedMessage, envelope.ResourceVersion)
	instance, err := handler.resolveOrCreateInstance(normalizedMessage, logicalService, resolvedConnectorID, envelope)
	if err != nil {
		return handler.rejectPublishAck(normalizedMessage, err)
	}
	resourceID := resolveServiceInstanceResourceID(instance.InstanceID, resolvedConnectorID, envelope.SessionID)
	decision := handler.guard.Evaluate(consistency.ResourceEventMeta{
		SessionID:       envelope.SessionID,
		SessionEpoch:    envelope.SessionEpoch,
		EventID:         envelope.EventID,
		ResourceType:    "service_instance",
		ResourceID:      resourceID,
		ResourceVersion: envelope.ResourceVersion,
	})
	if decision.Status == pb.EventStatusAccepted {
		autoRoute, shouldUpsertRoute, routeErr := handler.buildAutoRoute(normalizedMessage, logicalService, instance, envelope.ResourceVersion)
		if routeErr != nil {
			return handler.rejectPublishAck(normalizedMessage, routeErr)
		}
		if shouldUpsertRoute {
			warnings, metadata, admissionErr := handler.runAdmission(autoRoute)
			if admissionErr != nil {
				return handler.rejectPublishAck(normalizedMessage, admissionErr)
			}
			if len(warnings) > 0 || len(metadata) > 0 {
				slog.Warn(
					"auto route admission warnings",
					"route_id", strings.TrimSpace(autoRoute.RouteID),
					"logical_service_id", strings.TrimSpace(logicalService.LogicalServiceID),
					"warnings", warnings,
					"metadata", metadata,
				)
			}
		}
		handler.serviceRegistry.Upsert(handler.now(), logicalService, instance)
		if shouldUpsertRoute {
			handler.routeRegistry.Upsert(handler.now(), autoRoute)
		} else {
			handler.removeAutoRoute(logicalService.LogicalServiceID, logicalService.ServiceName, logicalService.Scope)
		}
		handler.metrics.ObserveBridgeServicePublish(logicalService.LogicalServiceID, instance.InstanceID)
		RefreshServiceAvailabilityMetrics(handler.metrics, handler.serviceRegistry, logicalService.LogicalServiceID)
	}
	handler.emitServiceResourceAuditLog(
		"publish",
		decision,
		logicalService.LogicalServiceID,
		instance.InstanceID,
		resolvedConnectorID,
		envelope.SessionID,
		envelope.SessionEpoch,
		envelope.ResourceVersion,
	)
	return consistency.BuildPublishServiceAck(
		decision.Status,
		logicalService.LogicalServiceID,
		instance.InstanceID,
		logicalService.ServiceName,
		logicalService.Scope,
		decision.AcceptedResourceVersion,
		decision.CurrentResourceVersion,
		decision.ErrorCode,
		decision.ErrorMessage,
	)
}

// HandleUnpublish 处理 UnpublishService 控制消息。
func (handler *PublishHandler) HandleUnpublish(envelope pb.ControlEnvelope, message pb.UnpublishService) pb.UnpublishServiceAck {
	if err := validate.ValidateControlEnvelope(envelope); err != nil {
		return handler.rejectUnpublishAck(message, err)
	}
	if err := validate.ValidateNoLegacyFields(envelope.Payload, "serviceKey", "serviceId", "namespace", "environment"); err != nil {
		return handler.rejectUnpublishAck(message, err)
	}
	if err := validate.ValidateUnpublishService(message); err != nil {
		return handler.rejectUnpublishAck(message, err)
	}
	if err := handler.validateSessionEpoch(envelope); err != nil {
		return handler.rejectUnpublishAck(message, err)
	}

	resolvedConnectorID := handler.resolveConnectorID(envelope)
	logicalServiceID, instanceID, resolveErr := handler.resolveUnpublishTarget(message, resolvedConnectorID, envelope.SessionID)
	if resolveErr != nil {
		return handler.rejectUnpublishAck(message, resolveErr)
	}
	resourceID := resolveServiceInstanceResourceID(instanceID, resolvedConnectorID, envelope.SessionID)
	if resourceID == "" {
		resourceID = strings.TrimSpace(logicalServiceID)
	}
	decision := handler.guard.Evaluate(consistency.ResourceEventMeta{
		SessionID:       envelope.SessionID,
		SessionEpoch:    envelope.SessionEpoch,
		EventID:         envelope.EventID,
		ResourceType:    "service_instance",
		ResourceID:      resourceID,
		ResourceVersion: envelope.ResourceVersion,
	})
	if decision.Status == pb.EventStatusAccepted {
		if strings.TrimSpace(instanceID) != "" {
			handler.serviceRegistry.RemoveInstanceByID(instanceID)
		} else if strings.TrimSpace(logicalServiceID) != "" {
			handler.serviceRegistry.RemoveInstanceByLogicalServiceAndRuntime(logicalServiceID, resolvedConnectorID, envelope.SessionID)
		}
		handler.reconcileAutoRouteOnUnpublish(logicalServiceID)
		RefreshServiceAvailabilityMetrics(handler.metrics, handler.serviceRegistry, logicalServiceID)
	}
	handler.emitServiceResourceAuditLog(
		"unpublish",
		decision,
		logicalServiceID,
		instanceID,
		resolvedConnectorID,
		envelope.SessionID,
		envelope.SessionEpoch,
		envelope.ResourceVersion,
	)
	return consistency.BuildUnpublishServiceAck(
		decision.Status,
		logicalServiceID,
		instanceID,
		decision.AcceptedResourceVersion,
		decision.CurrentResourceVersion,
		decision.ErrorCode,
		decision.ErrorMessage,
	)
}

// ReconcileFromFullSync 使用 full-sync 快照重建服务视图。
func (handler *PublishHandler) ReconcileFromFullSync(snapshot pb.FullSyncSnapshot) {
	if !snapshot.Completed {
		return
	}
	handler.serviceRegistry.ReplaceAll(handler.now(), snapshot.LogicalServices, snapshot.ServiceInstances)
	versionSnapshot := make(map[string]uint64, len(snapshot.LogicalServices)+len(snapshot.ServiceInstances)*2)
	for _, logicalService := range snapshot.LogicalServices {
		if strings.TrimSpace(logicalService.LogicalServiceID) == "" {
			continue
		}
		versionSnapshot["logical_service:"+strings.TrimSpace(logicalService.LogicalServiceID)] = logicalService.ResourceVersion
	}
	for _, instance := range snapshot.ServiceInstances {
		if strings.TrimSpace(instance.InstanceID) == "" {
			continue
		}
		versionSnapshot["service_instance:"+strings.TrimSpace(instance.InstanceID)] = instance.ResourceVersion
	}
	handler.guard.ReplaceAllVersions(versionSnapshot)
}

// rejectPublishAck 构造发布拒绝 ACK。
func (handler *PublishHandler) rejectPublishAck(message pb.PublishService, err error) pb.PublishServiceAck {
	logicalServiceID, currentVersion := handler.resolveCurrentPublishVersion(message)
	return consistency.BuildPublishServiceAck(
		pb.EventStatusRejected,
		logicalServiceID,
		strings.TrimSpace(message.InstanceID),
		strings.TrimSpace(message.ServiceName),
		message.Scope,
		currentVersion,
		currentVersion,
		extractErrorCode(err),
		err.Error(),
	)
}

// rejectUnpublishAck 构造下线拒绝 ACK。
func (handler *PublishHandler) rejectUnpublishAck(message pb.UnpublishService, err error) pb.UnpublishServiceAck {
	currentVersion := handler.serviceRegistry.CurrentVersion(message.LogicalServiceID, message.InstanceID)
	return consistency.BuildUnpublishServiceAck(
		pb.EventStatusRejected,
		strings.TrimSpace(message.LogicalServiceID),
		strings.TrimSpace(message.InstanceID),
		currentVersion,
		currentVersion,
		extractErrorCode(err),
		err.Error(),
	)
}

// resolveCurrentPublishVersion 解析发布请求对应的逻辑服务当前版本。
func (handler *PublishHandler) resolveCurrentPublishVersion(message pb.PublishService) (string, uint64) {
	if handler == nil || handler.serviceRegistry == nil {
		return "", 0
	}
	if strings.TrimSpace(message.InstanceID) != "" {
		if instanceSnapshot, exists := handler.serviceRegistry.GetInstanceByID(message.InstanceID); exists {
			return instanceSnapshot.Instance.LogicalServiceID, instanceSnapshot.Instance.ResourceVersion
		}
	}
	if logicalService, exists := handler.serviceRegistry.FindLogicalServiceByNameScope(message.ServiceName, message.Scope); exists {
		return logicalService.LogicalServiceID, logicalService.ResourceVersion
	}
	return "", 0
}

// resolveOrCreateLogicalService 解析或构造逻辑服务快照。
func (handler *PublishHandler) resolveOrCreateLogicalService(message pb.PublishService, resourceVersion uint64) pb.LogicalService {
	if logicalService, exists := handler.serviceRegistry.FindLogicalServiceByNameScope(message.ServiceName, message.Scope); exists {
		logicalService.Labels = cloneStringMap(message.Labels)
		logicalService.Metadata = cloneStringMap(message.Metadata)
		logicalService.ResourceVersion = resourceVersion
		return logicalService
	}
	return pb.LogicalService{
		LogicalServiceID: handler.serviceIDGenerator(handler.now(), buildLogicalServiceKey(message.ServiceName, message.Scope)),
		ServiceName:      strings.TrimSpace(message.ServiceName),
		Scope:            message.Scope,
		Status:           pb.ServiceStatusActive,
		Labels:           cloneStringMap(message.Labels),
		Metadata:         cloneStringMap(message.Metadata),
		ResourceVersion:  resourceVersion,
	}
}

// resolveOrCreateInstance 解析或构造服务实例快照。
func (handler *PublishHandler) resolveOrCreateInstance(
	message pb.PublishService,
	logicalService pb.LogicalService,
	connectorID string,
	envelope pb.ControlEnvelope,
) (pb.ServiceInstance, error) {
	instanceHint := message.InstanceID
	normalizedInstanceHint := strings.TrimSpace(instanceHint)
	normalizedConnectorID := strings.TrimSpace(connectorID)
	if normalizedInstanceHint != "" {
		if instanceSnapshot, exists := handler.serviceRegistry.GetInstanceByID(normalizedInstanceHint); exists {
			if strings.TrimSpace(instanceSnapshot.Instance.ConnectorID) != normalizedConnectorID {
				return pb.ServiceInstance{}, ltfperrors.New(ltfperrors.CodeInstanceOwnershipMismatch, "instance_id is owned by a different connector")
			}
			if strings.TrimSpace(instanceSnapshot.Instance.LogicalServiceID) != strings.TrimSpace(logicalService.LogicalServiceID) {
				return pb.ServiceInstance{}, ltfperrors.New(ltfperrors.CodeInstanceOwnershipMismatch, "instance_id belongs to a different logical service")
			}
			instanceSnapshot.Instance.SessionID = strings.TrimSpace(envelope.SessionID)
			instanceSnapshot.Instance.SessionEpoch = envelope.SessionEpoch
			instanceSnapshot.Instance.InstanceStatus = pb.ServiceStatusActive
			instanceSnapshot.Instance.ResourceVersion = envelope.ResourceVersion
			applyPublishPayloadToInstance(&instanceSnapshot.Instance, message)
			return instanceSnapshot.Instance, nil
		}
	}
	if instanceSnapshot, exists := handler.serviceRegistry.FindInstanceByConnectorLogicalService(normalizedConnectorID, logicalService.LogicalServiceID); exists {
		instanceSnapshot.Instance.SessionID = strings.TrimSpace(envelope.SessionID)
		instanceSnapshot.Instance.SessionEpoch = envelope.SessionEpoch
		instanceSnapshot.Instance.InstanceStatus = pb.ServiceStatusActive
		instanceSnapshot.Instance.ResourceVersion = envelope.ResourceVersion
		applyPublishPayloadToInstance(&instanceSnapshot.Instance, message)
		return instanceSnapshot.Instance, nil
	}
	instanceID := normalizedInstanceHint
	if instanceID == "" {
		instanceID = handler.instanceIDGenerator(handler.now(), logicalService.LogicalServiceID, normalizedConnectorID)
	}
	instance := pb.ServiceInstance{
		InstanceID:       instanceID,
		LogicalServiceID: logicalService.LogicalServiceID,
		ConnectorID:      normalizedConnectorID,
		SessionID:        strings.TrimSpace(envelope.SessionID),
		SessionEpoch:     envelope.SessionEpoch,
		InstanceStatus:   pb.ServiceStatusActive,
		HealthStatus:     pb.HealthStatusUnknown,
		ResourceVersion:  envelope.ResourceVersion,
	}
	applyPublishPayloadToInstance(&instance, message)
	return instance, nil
}

// resolveUnpublishTarget 解析下线路径对应的 logical_service_id 与 instance_id。
func (handler *PublishHandler) resolveUnpublishTarget(
	message pb.UnpublishService,
	connectorID string,
	sessionID string,
) (string, string, error) {
	normalizedInstanceID := strings.TrimSpace(message.InstanceID)
	if normalizedInstanceID != "" {
		if instanceSnapshot, exists := handler.serviceRegistry.GetInstanceByID(normalizedInstanceID); exists {
			normalizedConnectorID := strings.TrimSpace(connectorID)
			if normalizedConnectorID != "" &&
				strings.TrimSpace(instanceSnapshot.Instance.ConnectorID) != normalizedConnectorID {
				return "", "", ltfperrors.New(
					ltfperrors.CodeInstanceOwnershipMismatch,
					"instance_id is owned by a different connector",
				)
			}
			normalizedSessionID := strings.TrimSpace(sessionID)
			if normalizedSessionID != "" &&
				strings.TrimSpace(instanceSnapshot.Instance.SessionID) != "" &&
				strings.TrimSpace(instanceSnapshot.Instance.SessionID) != normalizedSessionID {
				return "", "", ltfperrors.New(
					ltfperrors.CodeInstanceOwnershipMismatch,
					"instance_id is owned by a different session",
				)
			}
			normalizedLogicalServiceID := strings.TrimSpace(message.LogicalServiceID)
			if normalizedLogicalServiceID != "" &&
				strings.TrimSpace(instanceSnapshot.Instance.LogicalServiceID) != normalizedLogicalServiceID {
				return "", "", ltfperrors.New(
					ltfperrors.CodeInstanceOwnershipMismatch,
					"instance_id belongs to a different logical service",
				)
			}
			return strings.TrimSpace(instanceSnapshot.Instance.LogicalServiceID), normalizedInstanceID, nil
		}
		return strings.TrimSpace(message.LogicalServiceID), normalizedInstanceID, nil
	}
	normalizedLogicalServiceID := strings.TrimSpace(message.LogicalServiceID)
	if normalizedLogicalServiceID == "" {
		if logicalService, exists := handler.serviceRegistry.FindLogicalServiceByNameScope(message.ServiceName, message.Scope); exists {
			normalizedLogicalServiceID = strings.TrimSpace(logicalService.LogicalServiceID)
		}
	}
	if normalizedLogicalServiceID != "" {
		if instanceSnapshot, exists := handler.serviceRegistry.FindInstanceByConnectorLogicalService(connectorID, normalizedLogicalServiceID); exists {
			if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(instanceSnapshot.Instance.SessionID) == strings.TrimSpace(sessionID) {
				return normalizedLogicalServiceID, strings.TrimSpace(instanceSnapshot.Instance.InstanceID), nil
			}
		}
	}
	return normalizedLogicalServiceID, "", nil
}

// resolveConnectorID 提取发布事件归属的 connector_id。
func (handler *PublishHandler) resolveConnectorID(envelope pb.ControlEnvelope) string {
	normalizedConnectorID := strings.TrimSpace(envelope.ConnectorID)
	if normalizedConnectorID != "" {
		return normalizedConnectorID
	}
	if handler.sessionRegistry == nil {
		return ""
	}
	sessionSnapshot, exists := handler.sessionRegistry.GetBySession(envelope.SessionID)
	if !exists {
		return ""
	}
	return strings.TrimSpace(sessionSnapshot.ConnectorID)
}

func (handler *PublishHandler) normalizePublishMessage(message pb.PublishService) (pb.PublishService, error) {
	normalizedMessage := message
	normalizedMessage.ServiceName = strings.TrimSpace(message.ServiceName)
	normalizedMessage.Scope = normalizeScope(message.Scope)
	normalizedMessage.Endpoints = cloneServiceEndpoints(message.Endpoints)
	normalizedMessage.Exposure = cloneServiceExposure(message.Exposure)
	normalizedMessage.HealthCheck = message.HealthCheck
	normalizedMessage.DiscoveryPolicy = cloneDiscoveryPolicy(message.DiscoveryPolicy)
	normalizedMessage.RouteHint = cloneRouteHint(message.RouteHint)
	normalizedMessage.Labels = cloneStringMap(message.Labels)
	normalizedMessage.Metadata = cloneStringMap(message.Metadata)
	normalizedMessage.Exposure.Host = strings.ToLower(strings.TrimSpace(normalizedMessage.Exposure.Host))
	if !shouldDerivePublishHost(normalizedMessage) || normalizedMessage.Exposure.Host != "" {
		return normalizedMessage, nil
	}
	if handler == nil || handler.hostDeriver == nil {
		return normalizedMessage, nil
	}
	derivedHost, err := handler.hostDeriver.Derive(normalizedMessage.ServiceName, normalizedMessage.Scope)
	if err != nil {
		return pb.PublishService{}, err
	}
	normalizedMessage.Exposure.Host = derivedHost
	return normalizedMessage, nil
}

func shouldDerivePublishHost(message pb.PublishService) bool {
	if strings.TrimSpace(message.Exposure.Host) != "" {
		return false
	}
	switch message.Exposure.IngressMode {
	case "", pb.IngressModeL7Shared:
	default:
		return false
	}
	switch strings.ToLower(strings.TrimSpace(message.ServiceType)) {
	case "http", "https", "grpc", "grpc_h2", "grpc-h2":
		return true
	default:
		return false
	}
}

func applyPublishPayloadToInstance(instance *pb.ServiceInstance, message pb.PublishService) {
	if instance == nil {
		return
	}
	instance.Endpoints = cloneServiceEndpoints(message.Endpoints)
	instance.Exposure = cloneServiceExposure(message.Exposure)
	instance.HealthCheck = message.HealthCheck
	instance.DiscoveryPolicy = cloneDiscoveryPolicy(message.DiscoveryPolicy)
	instance.RouteHint = cloneRouteHint(message.RouteHint)
	instance.Labels = cloneStringMap(message.Labels)
	instance.Metadata = cloneStringMap(message.Metadata)
}

func cloneServiceEndpoints(endpoints []pb.ServiceEndpoint) []pb.ServiceEndpoint {
	if len(endpoints) == 0 {
		return nil
	}
	return append([]pb.ServiceEndpoint(nil), endpoints...)
}

func cloneServiceExposure(exposure pb.ServiceExposure) pb.ServiceExposure {
	return pb.ServiceExposure{
		IngressMode: exposure.IngressMode,
		Host:        strings.ToLower(strings.TrimSpace(exposure.Host)),
		ListenPort:  exposure.ListenPort,
		SNIName:     strings.TrimSpace(exposure.SNIName),
		PathPrefix:  strings.TrimSpace(exposure.PathPrefix),
		AllowExport: exposure.AllowExport,
	}
}

func cloneDiscoveryPolicy(policy pb.DiscoveryPolicy) pb.DiscoveryPolicy {
	return pb.DiscoveryPolicy{
		Enabled:      policy.Enabled,
		Providers:    append([]string(nil), policy.Providers...),
		ExternalName: strings.TrimSpace(policy.ExternalName),
		Namespace:    strings.TrimSpace(policy.Namespace),
		Group:        strings.TrimSpace(policy.Group),
		Tags:         cloneStringMap(policy.Tags),
		Metadata:     cloneStringMap(policy.Metadata),
	}
}

func cloneRouteHint(routeHint pb.RouteHint) pb.RouteHint {
	return pb.RouteHint{
		MatchHeaders: cloneHeaderMatchers(routeHint.MatchHeaders),
		MatchQueries: cloneQueryMatchers(routeHint.MatchQueries),
		Priority:     routeHint.Priority,
	}
}

func cloneHeaderMatchers(matchers []pb.HeaderMatcher) []pb.HeaderMatcher {
	if len(matchers) == 0 {
		return nil
	}
	cloned := make([]pb.HeaderMatcher, 0, len(matchers))
	for _, matcher := range matchers {
		cloned = append(cloned, pb.HeaderMatcher{
			Name:    strings.TrimSpace(matcher.Name),
			Exact:   strings.TrimSpace(matcher.Exact),
			Prefix:  strings.TrimSpace(matcher.Prefix),
			Regex:   strings.TrimSpace(matcher.Regex),
			Present: matcher.Present,
		})
	}
	return cloned
}

func cloneQueryMatchers(matchers []pb.QueryMatcher) []pb.QueryMatcher {
	if len(matchers) == 0 {
		return nil
	}
	cloned := make([]pb.QueryMatcher, 0, len(matchers))
	for _, matcher := range matchers {
		cloned = append(cloned, pb.QueryMatcher{
			Name:    strings.TrimSpace(matcher.Name),
			Exact:   strings.TrimSpace(matcher.Exact),
			Prefix:  strings.TrimSpace(matcher.Prefix),
			Regex:   strings.TrimSpace(matcher.Regex),
			Present: matcher.Present,
		})
	}
	return cloned
}

func normalizeScope(scope pb.Scope) pb.Scope {
	return pb.Scope{
		Namespace:   strings.TrimSpace(scope.Namespace),
		Environment: strings.TrimSpace(scope.Environment),
	}
}

// resolveServiceInstanceResourceID 解析实例维度资源键。
func resolveServiceInstanceResourceID(instanceID string, connectorID string, sessionID string) string {
	normalizedInstanceID := strings.TrimSpace(instanceID)
	if normalizedInstanceID != "" {
		return normalizedInstanceID
	}
	return strings.TrimSpace(connectorID) + "|" + strings.TrimSpace(sessionID)
}

// buildLogicalServiceKey 构造逻辑服务唯一键。
func buildLogicalServiceKey(serviceName string, scope pb.Scope) string {
	return strings.TrimSpace(scope.Namespace) + "|" + strings.TrimSpace(scope.Environment) + "|" + strings.TrimSpace(serviceName)
}

// defaultPublishLogicalServiceIDGenerator 生成发布路径默认 logical_service_id。
func defaultPublishLogicalServiceIDGenerator(now time.Time, _ string) string {
	normalizedNow := now.UTC()
	if normalizedNow.IsZero() {
		normalizedNow = time.Now().UTC()
	}
	sequence := atomic.AddUint64(&publishServiceIDSequence, 1)
	return fmt.Sprintf("ls-%d-%d", normalizedNow.UnixNano(), sequence)
}

// defaultPublishInstanceIDGenerator 生成发布路径默认 instance_id。
func defaultPublishInstanceIDGenerator(now time.Time, _ string, _ string) string {
	normalizedNow := now.UTC()
	if normalizedNow.IsZero() {
		normalizedNow = time.Now().UTC()
	}
	sequence := atomic.AddUint64(&publishInstanceIDSequence, 1)
	return fmt.Sprintf("si-%d-%d", normalizedNow.UnixNano(), sequence)
}

func (handler *PublishHandler) buildAutoRoute(
	message pb.PublishService,
	logicalService pb.LogicalService,
	instance pb.ServiceInstance,
	resourceVersion uint64,
) (pb.Route, bool, error) {
	routeID := buildPublishAutoRouteID(logicalService.LogicalServiceID, logicalService.ServiceName, logicalService.Scope)
	if routeID == "" {
		return pb.Route{}, false, nil
	}
	if err := validate.ValidateRouteHint(message.RouteHint); err != nil {
		return pb.Route{}, false, err
	}
	match, ingressMode, shouldUpsertRoute, err := buildPublishAutoRouteMatch(message)
	if err != nil {
		return pb.Route{}, false, err
	}
	if !shouldUpsertRoute {
		return pb.Route{}, false, nil
	}
	return pb.Route{
		RouteID:         routeID,
		Scope:           logicalService.Scope,
		ResourceVersion: resourceVersion,
		Match:           match,
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeConnectorService,
			ConnectorService: &pb.ConnectorServiceTarget{
				Selector: pb.ServiceSelector{
					LogicalServiceID: strings.TrimSpace(logicalService.LogicalServiceID),
					ServiceName:      strings.TrimSpace(logicalService.ServiceName),
					Scope:            logicalService.Scope,
				},
			},
		},
		Priority: message.RouteHint.Priority,
		Metadata: map[string]string{
			"source":             "bridge.auto_route",
			"logical_service_id": strings.TrimSpace(logicalService.LogicalServiceID),
			"instance_id":        strings.TrimSpace(instance.InstanceID),
			"service_name":       strings.TrimSpace(logicalService.ServiceName),
			"ingress_mode":       string(ingressMode),
		},
	}, true, nil
}

func (handler *PublishHandler) reconcileAutoRouteOnUnpublish(logicalServiceID string) {
	normalizedLogicalServiceID := strings.TrimSpace(logicalServiceID)
	if normalizedLogicalServiceID == "" || handler == nil || handler.serviceRegistry == nil {
		return
	}
	logicalService, exists := handler.serviceRegistry.GetLogicalServiceByID(normalizedLogicalServiceID)
	if !exists || logicalService.ActiveInstanceCount > 0 {
		return
	}
	handler.removeAutoRoute(logicalService.LogicalServiceID, logicalService.ServiceName, logicalService.Scope)
}

func (handler *PublishHandler) removeAutoRoute(logicalServiceID string, serviceName string, scope pb.Scope) {
	if handler == nil || handler.routeRegistry == nil {
		return
	}
	routeID := buildPublishAutoRouteID(logicalServiceID, serviceName, scope)
	if routeID == "" {
		return
	}
	handler.routeRegistry.Remove(routeID)
}

func (handler *PublishHandler) runAdmission(route pb.Route) ([]string, map[string]string, error) {
	if handler == nil || handler.admission == nil {
		return nil, nil, nil
	}
	warnings, metadata, err := handler.admission.Admit(route)
	if err != nil {
		if handler.metrics != nil {
			handler.metrics.IncBridgeRouteConflictRejectionTotal()
		}
		conflictRouteID := admission.ExtractConflictRouteID(err)
		if metadata == nil && conflictRouteID != "" {
			metadata = map[string]string{"conflict_route_id": conflictRouteID}
		}
		if !ltfperrors.IsCode(err, ltfperrors.CodeIngressRouteMismatch) {
			err = ltfperrors.New(ltfperrors.CodeIngressRouteMismatch, err.Error())
		}
		return nil, metadata, err
	}
	return warnings, metadata, nil
}

func normalizePublishAutoRouteProtocol(message pb.PublishService) string {
	serviceProtocol := strings.ToLower(strings.TrimSpace(message.ServiceType))
	if serviceProtocol == "" {
		for _, endpoint := range message.Endpoints {
			serviceProtocol = strings.ToLower(strings.TrimSpace(endpoint.Protocol))
			if serviceProtocol != "" {
				break
			}
		}
	}
	switch serviceProtocol {
	case "http", "https":
		return "http"
	case "grpc", "grpc_h2", "grpc-h2":
		return "grpc"
	default:
		return ""
	}
}

func normalizePublishAutoRouteIngressMode(message pb.PublishService) pb.IngressMode {
	switch normalizedMode := pb.IngressMode(strings.TrimSpace(string(message.Exposure.IngressMode))); normalizedMode {
	case "", pb.IngressModeL7Shared:
		return pb.IngressModeL7Shared
	case pb.IngressModeTLSSNIShared:
		return pb.IngressModeTLSSNIShared
	case pb.IngressModeL4DedicatedPort:
		return pb.IngressModeL4DedicatedPort
	default:
		return normalizedMode
	}
}

func buildPublishAutoRouteMatch(message pb.PublishService) (pb.RouteMatch, pb.IngressMode, bool, error) {
	normalizedIngressMode := normalizePublishAutoRouteIngressMode(message)
	switch normalizedIngressMode {
	case pb.IngressModeL7Shared:
		normalizedProtocol := normalizePublishAutoRouteProtocol(message)
		if normalizedProtocol == "" {
			return pb.RouteMatch{}, "", false, nil
		}
		pathPrefix := strings.TrimSpace(message.Exposure.PathPrefix)
		if pathPrefix == "" {
			pathPrefix = "/"
		}
		return pb.RouteMatch{
			Protocol:   normalizedProtocol,
			Host:       strings.ToLower(strings.TrimSpace(message.Exposure.Host)),
			PathPrefix: pathPrefix,
			Headers:    cloneHeaderMatchers(message.RouteHint.MatchHeaders),
			Queries:    cloneQueryMatchers(message.RouteHint.MatchQueries),
		}, normalizedIngressMode, true, nil
	case pb.IngressModeTLSSNIShared:
		normalizedSNIName := strings.ToLower(strings.TrimSpace(message.Exposure.SNIName))
		if normalizedSNIName == "" {
			return pb.RouteMatch{}, "", false, ltfperrors.New(
				ltfperrors.CodeMissingRequiredField,
				"exposure.sni_name is required for tls_sni_shared",
			)
		}
		return pb.RouteMatch{
			Protocol:   "tls",
			ListenPort: message.Exposure.ListenPort,
			SNI:        normalizedSNIName,
		}, normalizedIngressMode, true, nil
	case pb.IngressModeL4DedicatedPort:
		if message.Exposure.ListenPort == 0 {
			return pb.RouteMatch{}, "", false, ltfperrors.New(
				ltfperrors.CodeMissingRequiredField,
				"exposure.listen_port is required for l4_dedicated_port",
			)
		}
		return pb.RouteMatch{
			Protocol:   "tcp",
			ListenPort: message.Exposure.ListenPort,
		}, normalizedIngressMode, true, nil
	default:
		return pb.RouteMatch{}, "", false, ltfperrors.New(
			ltfperrors.CodeUnsupportedValue,
			fmt.Sprintf("unsupported exposure.ingress_mode=%s", normalizedIngressMode),
		)
	}
}

func buildPublishAutoRouteID(logicalServiceID string, serviceName string, scope pb.Scope) string {
	resourceID := strings.TrimSpace(logicalServiceID)
	if resourceID == "" {
		resourceID = buildLogicalServiceKey(serviceName, scope)
	}
	if resourceID == "" {
		return ""
	}
	sanitizedResourceID := strings.NewReplacer(
		"/", "-",
		"\\", "-",
		":", "-",
		".", "-",
		" ", "-",
	).Replace(resourceID)
	return "agent-auto-route-" + sanitizedResourceID
}

// emitServiceResourceAuditLog 输出 service 资源变更审计日志。
func (handler *PublishHandler) emitServiceResourceAuditLog(
	action string,
	decision consistency.ResourceEventDecision,
	logicalServiceID string,
	instanceID string,
	connectorID string,
	sessionID string,
	sessionEpoch uint64,
	resourceVersion uint64,
) {
	slog.Info(
		"service resource audit",
		"action", strings.TrimSpace(action),
		"event_status", decision.Status,
		"error_code", strings.TrimSpace(decision.ErrorCode),
		"logical_service_id", strings.TrimSpace(logicalServiceID),
		"instance_id", strings.TrimSpace(instanceID),
		"connector_id", strings.TrimSpace(connectorID),
		"session_id", strings.TrimSpace(sessionID),
		"session_epoch", sessionEpoch,
		"resource_version", resourceVersion,
		"accepted_resource_version", decision.AcceptedResourceVersion,
		"current_resource_version", decision.CurrentResourceVersion,
	)
}

// extractErrorCode 提取协议错误码，不存在时回退到 INVALID_PAYLOAD。
func extractErrorCode(err error) string {
	code := ltfperrors.ExtractCode(err)
	if code != "" {
		return code
	}
	return ltfperrors.CodeInvalidPayload
}

// validateSessionEpoch 校验资源事件是否来自当前有效 session 代际。
func (handler *PublishHandler) validateSessionEpoch(envelope pb.ControlEnvelope) error {
	if handler.sessionRegistry == nil {
		return nil
	}
	sessionRuntime, exists := handler.sessionRegistry.GetBySession(envelope.SessionID)
	if !exists {
		return ltfperrors.New(ltfperrors.CodeStaleSessionEpoch, "session not found for resource event")
	}
	if sessionRuntime.Epoch != envelope.SessionEpoch {
		return ltfperrors.New(ltfperrors.CodeStaleSessionEpoch, "session epoch mismatch for resource event")
	}
	if sessionRuntime.State != registry.SessionActive {
		return ltfperrors.New(ltfperrors.CodeInvalidStateTransition, "session state does not allow resource mutation")
	}
	return nil
}

// cloneStringMap 深拷贝字符串 map。
func cloneStringMap(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}
