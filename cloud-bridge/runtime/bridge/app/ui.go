package app

import (
	"net/http"
	"strings"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/web"
)

const (
	// defaultAdminUIBasePath 定义管理页面默认挂载前缀。
	defaultAdminUIBasePath = "/admin"
)

// UIHandler 返回 Bridge 内嵌管理页面处理器。
func UIHandler() http.Handler {
	return web.Handler()
}

// AdminUIBasePath 返回默认管理页面路由前缀。
func AdminUIBasePath() string {
	return defaultAdminUIBasePath
}

// RegisterAdminUIRoutes 注册管理页面路由，包含 /admin 与 /admin/* 两种访问入口。
func RegisterAdminUIRoutes(mux *http.ServeMux, basePath string, handler http.Handler) {
	if mux == nil || handler == nil {
		return
	}
	normalizedBasePath := normalizeAdminUIBasePath(basePath)
	// /admin -> /admin/，避免前端相对路径在无尾斜杠时解析异常。
	mux.HandleFunc(normalizedBasePath, func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, normalizedBasePath+"/", http.StatusTemporaryRedirect)
	})
	// /admin/* 路径交给前端静态资源处理器（含 SPA fallback）。
	mux.Handle(normalizedBasePath+"/", http.StripPrefix(normalizedBasePath, handler))
	// 管理端口根路径默认跳转到 /admin/，降低使用门槛。
	mux.HandleFunc("/", func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/" {
			http.NotFound(writer, request)
			return
		}
		http.Redirect(writer, request, normalizedBasePath+"/", http.StatusTemporaryRedirect)
	})
}

// normalizeAdminUIBasePath 把输入前缀归一化到标准 /xxx 形式。
func normalizeAdminUIBasePath(basePath string) string {
	trimmedBasePath := strings.TrimSpace(basePath)
	if trimmedBasePath == "" {
		return defaultAdminUIBasePath
	}
	if !strings.HasPrefix(trimmedBasePath, "/") {
		trimmedBasePath = "/" + trimmedBasePath
	}
	trimmedBasePath = strings.TrimRight(trimmedBasePath, "/")
	if trimmedBasePath == "" || trimmedBasePath == "/" {
		return defaultAdminUIBasePath
	}
	return trimmedBasePath
}
