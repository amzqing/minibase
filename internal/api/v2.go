package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"minibase/internal/database"
	"minibase/internal/health"
)

func (s *Server) handleRollback(w http.ResponseWriter, r *http.Request, appName string) {
	res, err := s.deployer.Rollback(appName)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok", "version": res.Version, "port": res.Port,
		"access_url": fmt.Sprintf("http://%s%s", r.Host, res.RootPath),
	})
}

func (s *Server) handleAppStatus(w http.ResponseWriter, r *http.Request, appName string) {
	app, err := s.store.GetAppByName(appName)
	if err != nil || app == nil {
		writeErr(w, http.StatusNotFound, fmt.Errorf("应用不存在"))
		return
	}
	logs, _ := s.store.ListCallLogs(app.ID, 20)
	writeJSON(w, http.StatusOK, map[string]any{
		"app_name": app.Name, "status": app.Status,
		"current_version": app.CurrentVersion, "previous_version": app.PreviousVersion,
		"restart_count": app.RestartCount, "history": logs,
		"access_url": fmt.Sprintf("http://%s%s", r.Host, app.RootPath),
	})
}

func (s *Server) handleSecrets(w http.ResponseWriter, r *http.Request, appName string) {
	app, err := s.store.GetAppByName(appName)
	if err != nil || app == nil {
		writeErr(w, http.StatusNotFound, fmt.Errorf("应用不存在"))
		return
	}
	switch r.Method {
	case http.MethodGet:
		secrets, _ := s.store.GetSecrets(app.ID, "")
		out := []map[string]any{}
		for _, sec := range secrets {
			out = append(out, map[string]any{"key": sec.Key, "value": "<encrypted>", "is_secret": true})
		}
		writeJSON(w, http.StatusOK, out)
	case http.MethodPost:
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
			sec := &database.Secret{AppID: app.ID, Key: k, EncryptedValue: enc,
				Version: version, CreatedAt: time.Now().Unix()}
			if err := s.store.SaveSecret(sec); err != nil {
				writeErr(w, http.StatusInternalServerError, err)
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "count": len(body)})
	default:
		writeErr(w, http.StatusMethodNotAllowed, fmt.Errorf("不支持的方法"))
	}
}

// handleStorage 处理对象存储动作。外部路径 /apps/{app}/files 和兼容路径 /api/storage/* 都走这里。
func (s *Server) handleStorage(w http.ResponseWriter, r *http.Request, action, appName, fileID string) {
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
		id, err := s.storage.Put(appName, h.Filename, h.Header.Get("Content-Type"), data)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "url": fmt.Sprintf("/files/%s/%s", appName, id)})
	case action == "download" && r.Method == http.MethodGet:
		data, ct, err := s.storage.Get(appName, fileID)
		if err != nil {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		w.Header().Set("Content-Type", ct)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	case action == "delete" && r.Method == http.MethodDelete:
		if err := s.storage.Delete(appName, fileID); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	case action == "list" && r.Method == http.MethodGet:
		objs, err := s.storage.List(appName)
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

// handleFileAccess GET /files/{app}/{id} — 免 Token，供浏览器访问。
func (s *Server) handleFileAccess(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/files/"), "/")
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	data, ct, err := s.storage.Get(parts[0], parts[1])
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", ct)
	_, _ = w.Write(data)
}

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
