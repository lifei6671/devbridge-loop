package web

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io"
	"io/fs"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
)

const (
	// HeaderUIVersion 用于标记当前响应对应的内嵌 UI 版本。
	HeaderUIVersion = "X-Bridge-UI-Version"
	// cacheControlHTML 用于入口 HTML，要求每次协商缓存避免陈旧页面。
	cacheControlHTML = "no-cache"
	// cacheControlImmutable 用于带 hash 的静态资源，允许长期强缓存。
	cacheControlImmutable = "public, max-age=31536000, immutable"
	// cacheControlDefault 用于未命中 hash 规则的静态资源，给短缓存兜底。
	cacheControlDefault = "public, max-age=300"
)

var (
	// hashedAssetNamePattern 匹配常见的构建产物 hash 文件名（例如 main-ABC12345.js）。
	hashedAssetNamePattern = regexp.MustCompile(`-[A-Za-z0-9_-]{8,}\.`)
	embeddedBundleOnce     sync.Once
	embeddedBundle         uiBundle
)

// uiBundle 缓存内嵌静态资源句柄与版本信息，避免每次请求重复计算。
type uiBundle struct {
	staticFS fs.FS
	version  string
	ready    bool
}

//go:embed dist/*
var distFS embed.FS

// Handler 返回带 SPA fallback、版本标识与缓存策略的内嵌静态资源处理器。
func Handler() http.Handler {
	bundle := loadBundle()
	if !bundle.ready {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// 构建产物缺失时显式返回 503，避免误判为业务路由 404。
			http.Error(w, "bridge ui not built", http.StatusServiceUnavailable)
		})
	}

	fileServer := http.FileServer(http.FS(bundle.staticFS))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		normalizedPath := normalizeRequestPath(r.URL.Path)
		if !fileExists(bundle.staticFS, normalizedPath) {
			// 未命中静态资源时回落到 index.html，支持 SPA 前端路由。
			normalizedPath = "index.html"
		}
		// 每个响应都附带内嵌版本标识，便于线上排障确认前后端版本一致性。
		w.Header().Set(HeaderUIVersion, bundle.version)
		// 基于文件路径应用缓存策略：HTML 禁缓存，hash 资源长期缓存。
		w.Header().Set("Cache-Control", cacheControlForPath(normalizedPath))
		clonedRequest := r.Clone(r.Context())
		// index.html 统一映射到根路径，避免 FileServer 的 index 重定向。
		clonedRequest.URL.Path = fileServerPathFor(normalizedPath)
		fileServer.ServeHTTP(w, clonedRequest)
	})
}

// EmbeddedVersion 返回内嵌 UI 的版本指纹，用于日志与诊断展示。
func EmbeddedVersion() string {
	return loadBundle().version
}

// loadBundle 初始化并缓存内嵌资源元信息。
func loadBundle() uiBundle {
	embeddedBundleOnce.Do(func() {
		subFS, err := fs.Sub(distFS, "dist")
		if err != nil {
			embeddedBundle = uiBundle{
				version: "missing",
				ready:   false,
			}
			return
		}
		embeddedBundle = uiBundle{
			staticFS: subFS,
			version:  computeBundleVersion(subFS),
			ready:    true,
		}
	})
	return embeddedBundle
}

// computeBundleVersion 基于文件路径与内容计算稳定版本指纹。
func computeBundleVersion(staticFS fs.FS) string {
	if staticFS == nil {
		return "missing"
	}
	filePaths := make([]string, 0, 32)
	_ = fs.WalkDir(staticFS, ".", func(currentPath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			// 版本计算失败不阻塞服务启动，跳过异常节点即可。
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		filePaths = append(filePaths, currentPath)
		return nil
	})
	if len(filePaths) == 0 {
		return "empty"
	}
	sort.Strings(filePaths)
	hasher := sha256.New()
	for _, filePath := range filePaths {
		// 先写入路径，保证同内容不同文件名也会得到不同版本。
		_, _ = io.WriteString(hasher, filePath)
		_, _ = hasher.Write([]byte{0})
		file, err := staticFS.Open(filePath)
		if err != nil {
			continue
		}
		_, _ = io.Copy(hasher, file)
		_ = file.Close()
		_, _ = hasher.Write([]byte{0})
	}
	fullHash := hex.EncodeToString(hasher.Sum(nil))
	if len(fullHash) < 12 {
		return fullHash
	}
	// 截断为短指纹，便于日志展示与 Header 传输。
	return fullHash[:12]
}

// normalizeRequestPath 归一化请求路径，避免目录穿越并统一入口页面映射。
func normalizeRequestPath(rawPath string) string {
	cleanedPath := path.Clean("/" + strings.TrimSpace(rawPath))
	trimmedPath := strings.TrimPrefix(cleanedPath, "/")
	if trimmedPath == "" || trimmedPath == "." {
		return "index.html"
	}
	return trimmedPath
}

// fileExists 判断指定路径是否是一个可读取文件。
func fileExists(staticFS fs.FS, filePath string) bool {
	if staticFS == nil {
		return false
	}
	file, err := staticFS.Open(filePath)
	if err != nil {
		return false
	}
	defer func() {
		_ = file.Close()
	}()
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// cacheControlForPath 按文件类型与命名规则返回缓存头策略。
func cacheControlForPath(filePath string) string {
	lowerPath := strings.ToLower(strings.TrimSpace(filePath))
	if strings.HasSuffix(lowerPath, ".html") {
		return cacheControlHTML
	}
	baseName := path.Base(filePath)
	if hashedAssetNamePattern.MatchString(baseName) {
		return cacheControlImmutable
	}
	return cacheControlDefault
}

// fileServerPathFor 把内部文件路径转换为 FileServer 期望的请求路径。
func fileServerPathFor(filePath string) string {
	if strings.EqualFold(strings.TrimSpace(filePath), "index.html") {
		return "/"
	}
	return "/" + filePath
}
