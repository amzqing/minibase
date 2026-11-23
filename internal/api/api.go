// Package api 把 HTTP 请求翻译成部署器/存储层调用。
// 第一版入口只有 HTTP API；CLI 是它的客户端。
package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"minibase/internal/auth"
	"minibase/internal/config"
	"minibase/internal/crypto"
	"minibase/internal/database"
	"minibase/internal/deploy"
	"minibase/internal/logger"
	"minibase/internal/proxy"
	"minibase/internal/storage"
)

// Server 组装全部路由。
type Server struct {
	store    *database.Store
	deployer *deploy.Deployer
	proxy    *proxy.Proxy
	token    string
	cfg      *config.Config
	cipher   *crypto.Cipher
	storage  storage.Store
	logger   *logger.Logger
	mux      *http.ServeMux
}

// New 创建 HTTP 服务。
func New(store *database.Store, deployer *deploy.Deployer, token string, cfg *config.Config) *Server {
	cipher, _ := crypto.New(cfg.MasterKey)
	s := &Server{
		store: store, deployer: deployer, proxy: proxy.New(store), token: token, cfg: cfg,
		cipher: cipher, storage: storage.NewLocalStore(cfg, store), logger: logger.New(),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", deploy.HealthHandler)
	mux.HandleFunc("GET /status", s.handleStatusPage)
	mux.Handle("/api/", auth.Middleware(token)(http.HandlerFunc(s.handleAPI)))
	mux.Handle("/files/", http.HandlerFunc(s.handleFileAccess)) // 免 Token，浏览器访问
	mux.Handle("/apps/", s.proxy)
	mux.Handle("/", http.NotFoundHandler())
	s.mux = mux
	return s
}

// Handler 返回可交给 http.Server 的处理器。
func (s *Server) Handler() http.Handler { return s.mux }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/deploy":
		s.handleDeploy(w, r)
	case r.Method == http.MethodGet && len(r.URL.Path) > len("/api/status/") && r.URL.Path[:len("/api/status/")] == "/api/status/":
		name := r.URL.Path[len("/api/status/"):]
		s.handleStatus(w, r, name)
	case r.URL.Path == "/api/apps-list":
		s.handleAppsList(w, r)
	case r.URL.Path == "/api/logs/control":
		s.handleLogsControl(w, r)
	case r.URL.Path == "/api/logs/audit":
		s.handleAudit(w, r)
	case r.Method == http.MethodGet && len(r.URL.Path) > len("/api/logs/") && r.URL.Path[:len("/api/logs/")] == "/api/logs/":
		name := r.URL.Path[len("/api/logs/"):]
		s.handleLogs(w, r, name)
	case len(r.URL.Path) >= len("/api/data/") && r.URL.Path[:len("/api/data/")] == "/api/data/":
		s.handleDataAPI(w, r)
	case len(r.URL.Path) >= len("/api/storage/") && r.URL.Path[:len("/api/storage/")] == "/api/storage/":
		s.handleStorage(w, r)
	case len(r.URL.Path) >= len("/api/apps/") && r.URL.Path[:len("/api/apps/")] == "/api/apps/":
		rest := r.URL.Path[len("/api/apps/"):]
		if len(rest) > 0 && strings.HasSuffix(rest, "/rollback") && r.Method == http.MethodPost {
			s.handleRollback(w, r)
		} else if len(rest) > 0 && strings.HasSuffix(rest, "/status") && r.Method == http.MethodGet {
			s.handleAppStatus(w, r)
		} else if len(rest) > 0 && strings.HasSuffix(rest, "/secrets") && r.Method == http.MethodPost {
			s.handleSecrets(w, r)
		} else if len(rest) > 0 && strings.HasSuffix(rest, "/env") {
			s.handleEnv(w, r)
		} else if len(rest) > 0 && strings.HasSuffix(rest, "/applog") {
			s.handleAppLog(w, r)
		} else if r.Method == http.MethodDelete && strings.Count(rest, "/") == 1 {
			s.handleDown(w, r)
		} else {
			http.NotFound(w, r)
		}
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleDeploy(w http.ResponseWriter, r *http.Request) {
	// 限制请求体大小，避免大包打爆内存。
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxUploadMB*1024*1024)
	if err := r.ParseMultipartForm(s.cfg.MultipartMemoryMB * 1024 * 1024); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("解析上传表单失败: %w", err))
		return
	}
	f, header, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("缺少 zip 文件字段 file: %w", err))
		return
	}
	defer f.Close()

	tmp, err := os.CreateTemp("", "minibase-upload-*.zip")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := io.Copy(tmp, f); err != nil {
		tmp.Close()
		writeErr(w, http.StatusInternalServerError, fmt.Errorf("保存上传文件失败: %w", err))
		return
	}
	tmp.Close()

	mode := r.FormValue("mode")
	if mode == "" {
		mode = "b"
	}
	res, err := s.deployer.Deploy(r.Context(), deploy.Request{
		ZipPath:   tmpPath,
		AppName:   r.FormValue("app"),
		ProjectID: r.FormValue("project"),
		Mode:      mode,
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	out := map[string]any{
		"project_id": res.ProjectID,
		"app_name":   res.AppName,
		"root_path":  res.RootPath,
		"version":    res.Version,
		"port":       res.Port,
		"status":     res.Status,
		"access_url": fmt.Sprintf("http://%s%s", r.Host, res.RootPath),
		"notice":     "第一版不备份，数据自己负责",
		"file":       header.Filename,
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request, name string) {
	app, err := s.store.FindAppByName(name)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if app == nil {
		writeErr(w, http.StatusNotFound, fmt.Errorf("应用 %q 不存在", name))
		return
	}
	root := app.RootPath
	writeJSON(w, http.StatusOK, map[string]any{
		"app_name": app.Name, "project_id": app.ProjectID, "root_path": root,
		"current_version": app.CurrentVersion, "previous_version": app.PreviousVersion,
		"port": app.Port, "status": app.Status,
		"access_url": fmt.Sprintf("http://%s%s", r.Host, root),
		"notice":     "第一版不备份，数据自己负责",
	})
}

func (s *Server) handleLogs(w http.ResponseWriter, _ *http.Request, name string) {
	app, err := s.store.FindAppByName(name)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if app == nil {
		writeErr(w, http.StatusNotFound, fmt.Errorf("应用 %q 不存在", name))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"app_name": name,
		"logs":     "",
		"notice":   "日志未开启（版本一不采集应用日志）",
	})
}
