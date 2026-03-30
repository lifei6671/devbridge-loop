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

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/app"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	cfg, err := loadRuntimeConfigFromFlags()
	if err != nil {
		log.Fatalf("load bridge config failed: %v", err)
	}
	logRuntimeConfigPaths(cfg)
	runtime, err := app.Bootstrap(ctx, cfg)
	if err != nil {
		log.Fatalf("bridge bootstrap failed: %v", err)
	}

	if err := runtime.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("bridge runtime stopped: %v", err)
	}
}

// loadRuntimeConfigFromFlags 解析命令行参数，并委托 app 层按统一优先级加载运行配置。
func loadRuntimeConfigFromFlags() (app.Config, error) {
	configFilePathFlag := flag.String("config", "", "Bridge YAML 配置文件路径")
	flag.Parse()
	return app.LoadRuntimeConfig(strings.TrimSpace(*configFilePathFlag))
}

func logRuntimeConfigPaths(runtimeConfig app.Config) {
	baseConfigPath, userConfigPath := resolveLoadedRuntimeConfigPaths(runtimeConfig)
	log.Printf(
		"bridge config sources base_config_path=%s user_config_path=%s",
		formatLoadedRuntimeConfigPathForLog(baseConfigPath),
		formatLoadedRuntimeConfigPathForLog(userConfigPath),
	)
}

func resolveLoadedRuntimeConfigPaths(runtimeConfig app.Config) (string, string) {
	return existingConfigFilePath(runtimeConfig.RuntimeBaseConfigFilePath), existingConfigFilePath(runtimeConfig.RuntimeConfigFilePath)
}

func existingConfigFilePath(rawConfigFilePath string) string {
	normalizedConfigFilePath := strings.TrimSpace(rawConfigFilePath)
	if normalizedConfigFilePath == "" {
		return ""
	}
	fileInfo, err := os.Stat(normalizedConfigFilePath)
	if err != nil || fileInfo.IsDir() {
		return ""
	}
	return normalizedConfigFilePath
}

func formatLoadedRuntimeConfigPathForLog(configFilePath string) string {
	if strings.TrimSpace(configFilePath) == "" {
		return "<none>"
	}
	return configFilePath
}
