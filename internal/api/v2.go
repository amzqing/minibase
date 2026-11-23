package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"minibase/internal/database"
	"minibase/internal/health"
)

// ===== 版本二新增 API =====

// handleRollback POST /api/apps/<项目ID>/<应用名>/rollback
func (s *Server) handleRollback(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/apps/"), "/")
	if len(parts) < 3 || parts[2] != "rollback" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("路径格式错误"))
		return
	}
	res, err := s.deployer.Rollback(parts[0], parts[1])
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok", "version": res.Version, "port": res.Port,
		"access_url": fmt.Sprintf("http://%s%s", r.Host, res.RootPath),
	})
}

// handleAppStatus GET /api/apps/<项目ID>/<应用名>/status
func (s *Server) handleAppStatus(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/apps/"), "/")
	if len(parts) < 3 || parts[2] != "status" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("路径格式错误"))
		return
	}
	app, err := s.store.GetAppByName(parts[0], parts[1])
	if err != nil || app == nil {
		writeErr(w, http.StatusNotFound, fmt.Errorf("应用不存在"))
		return
	}
	logs, _ := s.store.ListCallLogs(app.ID, 20)
	writeJSON(w, http.StatusOK, map[string]any{
		"app_name": app.Name, "status": app.Status,
		"current_version": app.CurrentVersion, "previous_version": app.PreviousVersion,
		"restart_count": app.RestartCount,
		"history":       logs,
	})
}

// handleSecrets POST /api/apps/<项目ID>/<应用名>/secrets
func (s *Server) handleSecrets(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/apps/"), "/")
	if len(parts) < 3 || parts[2] != "secrets" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("路径格式错误"))
		return
	}
	app, err := s.store.GetAppByName(parts[0], parts[1])
	if err != nil || app == nil {
		writeErr(w, http.StatusNotFound, fmt.Errorf("应用不存在"))
		return
	}
	var body map[string]string
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	version := app.CurrentVersion
	if version == "" {
		version = "v1"
	}
	for k, v := range body {
		enc, err := s.cipher.Encrypt(v)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		sec := &database.Secret{AppID: app.ID, Key: k, EncryptedValue: enc, Version: version, CreatedAt: time.Now().Unix()}
		if err := s.store.SaveSecret(sec); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "count": len(body)})
}

// handleStorage 对象存储 API
func (s *Server) handleStorage(w http.ResponseWriter, r *http.Request) {
	// /api/storage/<动作>/<项目ID>/<应用名>[/<文件ID>]
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/storage/"), "/")
	if len(parts) < 3 {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("路径格式错误"))
		return
	}
	action, projectID, appName := parts[0], parts[1], parts[2]
	fileID := ""
	if len(parts) >= 4 {
		fileID = parts[3]
	}

	switch {
	case action == "upload" && r.Method == http.MethodPost:
		r.Body = http.MaxBytesReader(w, r.Body, s.cfg.StorageMaxFileMB*1024*1024)
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		f, h, err := r.FormFile("file")
		if err != nil {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("缺少 file 字段"))
			return
		}
		defer f.Close()
		data, err := io.ReadAll(f)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		if _, force, err := health.CheckDisk(s.cfg.DataDir, s.cfg.DiskWarnPercent, s.cfg.DiskForcePercent); err == nil && force {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("磁盘空间不足，拒绝上传"))
			return
		}
		id, err := s.storage.Put(projectID, appName, h.Filename, h.Header.Get("Content-Type"), data)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"id": id, "url": fmt.Sprintf("/files/%s/%s/%s", projectID, appName, id),
		})
	case action == "download" && r.Method == http.MethodGet:
		data, ct, err := s.storage.Get(projectID, appName, fileID)
		if err != nil {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		w.Header().Set("Content-Type", ct)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	case action == "delete" && r.Method == http.MethodDelete:
		if err := s.storage.Delete(projectID, appName, fileID); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	case action == "list" && r.Method == http.MethodGet:
		objs, err := s.storage.List(projectID, appName)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, objs)
	case action == "migrate":
		writeJSON(w, http.StatusOK, map[string]any{"status": "not_implemented", "notice": "外部 S3 迁移接口预留，版本二不实现"})
	default:
		http.NotFound(w, r)
	}
}

// handleFileAccess GET /files/<项目ID>/<应用名>/<文件ID> — 免 Token，供浏览器访问
func (s *Server) handleFileAccess(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/files/"), "/")
	if len(parts) != 3 {
		http.NotFound(w, r)
		return
	}
	data, ct, err := s.storage.Get(parts[0], parts[1], parts[2])
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", ct)
	_, _ = w.Write(data)
}

// handleLogsControl 日志开关与查询
func (s *Server) handleLogsControl(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var body struct {
			Scope    string `json:"scope"`
			AppName  string `json:"app_name"`
			Enabled  bool   `json:"enabled"`
			Duration int    `json:"duration_sec"`
			Level    string `json:"level"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		s.logger.Set(body.Scope, body.AppName, body.Enabled, time.Duration(body.Duration)*time.Second, body.Level)
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
		return
	}
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, s.logger.Status())
		return
	}
	http.NotFound(w, r)
}

// handleAudit GET /api/logs/audit — 查 call_logs
func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	logs, err := s.store.ListCallLogs("", limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, logs)
}

// 保留 os 和 filepath 导入（文件下载可能需要）
var _ = os.ReadFile
var _ = filepath.Join
