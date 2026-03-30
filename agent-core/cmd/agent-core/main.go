package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/lifei6671/devbridge-loop/agent-core/runtime/agent/app"
)

const (
	envAgentID                  = "DEV_AGENT_CFG_AGENT_ID"
	envBridgeAddr               = "DEV_AGENT_CFG_BRIDGE_ADDR"
	envBridgeTransport          = "DEV_AGENT_CFG_BRIDGE_TRANSPORT"
	envBridgeTLSEnabled         = "DEV_AGENT_CFG_BRIDGE_TLS_ENABLED"
	envBridgeTLSRootCAFile      = "DEV_AGENT_CFG_BRIDGE_TLS_ROOT_CA_FILE"
	envBridgeTLSServerName      = "DEV_AGENT_CFG_BRIDGE_TLS_SERVER_NAME"
	envBridgeAuthMethod         = "DEV_AGENT_CFG_BRIDGE_AUTH_METHOD"
	envBridgeAuthToken          = "DEV_AGENT_CFG_BRIDGE_AUTH_TOKEN"
	envBridgeClientCapVersion   = "DEV_AGENT_CFG_BRIDGE_CLIENT_CAP_VERSION"
	envTunnelPoolMinIdle        = "DEV_AGENT_CFG_TUNNEL_POOL_MIN_IDLE"
	envTunnelPoolMaxIdle        = "DEV_AGENT_CFG_TUNNEL_POOL_MAX_IDLE"
	envTunnelPoolMaxInflight    = "DEV_AGENT_CFG_TUNNEL_POOL_MAX_INFLIGHT"
	envTunnelPoolTTLMS          = "DEV_AGENT_CFG_TUNNEL_POOL_TTL_MS"
	envTunnelPoolOpenRate       = "DEV_AGENT_CFG_TUNNEL_POOL_OPEN_RATE"
	envTunnelPoolOpenBurst      = "DEV_AGENT_CFG_TUNNEL_POOL_OPEN_BURST"
	envTunnelPoolReconcileGapMS = "DEV_AGENT_CFG_TUNNEL_POOL_RECONCILE_GAP_MS"
)

// main 负责启动 agent-runtime，并处理系统退出信号。
func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	resolvedConfig, bootstrapOptions, runOptions, err := loadRuntimeConfigFromArgs(os.Args[1:])
	if err != nil {
		log.Fatalf("load runtime config failed: %v", err)
	}

	// 通过 BootstrapWithOptions 应用 tunnel pool 覆盖，确保配置真实进入 runtime。
	runtime, err := app.BootstrapWithOptions(ctx, resolvedConfig, bootstrapOptions)
	if err != nil {
		log.Fatalf("agent bootstrap failed: %v", err)
	}

	if err := runtime.Run(ctx, runOptions); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("agent runtime stopped: %v", err)
	}
}

func loadRuntimeConfigFromArgs(args []string) (app.Config, app.BootstrapOptions, app.RunOptions, error) {
	flagSet := flag.NewFlagSet("agent-core", flag.ContinueOnError)
	flagSet.SetOutput(os.Stderr)
	configFilePathFlag := flagSet.String("config", "", "Agent YAML 配置文件路径")
	tauriFlag := flagSet.Bool("tauri", false, "显式启用 Tauri / LocalRPC 启动模式")
	webFlag := flagSet.Bool("web", false, "显式启用 Web 管理面启动模式")
	if err := flagSet.Parse(args); err != nil {
		return app.Config{}, app.BootstrapOptions{}, app.RunOptions{}, err
	}
	resolvedConfig, bootstrapOptions, err := loadRuntimeConfig(strings.TrimSpace(*configFilePathFlag))
	if err != nil {
		return app.Config{}, app.BootstrapOptions{}, app.RunOptions{}, err
	}
	runOptions, err := resolveRunOptions(resolvedConfig, *tauriFlag, *webFlag)
	if err != nil {
		return app.Config{}, app.BootstrapOptions{}, app.RunOptions{}, err
	}
	return resolvedConfig, bootstrapOptions, runOptions, nil
}

func loadRuntimeConfig(configFilePath string) (app.Config, app.BootstrapOptions, error) {
	resolvedConfig, err := app.LoadRuntimeConfig(strings.TrimSpace(configFilePath))
	if err != nil {
		return app.Config{}, app.BootstrapOptions{}, err
	}
	return resolvedConfig, app.BootstrapOptions{}, nil
}

func resolveRunOptions(config app.Config, tauriEnabled bool, webEnabled bool) (app.RunOptions, error) {
	runOptions := app.RunOptions{
		EnableLocalRPC: tauriEnabled,
		EnableWeb:      false,
	}
	switch {
	case webEnabled:
		runOptions.EnableWeb = true
	case tauriEnabled:
		runOptions.EnableWeb = false
	default:
		runOptions.EnableWeb = config.UI.Web.Enabled
	}
	if err := runOptions.Validate(config); err != nil {
		return app.RunOptions{}, err
	}
	return runOptions, nil
}

// loadRuntimeConfigFromEnv 从环境变量加载 agent-core 真实配置并做启动前校验。
func loadRuntimeConfigFromEnv(defaultConfig app.Config) (app.Config, app.BootstrapOptions, error) {
	resolvedConfig, err := app.ApplyRuntimeConfigEnvOverrides(defaultConfig)
	if err != nil {
		return app.Config{}, app.BootstrapOptions{}, err
	}
	return resolvedConfig, app.BootstrapOptions{}, nil
}
