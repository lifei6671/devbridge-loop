package app

import "github.com/lifei6671/devbridge-loop/cloud-bridge/internal/httpapi"

// Option 定义 app 构造可选项。
type Option func(*appOptions)

type appOptions struct {
	hotRestartSignal <-chan struct{}
	configEditor     httpapi.ConfigEditor
}

func defaultAppOptions() appOptions {
	return appOptions{
		hotRestartSignal: nil,
		configEditor:     nil,
	}
}

// WithHotRestartSignal 注入热重启信号通道。
func WithHotRestartSignal(signal <-chan struct{}) Option {
	return func(options *appOptions) {
		options.hotRestartSignal = signal
	}
}

// WithConfigEditor 注入配置编辑器，用于管理页保存配置与触发重启。
func WithConfigEditor(editor httpapi.ConfigEditor) Option {
	return func(options *appOptions) {
		options.configEditor = editor
	}
}
