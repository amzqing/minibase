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

func (s *Server) handleEnv(w http.ResponseWriter, r *http.Request, appName string) {
	app, err := s.store.GetAppByName(appName)
	if err != nil || app == nil {
		writeErr(w, http.StatusNotFound, fmt.Errorf("应用不存在"))
		return
	}
	if r.Method == http.MethodPost {
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
				if err := s.store.SaveSecret(&database.Secret{AppID: app.ID, Key: k, EncryptedValue: enc,
					Version: "", CreatedAt: time.Now().Unix()}); err != nil {
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

// logLine 是给管控面板展示的结构化日志行。
type logLine struct {
	Time    string `json:"time"`
	Level   string `json:"level"`
	Message string `json:"message"`
	Raw     string `json:"raw"`
}

// handleAppLog 返回应用日志：原始文本 + 结构化行，供表格展示。
func (s *Server) handleAppLog(w http.ResponseWriter, _ *http.Request, appName string) {
	app, err := s.store.GetAppByName(appName)
	if err != nil || app == nil {
		writeErr(w, http.StatusNotFound, fmt.Errorf("应用不存在"))
		return
	}
	content := ""
	ver, _ := s.store.GetVersion(app.ID, app.CurrentVersion)
	if ver != nil && ver.SourceDir != "" {
		if b, readErr := os.ReadFile(filepath.Join(ver.SourceDir, "app.log")); readErr == nil {
			content = string(b)
		}
	}
	if len(content) > 8192 {
		content = content[len(content)-8192:]
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"logs":  content,
		"lines": parseLogLines(content),
	})
}

// parseLogLines 把纯文本日志解析成表格行。识别 JSON 日志、时间戳前缀和级别标记；
// 识别不出来的行原样作为 INFO 内容，不丢数据。
func parseLogLines(content string) []logLine {
	out := make([]logLine, 0)
	for _, raw := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		if raw == "" {
			continue
		}
		line := parseLogLine(raw)
		out = append(out, line)
	}
	return out
}

func parseLogLine(raw string) logLine {
	line := logLine{Level: "INFO", Message: raw, Raw: raw}

	// JSON 日志：{"time":"...","level":"error","msg":"..."}
	var obj map[string]any
	if json.Unmarshal([]byte(raw), &obj) == nil {
		pick := func(keys ...string) string {
			for _, k := range keys {
				if v, ok := obj[k]; ok {
					if str, ok := v.(string); ok {
						return str
					}
				}
			}
			return ""
		}
		t := pick("time", "timestamp", "@timestamp", "ts")
		lv := pick("level", "lvl", "severity")
		msg := pick("msg", "message", "log")
		if msg == "" {
			b, _ := json.Marshal(obj)
			msg = string(b)
		}
		if t != "" {
			line.Time = normalizeLogTime(t)
		}
		if lv != "" {
			line.Level = normalizeLogLevel(lv)
		}
		line.Message = msg
		return line
	}

	rest := strings.TrimSpace(raw)
	// 方括号级别：[ERROR] xxx
	if strings.HasPrefix(rest, "[") {
		if end := strings.Index(rest, "]"); end > 1 {
			level := strings.ToUpper(strings.TrimSpace(rest[1:end]))
			if isLogLevel(level) {
				line.Level = level
				rest = strings.TrimSpace(rest[end+1:])
			}
		}
	}

	// 时间戳前缀：2026-11-24T10:00:00Z / 2026-11-24 10:00:00 / 2026/11/24 10:00:00
	if len(rest) >= 19 {
		candidate := rest[:19]
		if _, err := time.Parse("2006-01-02T15:04:05", candidate); err == nil ||
			(len(candidate) == 19 && candidate[4] == '-' && candidate[7] == '-' && candidate[10] == ' ') {
			line.Time = normalizeLogTime(candidate)
			rest = strings.TrimSpace(rest[19:])
		}
	}

	// 裸级别前缀：INFO xxx / ERROR: xxx
	if fields := strings.Fields(rest); len(fields) > 0 {
		level := strings.ToUpper(strings.Trim(fields[0], "[:"))
		if isLogLevel(level) {
			line.Level = level
			rest = strings.TrimSpace(strings.TrimPrefix(rest, fields[0]))
		}
	}

	if rest != "" {
		line.Message = rest
	}
	return line
}

func normalizeLogTime(value string) string {
	for _, layout := range []string{
		time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05",
		"2006-01-02 15:04:05", "2006/01/02 15:04:05",
	} {
		if t, err := time.Parse(layout, value); err == nil {
			return t.Format("01-02 15:04:05")
		}
	}
	return value
}

func isLogLevel(level string) bool {
	switch strings.ToUpper(level) {
	case "TRACE", "DEBUG", "INFO", "WARN", "WARNING", "ERROR", "FATAL", "PANIC":
		return true
	}
	return false
}

func normalizeLogLevel(value string) string {
	level := strings.ToUpper(strings.TrimSpace(value))
	if level == "WARNING" {
		return "WARN"
	}
	if !isLogLevel(level) {
		return "INFO"
	}
	return level
}
