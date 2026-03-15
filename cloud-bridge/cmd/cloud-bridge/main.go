package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/app"
)

const defaultRuntimeConfigFileName = "bridge.yaml"

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	cfg, err := loadRuntimeConfigFromFlags()
	if err != nil {
		log.Fatalf("load bridge config failed: %v", err)
	}
	runtime, err := app.Bootstrap(ctx, cfg)
	if err != nil {
		log.Fatalf("bridge bootstrap failed: %v", err)
	}

	if err := runtime.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("bridge runtime stopped: %v", err)
	}
}

// loadRuntimeConfigFromFlags 解析命令行参数，并按需从 YAML 文件加载配置。
// 规则：
// 1) 显式传入 -config 时，按该路径加载并作为后续后台持久化目标；
// 2) 未传 -config 时，先使用默认配置，再尝试自动加载 ./bridge.yaml；
// 3) 若 ./bridge.yaml 不存在，保留默认配置并把该路径作为后台保存目标。
func loadRuntimeConfigFromFlags() (app.Config, error) {
	configFilePathFlag := flag.String("config", "", "Bridge YAML 配置文件路径")
	flag.Parse()

	normalizedConfigFilePath := strings.TrimSpace(*configFilePathFlag)
	if normalizedConfigFilePath != "" {
		return app.LoadConfigFromYAMLFile(normalizedConfigFilePath)
	}
	defaultConfigFilePath, err := filepath.Abs(defaultRuntimeConfigFileName)
	if err != nil {
		return app.Config{}, err
	}
	if _, statErr := os.Stat(defaultConfigFilePath); statErr == nil {
		return app.LoadConfigFromYAMLFile(defaultConfigFilePath)
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return app.Config{}, statErr
	}
	defaultConfig := app.DefaultConfig()
	defaultConfig.RuntimeConfigFilePath = defaultConfigFilePath
	return defaultConfig, nil
}
