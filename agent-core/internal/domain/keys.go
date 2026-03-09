package domain

import (
	"fmt"
	"strings"
)

// ServiceKey identifies one logical service in one env.
type ServiceKey struct {
	Env         string
	ServiceName string
}

// NewServiceKey constructs a normalized service key.
func NewServiceKey(env, serviceName string) ServiceKey {
	return ServiceKey{
		Env:         canonicalName(env),
		ServiceName: canonicalName(serviceName),
	}
}

// String returns stable string representation for map indexing.
func (k ServiceKey) String() string {
	return fmt.Sprintf("%s|%s", canonicalName(k.Env), canonicalName(k.ServiceName))
}

// InstanceKey identifies one service instance.
type InstanceKey struct {
	ServiceKey
	InstanceID string
}

// NewInstanceKey constructs a normalized instance key.
func NewInstanceKey(env, serviceName, instanceID string) InstanceKey {
	return InstanceKey{
		ServiceKey: NewServiceKey(env, serviceName),
		InstanceID: canonicalID(instanceID),
	}
}

// String returns stable string representation for map indexing.
func (k InstanceKey) String() string {
	return fmt.Sprintf("%s|%s", k.ServiceKey.String(), canonicalID(k.InstanceID))
}

// EndpointKey identifies one protocol endpoint in one instance.
type EndpointKey struct {
	InstanceKey
	Protocol   string
	ListenPort int
}

// NewEndpointKey constructs a normalized endpoint key.
func NewEndpointKey(env, serviceName, instanceID, protocol string, listenPort int) EndpointKey {
	return EndpointKey{
		InstanceKey: NewInstanceKey(env, serviceName, instanceID),
		Protocol:    canonicalName(protocol),
		ListenPort:  listenPort,
	}
}

// String returns stable string representation for map indexing.
func (k EndpointKey) String() string {
	return fmt.Sprintf("%s|%s|%d", k.InstanceKey.String(), canonicalName(k.Protocol), k.ListenPort)
}

func canonicalName(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func canonicalID(value string) string {
	return strings.TrimSpace(value)
}
