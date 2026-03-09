package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/lifei6671/devbridge-loop/examples/internal/agentclient"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	grpc_health_v1 "google.golang.org/grpc/health/grpc_health_v1"
)

const (
	serviceName          = "inventory-service"
	grpcHealthService    = "inventory.v1.InventoryService"
	defaultHTTPAddr      = "127.0.0.1:18103"
	defaultGRPCAddr      = "127.0.0.1:19103"
	defaultAgentAddr     = "127.0.0.1:39090"
	defaultEnvName       = "base"
	defaultTTLSeconds    = 30
	defaultHeartbeatSec  = 10
	defaultAgentTimeoutM = 3000
)

type config struct {
	HTTPAddr          string
	GRPCAddr          string
	AgentAddr         string
	EnvName           string
	InstanceID        string
	AgentEnabled      bool
	TTLSeconds        int
	HeartbeatInterval time.Duration
	AgentTimeout      time.Duration
}

func main() {
	cfg := loadConfig()
	logger := log.New(os.Stdout, "[inventory-service] ", log.LstdFlags|log.Lmicroseconds)
	client := agentclient.New(cfg.AgentAddr, cfg.AgentTimeout)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	httpServer := newHTTPServer(cfg, logger)
	grpcListener, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		logger.Fatalf("监听 gRPC 地址失败: %v", err)
	}

	grpcHealth := health.NewServer()
	grpcHealth.SetServingStatus(grpcHealthService, grpc_health_v1.HealthCheckResponse_SERVING)
	grpcServer := grpc.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcServer, grpcHealth)

	if cfg.AgentEnabled {
		if err := registerToAgent(ctx, client, cfg); err != nil {
			logger.Printf("向 agent 注册失败（服务继续运行）: %v", err)
		} else {
			logger.Printf("已注册到 agent: instanceId=%s env=%s", cfg.InstanceID, cfg.EnvName)
		}
		go startHeartbeatLoop(ctx, client, cfg, logger)
	}

	serverErrCh := make(chan error, 2)
	go func() {
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrCh <- fmt.Errorf("http server error: %w", err)
		}
	}()
	go func() {
		if err := grpcServer.Serve(grpcListener); err != nil {
			serverErrCh <- fmt.Errorf("grpc server error: %w", err)
		}
	}()

	logger.Printf("服务已启动: http=%s grpc=%s env=%s agentEnabled=%v", cfg.HTTPAddr, cfg.GRPCAddr, cfg.EnvName, cfg.AgentEnabled)

	var runErr error
	select {
	case err := <-serverErrCh:
		runErr = err
		logger.Printf("服务异常退出: %v", err)
		stop()
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if cfg.AgentEnabled {
		// 退出时主动注销，避免 demo 频繁重启时残留脏注册。
		if err := unregisterFromAgent(shutdownCtx, client, cfg); err != nil {
			logger.Printf("注销 agent 失败: %v", err)
		}
	}

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Printf("关闭 HTTP 服务失败: %v", err)
	}
	gracefulStopGRPC(grpcServer, logger)

	if runErr != nil {
		os.Exit(1)
	}
}

func newHTTPServer(cfg config, _ *log.Logger) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"status":      "ok",
			"serviceName": serviceName,
			"env":         cfg.EnvName,
			"instanceId":  cfg.InstanceID,
		})
	})
	mux.HandleFunc("GET /api/v1/inventory/ping", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"serviceName": serviceName,
			"message":     "inventory ready",
			"env":         cfg.EnvName,
			"instanceId":  cfg.InstanceID,
			"time":        time.Now().UTC().Format(time.RFC3339),
			"query":       r.URL.RawQuery,
		})
	})

	return &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
}

func registerToAgent(ctx context.Context, client *agentclient.Client, cfg config) error {
	httpHost, httpPort, err := splitAddr(cfg.HTTPAddr)
	if err != nil {
		return err
	}
	grpcHost, grpcPort, err := splitAddr(cfg.GRPCAddr)
	if err != nil {
		return err
	}

	return client.Register(ctx, agentclient.Registration{
		ServiceName: serviceName,
		Env:         cfg.EnvName,
		InstanceID:  cfg.InstanceID,
		TTLSeconds:  cfg.TTLSeconds,
		HTTPHost:    httpHost,
		HTTPPort:    httpPort,
		GRPCHost:    grpcHost,
		GRPCPort:    grpcPort,
	})
}

func unregisterFromAgent(ctx context.Context, client *agentclient.Client, cfg config) error {
	if strings.TrimSpace(cfg.InstanceID) == "" {
		return nil
	}
	return client.Unregister(ctx, cfg.InstanceID)
}

func startHeartbeatLoop(ctx context.Context, client *agentclient.Client, cfg config, logger *log.Logger) {
	ticker := time.NewTicker(cfg.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			hbCtx, cancel := context.WithTimeout(ctx, cfg.AgentTimeout)
			err := client.Heartbeat(hbCtx, cfg.InstanceID)
			cancel()
			if err != nil {
				logger.Printf("发送心跳失败: %v", err)
			}
		}
	}
}

func gracefulStopGRPC(server *grpc.Server, logger *log.Logger) {
	done := make(chan struct{})
	go func() {
		server.GracefulStop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		logger.Printf("gRPC 优雅关闭超时，执行强制停止")
		server.Stop()
	}
}

func writeJSON(w http.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(payload)
}

func splitAddr(address string) (host string, port int, err error) {
	h, p, err := net.SplitHostPort(strings.TrimSpace(address))
	if err != nil {
		return "", 0, fmt.Errorf("解析地址失败: %w", err)
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(p))
	if err != nil {
		return "", 0, fmt.Errorf("解析端口失败: %w", err)
	}
	if strings.TrimSpace(h) == "" {
		h = "127.0.0.1"
	}
	return h, parsed, nil
}

func loadConfig() config {
	envName := getenv("DEMO_ENV_NAME", defaultEnvName)
	instanceID := strings.TrimSpace(os.Getenv("DEMO_INSTANCE_ID"))
	if instanceID == "" {
		instanceID = fmt.Sprintf("%s-%d", serviceName, time.Now().UnixNano())
	}

	return config{
		HTTPAddr:          getenv("INVENTORY_HTTP_ADDR", defaultHTTPAddr),
		GRPCAddr:          getenv("INVENTORY_GRPC_ADDR", defaultGRPCAddr),
		AgentAddr:         getenv("DEMO_AGENT_ADDR", defaultAgentAddr),
		EnvName:           envName,
		InstanceID:        instanceID,
		AgentEnabled:      getenvBool("DEMO_AGENT_ENABLED", true),
		TTLSeconds:        getenvInt("DEMO_TTL_SECONDS", defaultTTLSeconds),
		HeartbeatInterval: time.Duration(getenvInt("DEMO_HEARTBEAT_INTERVAL_SEC", defaultHeartbeatSec)) * time.Second,
		AgentTimeout:      time.Duration(getenvInt("DEMO_AGENT_TIMEOUT_MS", defaultAgentTimeoutM)) * time.Millisecond,
	}
}

func getenv(key string, fallback string) string {
	if value, ok := os.LookupEnv(key); ok && strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func getenvInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func getenvBool(key string, fallback bool) bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if value == "" {
		return fallback
	}
	switch value {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}
