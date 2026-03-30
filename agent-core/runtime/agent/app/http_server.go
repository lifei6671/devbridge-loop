package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"

	"github.com/lifei6671/devbridge-loop/agent-core/runtime/agent/hostapi"
	"github.com/lifei6671/devbridge-loop/agent-core/runtime/agent/httpapi"
	agentweb "github.com/lifei6671/devbridge-loop/agent-core/web"
)

type httpAgentServer struct {
	handler    http.Handler
	listenAddr string

	closeMu sync.Mutex
	closed  bool
	server  *http.Server
}

func newHTTPServer(runtimeInstance *Runtime) (*httpAgentServer, error) {
	if runtimeInstance == nil {
		return nil, errors.New("runtime is nil")
	}
	if !runtimeInstance.cfg.UI.Web.Enabled {
		return nil, nil
	}
	hostHandler := hostapi.NewService(newRuntimeHostAPI(runtimeInstance, "", ""))
	httpHandler, err := httpapi.NewServer(httpapi.ServerOptions{
		BasePath:          runtimeInstance.cfg.UI.Web.BasePath,
		SessionCookieName: runtimeInstance.cfg.UI.Web.SessionCookieName,
		Username:          runtimeInstance.cfg.UI.Web.Auth.Username,
		Password:          runtimeInstance.cfg.UI.Web.Auth.Password,
		Handler:           hostHandler,
		UIHandler:         agentweb.Handler(),
	})
	if err != nil {
		return nil, fmt.Errorf("new http server: %w", err)
	}
	return &httpAgentServer{
		handler:    httpHandler,
		listenAddr: runtimeInstance.cfg.UI.Web.ListenAddr,
		server: &http.Server{
			Handler: httpHandler,
		},
	}, nil
}

func (server *httpAgentServer) Serve(ctx context.Context) error {
	if server == nil {
		return nil
	}
	listener, err := net.Listen("tcp", server.listenAddr)
	if err != nil {
		return fmt.Errorf("http api listen failed: %w", err)
	}
	go func() {
		<-ctx.Done()
		_ = server.Close()
	}()
	err = server.server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return nil
	}
	return err
}

func (server *httpAgentServer) Close() error {
	if server == nil {
		return nil
	}
	server.closeMu.Lock()
	defer server.closeMu.Unlock()
	if server.closed {
		return nil
	}
	server.closed = true
	if server.server != nil {
		return server.server.Close()
	}
	return nil
}
