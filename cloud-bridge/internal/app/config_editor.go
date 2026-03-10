package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/config"
)

// BridgeConfigEditor 提供 bridge 配置文件读写与热重启信号触发能力。
type BridgeConfigEditor struct {
	mu            sync.Mutex
	configPath    string
	runtimeConfig config.Config
	restartSignal chan<- struct{}
}

// NewBridgeConfigEditor 创建配置编辑器。
func NewBridgeConfigEditor(configPath string, runtimeConfig config.Config, restartSignal chan<- struct{}) *BridgeConfigEditor {
	resolvedPath := strings.TrimSpace(configPath)
	if resolvedPath == "" {
		resolvedPath = config.ResolveConfigFilePath()
	}
	return &BridgeConfigEditor{
		configPath:    resolvedPath,
		runtimeConfig: runtimeConfig,
		restartSignal: restartSignal,
	}
}

// LoadConfigFile 返回配置文件路径及内容；若文件不存在则回退到运行时配置模板。
func (e *BridgeConfigEditor) LoadConfigFile() (string, string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	content, err := os.ReadFile(e.configPath)
	if err == nil {
		return e.configPath, string(content), nil
	}
	if !os.IsNotExist(err) {
		return e.configPath, "", fmt.Errorf("read config file failed: %w", err)
	}

	// 文件不存在时返回可编辑模板，避免管理页首次打开无内容可改。
	template, marshalErr := config.MarshalConfigYAML(e.runtimeConfig)
	if marshalErr != nil {
		return e.configPath, "", fmt.Errorf("marshal default config yaml failed: %w", marshalErr)
	}
	return e.configPath, string(template), nil
}

// SaveConfigFile 保存配置文件并做 YAML 有效性校验。
func (e *BridgeConfigEditor) SaveConfigFile(content string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	trimmedContent := strings.TrimSpace(content)
	if trimmedContent == "" {
		return fmt.Errorf("config content is empty")
	}
	payload := []byte(trimmedContent + "\n")
	if err := config.ValidateConfigYAML(e.configPath, payload); err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(e.configPath), 0o755); err != nil && filepath.Dir(e.configPath) != "." {
		return fmt.Errorf("create config directory failed: %w", err)
	}
	if err := os.WriteFile(e.configPath, payload, 0o600); err != nil {
		return fmt.Errorf("write config file failed: %w", err)
	}
	return nil
}

// TriggerHotRestart 异步触发 bridge 热重启，使用非阻塞写入避免重复排队。
func (e *BridgeConfigEditor) TriggerHotRestart() {
	if e == nil || e.restartSignal == nil {
		return
	}
	go func() {
		// 让保存接口先回包，再触发重启，避免浏览器端误判保存失败。
		time.Sleep(120 * time.Millisecond)
		select {
		case e.restartSignal <- struct{}{}:
		default:
		}
	}()
}
