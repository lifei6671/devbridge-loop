package grpcbinding

import (
	"context"
	"errors"
	"testing"

	"github.com/lifei6671/devbridge-loop/ltfp/transport"
)

type noopControlChannel struct {
	doneChannel chan struct{}
}

func (channel *noopControlChannel) WriteControlFrame(context.Context, transport.ControlFrame) error {
	return nil
}

func (channel *noopControlChannel) ReadControlFrame(context.Context) (transport.ControlFrame, error) {
	return transport.ControlFrame{}, nil
}

func (channel *noopControlChannel) Close(context.Context) error {
	return nil
}

func (channel *noopControlChannel) Done() <-chan struct{} {
	return channel.doneChannel
}

func (channel *noopControlChannel) Err() error {
	return nil
}

type noopTunnelProducer struct{}

func (producer *noopTunnelProducer) OpenTunnel(context.Context) (transport.Tunnel, error) {
	return nil, transport.ErrUnsupported
}

type noopTunnelAcceptor struct{}

func (acceptor *noopTunnelAcceptor) AcceptTunnel(context.Context) (transport.Tunnel, error) {
	return nil, transport.ErrUnsupported
}

// TestNewSessionRoleValidation 验证角色配置约束。
func TestNewSessionRoleValidation(testingObject *testing.T) {
	_, err := NewSession("unknown", SessionConfig{
		Meta: transport.SessionMeta{SessionID: "session-role"},
	})
	if err == nil {
		testingObject.Fatalf("expected unknown role error")
	}
	if !errors.Is(err, transport.ErrInvalidArgument) {
		testingObject.Fatalf("expected ErrInvalidArgument, got %v", err)
	}

	_, err = NewSession(SessionRoleAgent, SessionConfig{
		Meta: transport.SessionMeta{SessionID: "agent-no-producer"},
	})
	if !errors.Is(err, transport.ErrInvalidArgument) {
		testingObject.Fatalf("expected invalid argument for missing producer, got %v", err)
	}

	_, err = NewSession(SessionRoleServer, SessionConfig{
		Meta:           transport.SessionMeta{SessionID: "server-no-acceptor"},
		TunnelPool:     transport.NewInMemoryTunnelPool(),
		ControlChannel: &noopControlChannel{doneChannel: make(chan struct{})},
	})
	if !errors.Is(err, transport.ErrInvalidArgument) {
		testingObject.Fatalf("expected invalid argument for missing acceptor, got %v", err)
	}
}

// TestNewSessionBuildsServerSession 验证构造出的 server session 能提供 grpc 绑定能力。
func TestNewSessionBuildsServerSession(testingObject *testing.T) {
	session, err := NewSession(SessionRoleServer, SessionConfig{
		Meta:           transport.SessionMeta{SessionID: "server-session"},
		ControlChannel: &noopControlChannel{doneChannel: make(chan struct{})},
		TunnelAcceptor: &noopTunnelAcceptor{},
		TunnelPool:     transport.NewInMemoryTunnelPool(),
	})
	if err != nil {
		testingObject.Fatalf("create server session failed: %v", err)
	}
	if session.BindingInfo().Type != transport.BindingTypeGRPCH2 {
		testingObject.Fatalf("expected grpc_h2 binding type, got %s", session.BindingInfo().Type)
	}
	if err := session.Open(context.Background()); err != nil {
		testingObject.Fatalf("open session failed: %v", err)
	}
	if _, err := session.TunnelAcceptor(); err != nil {
		testingObject.Fatalf("expected tunnel acceptor capability, got %v", err)
	}
	if _, err := session.TunnelPool(); err != nil {
		testingObject.Fatalf("expected tunnel pool capability, got %v", err)
	}
}
