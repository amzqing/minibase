// Package proxy 把外部请求按应用根路径转发到应用监听的端口。
// 只动路径：剥掉 /apps/<项目ID>/<应用名> 前缀，别的都不碰。
package proxy

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"strings"

	"minibase/internal/database"
)

// Proxy 是按根路径分发的反向代理。
type Proxy struct {
	store *database.Store
	rp    *httputil.ReverseProxy
}

// New 创建代理。
func New(store *database.Store) *Proxy {
	p := &Proxy{store: store}
	p.rp = &httputil.ReverseProxy{
		Director: func(*http.Request) {}, // URL 已在 ServeHTTP 里设好
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			http.Error(w, fmt.Sprintf("转发到应用失败: %v", err), http.StatusBadGateway)
		},
	}
	return p
}

// MatchRoot 从路径里拆出应用根路径和剥前缀后的干净路径。
// 匹配不上返回空根路径。
func MatchRoot(path string) (root, rest string) {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) < 3 || parts[0] != "apps" || parts[1] == "" || parts[2] == "" {
		return "", ""
	}
	root = "/apps/" + parts[1] + "/" + parts[2]
	rest = strings.TrimPrefix(path, root)
	if rest == "" {
		rest = "/"
	}
	if !strings.HasPrefix(rest, "/") {
		return "", ""
	}
	return root, rest
}

// ServeHTTP 实现转发。
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	root, rest := MatchRoot(r.URL.Path)
	if root == "" {
		http.NotFound(w, r)
		return
	}
	app, err := p.store.GetAppByRootPath(root)
	if err != nil {
		http.Error(w, "查询应用失败", http.StatusInternalServerError)
		return
	}
	if app == nil || app.CurrentVersion == "" || app.Port == 0 {
		http.Error(w, "应用不存在或尚未就绪", http.StatusServiceUnavailable)
		return
	}

	// 跨项目隔离：带 X-MiniBase-From 头时，只允许同项目应用互相调用。
	// 无头视为外部浏览器访问，保持原有行为不变。
	if !AllowedBySource(r.Header.Get("X-MiniBase-From"), app.ProjectID) {
		http.Error(w, "跨项目调用被拒绝", http.StatusForbidden)
		return
	}

	out := r.Clone(r.Context())
	u := *r.URL
	u.Scheme = "http"
	u.Host = fmt.Sprintf("127.0.0.1:%d", app.Port)
	u.Path = rest
	out.URL = &u
	out.Host = r.Host
	p.rp.ServeHTTP(w, out)
}

// AllowedBySource 检查来源头：无头放行（外部访问），有头时只允许同项目。
func AllowedBySource(from, targetProject string) bool {
	return from == "" || from == targetProject
}
