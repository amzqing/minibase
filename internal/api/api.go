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
	"minibase/web"
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

func New(store *database.Store, deployer *deploy.Deployer, token string, cfg *config.Config) *Server {
	cipher, _ := crypto.New(cfg.MasterKey)
	s := &Server{
		store: store, deployer: deployer, proxy: proxy.New(store), token: token, cfg: cfg,
		cipher: cipher, storage: storage.NewLocalStore(cfg, store), logger: logger.New(),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", deploy.HealthHandler)
	mux.HandleFunc("GET /api/system/health", deploy.HealthHandler)
	mux.HandleFunc("GET /status", s.handleStatusPage)
	mux.Handle("/ui/", http.StripPrefix("/ui/", http.FileServer(http.FS(web.FS()))))
	mux.Handle("/api/", auth.Middleware(cfg)(http.HandlerFunc(s.handleAPI)))
	mux.Handle("/files/", http.HandlerFunc(s.handleFileAccess))
	mux.Handle("/apps/", s.proxy)
	mux.Handle("/", http.NotFoundHandler())
	s.mux = mux
	return s
}

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
	path := r.URL.Path

	switch path {
	case "/api/usage", "/api/system/usage":
		if r.Method == http.MethodGet {
			s.handleUsage(w, r)
			return
		}
	case "/api/logs/audit", "/api/system/audit":
		s.handleAudit(w, r)
		return
	case "/api/logs/control", "/api/system/log-control":
		s.handleLogsControl(w, r)
		return
	case "/api/auth/token/rotate", "/api/system/token/rotate":
		s.handleTokenRotate(w, r)
		return
	case "/api/apps", "/api/deploy":
		if r.Method == http.MethodPost {
			s.handleDeploy(w, r)
			return
		}
		if path == "/api/apps" && r.Method == http.MethodGet {
			s.handleAppsList(w, r)
			return
		}
	case "/api/apps-list":
		s.handleAppsList(w, r)
		return
	}

	if strings.HasPrefix(path, "/api/status/") {
		s.handleStatus(w, r, strings.TrimPrefix(path, "/api/status/"))
		return
	}
	if strings.HasPrefix(path, "/api/logs/") {
		s.handleLogs(w, r, strings.TrimPrefix(path, "/api/logs/"))
		return
	}
	if strings.HasPrefix(path, "/api/storage/") {
		action, app, fileID, ok := storageRoute(path)
		if !ok {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("路径格式错误"))
			return
		}
		// 兼容路径：ID 能解析就归一成应用名；否则保留原标识（存储层按名字隔离）。
		if resolved, err := s.resolveApp(app); err == nil && resolved != nil {
			app = resolved.Name
		}
		s.handleStorage(w, r, action, app, fileID)
		return
	}

	identifier, rest, ok := appRoute(path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	resolved, err := s.resolveApp(identifier)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if resolved == nil {
		writeErr(w, http.StatusNotFound, fmt.Errorf("应用不存在"))
		return
	}
	app := resolved.Name
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodGet:
			s.handleAppStatus(w, r, app)
		case http.MethodPut:
			s.handleAppEdit(w, r, app)
		case http.MethodDelete:
			s.handleDestroy(w, r, app)
		default:
			http.NotFound(w, r)
		}
		return
	}

	if rest[0] == "databases" {
		s.handleDatabase(w, r, app, rest[1:])
		return
	}
	if rest[0] == "cors" {
		if r.Method == http.MethodDelete {
			s.handleCorsDelete(w, r, app, rest[1:])
		} else {
			s.handleCors(w, r, app, rest[1:])
		}
		return
	}
	if rest[0] == "files" {
		fileID := ""
		if len(rest) > 1 {
			fileID = rest[1]
		}
		switch r.Method {
		case http.MethodPost:
			s.handleStorage(w, r, "upload", app, fileID)
		case http.MethodGet:
			if fileID == "" {
				s.handleStorage(w, r, "list", app, fileID)
			} else {
				s.handleStorage(w, r, "download", app, fileID)
			}
		case http.MethodDelete:
			s.handleStorage(w, r, "delete", app, fileID)
		default:
			http.NotFound(w, r)
		}
		return
	}

	action := rest[0]
	if action == "actions" && len(rest) > 1 {
		action = rest[1]
	}
	switch action {
	case "status":
		s.handleAppStatus(w, r, app)
	case "logs", "applog":
		s.handleAppLog(w, r, app)
	case "usage":
		s.handleAppUsage(w, r, app)
	case "env":
		s.handleEnv(w, r, app)
	case "secrets":
		s.handleSecrets(w, r, app)
	case "versions":
		s.handleVersions(w, r, app)
	case "deployments":
		s.handleDeployments(w, r, app)
	case "resume":
		s.handleResume(w, r, app)
	case "pause":
		s.handlePause(w, r, app)
	case "restart":
		s.handleRestart(w, r, app)
	case "rollback":
		s.handleRollback(w, r, app)
	default:
		http.NotFound(w, r)
	}
}

// resolveApp 把路径中的应用标识解析成应用。优先按 ID，其次兼容旧的应用名。
// 内部各层继续用真实应用名作为存储和数据库键，避免数据迁移。
func (s *Server) resolveApp(identifier string) (*database.App, error) {
	if identifier == "" {
		return nil, nil
	}
	app, err := s.store.GetAppByID(identifier)
	if err != nil {
		return nil, err
	}
	if app != nil {
		return app, nil
	}
	return s.store.GetAppByName(identifier)
}

func (s *Server) handleDeploy(w http.ResponseWriter, r *http.Request) {
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
		ZipPath: tmpPath, AppName: r.FormValue("app"), Mode: mode,
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"app_name": res.AppName, "root_path": res.RootPath, "version": res.Version,
		"port": res.Port, "status": res.Status,
		"access_url": fmt.Sprintf("http://%s%s", r.Host, res.RootPath),
		"notice":     "第一版不备份，数据自己负责", "file": header.Filename,
	})
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
	writeJSON(w, http.StatusOK, map[string]any{
		"app_name": app.Name, "root_path": app.RootPath,
		"current_version": app.CurrentVersion, "previous_version": app.PreviousVersion,
		"port": app.Port, "status": app.Status,
		"access_url": fmt.Sprintf("http://%s%s", r.Host, app.RootPath),
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
	writeJSON(w, http.StatusOK, map[string]any{"app_name": name, "logs": "", "notice": "日志未开启（版本一不采集应用日志）"})
}
