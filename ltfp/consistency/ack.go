package consistency

import "github.com/lifei6671/devbridge-loop/ltfp/pb"

// BuildPublishServiceAck 构造 PublishServiceAck 的统一语义响应。
func BuildPublishServiceAck(status pb.EventStatus, serviceID string, serviceKey string, acceptedVersion uint64, currentVersion uint64, errorCode string, errorMessage string) pb.PublishServiceAck {
	// accepted 字段由事件状态派生，duplicate 也视为幂等成功。
	accepted := status == pb.EventStatusAccepted || status == pb.EventStatusDuplicate
	return pb.PublishServiceAck{
		Accepted:                accepted,
		ServiceID:               serviceID,
		ServiceKey:              serviceKey,
		AcceptedResourceVersion: acceptedVersion,
		CurrentResourceVersion:  currentVersion,
		ErrorCode:               errorCode,
		ErrorMessage:            errorMessage,
	}
}

// BuildUnpublishServiceAck 构造 UnpublishServiceAck 的统一语义响应。
func BuildUnpublishServiceAck(status pb.EventStatus, serviceID string, serviceKey string, acceptedVersion uint64, currentVersion uint64, errorCode string, errorMessage string) pb.UnpublishServiceAck {
	// accepted 字段由事件状态派生，duplicate 也视为幂等成功。
	accepted := status == pb.EventStatusAccepted || status == pb.EventStatusDuplicate
	return pb.UnpublishServiceAck{
		Accepted:                accepted,
		ServiceID:               serviceID,
		ServiceKey:              serviceKey,
		AcceptedResourceVersion: acceptedVersion,
		CurrentResourceVersion:  currentVersion,
		ErrorCode:               errorCode,
		ErrorMessage:            errorMessage,
	}
}

// BuildRouteAssignAck 构造 RouteAssignAck 的统一语义响应。
func BuildRouteAssignAck(status pb.EventStatus, routeID string, acceptedVersion uint64, currentVersion uint64, errorCode string, errorMessage string) pb.RouteAssignAck {
	// accepted 字段由事件状态派生，duplicate 也视为幂等成功。
	accepted := status == pb.EventStatusAccepted || status == pb.EventStatusDuplicate
	return pb.RouteAssignAck{
		Accepted:                accepted,
		RouteID:                 routeID,
		AcceptedResourceVersion: acceptedVersion,
		CurrentResourceVersion:  currentVersion,
		ErrorCode:               errorCode,
		ErrorMessage:            errorMessage,
	}
}

// BuildRouteRevokeAck 构造 RouteRevokeAck 的统一语义响应。
func BuildRouteRevokeAck(status pb.EventStatus, routeID string, acceptedVersion uint64, currentVersion uint64, errorCode string, errorMessage string) pb.RouteRevokeAck {
	// accepted 字段由事件状态派生，duplicate 也视为幂等成功。
	accepted := status == pb.EventStatusAccepted || status == pb.EventStatusDuplicate
	return pb.RouteRevokeAck{
		Accepted:                accepted,
		RouteID:                 routeID,
		AcceptedResourceVersion: acceptedVersion,
		CurrentResourceVersion:  currentVersion,
		ErrorCode:               errorCode,
		ErrorMessage:            errorMessage,
	}
}
