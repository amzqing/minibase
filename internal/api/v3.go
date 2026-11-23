package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"minibase/internal/database"
)

// handleEnv API: POST /api/apps/<项目ID>/<应用名>/env  查用 GET
func (s *Server) handleEnv(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/apps/"), "/")
	if len(parts) < 3 || parts[2] != "env" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("路径格式错误"))
		return
	}
	app, err := s.store.GetAppByName(parts[0], parts[1])
	if err != nil || app == nil {
		writeErr(w, http.StatusNotFound, fmt.Errorf("应用不存在"))
		return
	}
	if r.Method == http.MethodPost {
		// 统一机制：普通变量和密钥走同一个 API。
		// body: {"KEY":"value", ..., "_secret":["KEY2"]} — _secret 列出的键加密存 secrets 表。
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		secretKeys := map[string]bool{}
		if raw, ok := body["_secret"]; ok {
			var keys []string
			if err := json.Unmarshal(raw, &keys); err != nil {
				writeErr(w, http.StatusBadRequest, fmt.Errorf("_secret 必须是字符串数组"))
				return
			}
			for _, k := range keys {
				secretKeys[k] = true
			}
			delete(body, "_secret")
		}
		count := 0
		for k, raw := range body {
			if k == "" || strings.Contains(k, "=") {
				writeErr(w, http.StatusBadRequest, fmt.Errorf("变量名不合法: %q", k))
				return
			}
			var v string
			if err := json.Unmarshal(raw, &v); err != nil {
				writeErr(w, http.StatusBadRequest, fmt.Errorf("变量 %q 的值必须是字符串", k))
				return
			}
			if secretKeys[k] {
				enc, err := s.cipher.Encrypt(v)
				if err != nil {
					writeErr(w, http.StatusInternalServerError, err)
					return
				}
				if err := s.store.SaveSecret(&database.Secret{AppID: app.ID, Key: k, EncryptedValue: enc, Version: "", CreatedAt: time.Now().Unix()}); err != nil {
					writeErr(w, http.StatusInternalServerError, err)
					return
				}
			} else {
				if err := s.store.SaveAppEnv(&database.AppEnv{AppID: app.ID, Key: k, Value: v, Version: ""}); err != nil {
					writeErr(w, http.StatusInternalServerError, err)
					return
				}
			}
			count++
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "count": count, "notice": "下次部署生效"})
		return
	}
	// GET: 列出全部变量，密钥值用 <encrypted> 占位
	envs, _ := s.store.GetAppEnv(app.ID, "")
	out := []map[string]any{}
	for _, e := range envs {
		out = append(out, map[string]any{"key": e.Key, "value": e.Value, "is_secret": false})
	}
	secrets, _ := s.store.GetSecrets(app.ID, "")
	for _, sec := range secrets {
		out = append(out, map[string]any{"key": sec.Key, "value": "<encrypted>", "is_secret": true})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleDown DELETE /api/apps/<项目ID>/<应用名>
func (s *Server) handleDown(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/apps/"), "/")
	if len(parts) != 2 {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("路径格式错误"))
		return
	}
	if err := s.deployer.Down(parts[0], parts[1]); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "notice": "端口已回收"})
}

// handleAppLog GET /api/apps/<项目ID>/<应用名>/applog — 返回应用日志尾部
func (s *Server) handleAppLog(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/apps/"), "/")
	if len(parts) < 3 || parts[2] != "applog" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("路径格式错误"))
		return
	}
	app, err := s.store.GetAppByName(parts[0], parts[1])
	if err != nil || app == nil {
		writeErr(w, http.StatusNotFound, fmt.Errorf("应用不存在"))
		return
	}
	// 找当前版本目录的 app.log
	ver, _ := s.store.GetVersion(app.ID, app.CurrentVersion)
	if ver == nil || ver.SourceDir == "" {
		writeJSON(w, http.StatusOK, map[string]any{"logs": ""})
		return
	}
	b, err := os.ReadFile(filepath.Join(ver.SourceDir, "app.log"))
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"logs": ""})
		return
	}
	content := string(b)
	if len(content) > 8192 {
		content = content[len(content)-8192:]
	}
	writeJSON(w, http.StatusOK, map[string]any{"logs": content})
}
