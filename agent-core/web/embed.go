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
	// HeaderUIVersion 标记当前响应使用的内嵌 UI 指纹，便于排障确认前后端是否一致。
	HeaderUIVersion = "X-Agent-UI-Version"
	// cacheControlHTML 确保入口 HTML 每次都与服务端重新协商，避免陈旧入口页。
	cacheControlHTML = "no-cache"
	// cacheControlImmutable 用于 hash 产物，允许长期缓存。
	cacheControlImmutable = "public, max-age=31536000, immutable"
	// cacheControlDefault 为非 hash 资源提供短时缓存兜底。
	cacheControlDefault = "public, max-age=300"
)

var (
	hashedAssetNamePattern = regexp.MustCompile(`-[A-Za-z0-9_-]{8,}\.`)
	embeddedBundleOnce     sync.Once
	embeddedBundle         uiBundle
)

type uiBundle struct {
	staticFS fs.FS
	version  string
	ready    bool
}

//go:embed dist/*
var distFS embed.FS

// Handler 返回带 SPA fallback、版本标识与缓存策略的内嵌 Agent UI 处理器。
func Handler() http.Handler {
	bundle := loadBundle()
	if !bundle.ready {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			http.Error(writer, "agent ui not built", http.StatusServiceUnavailable)
		})
	}

	fileServer := http.FileServer(http.FS(bundle.staticFS))

	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		normalizedPath := normalizeRequestPath(request.URL.Path)
		if !fileExists(bundle.staticFS, normalizedPath) {
			normalizedPath = "index.html"
		}
		writer.Header().Set(HeaderUIVersion, bundle.version)
		writer.Header().Set("Cache-Control", cacheControlForPath(normalizedPath))
		clonedRequest := request.Clone(request.Context())
		clonedRequest.URL.Path = fileServerPathFor(normalizedPath)
		fileServer.ServeHTTP(writer, clonedRequest)
	})
}

// EmbeddedVersion 返回当前内嵌 UI 的短指纹。
func EmbeddedVersion() string {
	return loadBundle().version
}

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

func computeBundleVersion(staticFS fs.FS) string {
	if staticFS == nil {
		return "missing"
	}
	filePaths := make([]string, 0, 16)
	_ = fs.WalkDir(staticFS, ".", func(currentPath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
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
	return fullHash[:12]
}

func normalizeRequestPath(rawPath string) string {
	cleanedPath := path.Clean("/" + strings.TrimSpace(rawPath))
	trimmedPath := strings.TrimPrefix(cleanedPath, "/")
	if trimmedPath == "" || trimmedPath == "." {
		return "index.html"
	}
	return trimmedPath
}

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

func fileServerPathFor(filePath string) string {
	if strings.EqualFold(strings.TrimSpace(filePath), "index.html") {
		return "/"
	}
	return "/" + filePath
}
