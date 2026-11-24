package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"minibase/internal/database"
)

// checkStatus 校验操作前后状态，不合法写 409。
func (s *Server) checkStatus(w http.ResponseWriter, appName string, allowed ...string) *database.App {
	app, err := s.store.GetAppByName(appName)
	if err != nil || app == nil {
		writeErr(w, http.StatusNotFound, fmt.Errorf("应用不存在"))
		return nil
	}
	for _, a := range allowed {
		if app.Status == a {
			return app
		}
	}
	writeJSON(w, http.StatusConflict, map[string]any{
		"error":          fmt.Sprintf("当前状态 %q 不允许该操作", app.Status),
		"current_status": app.Status, "allowed": allowed,
	})
	return nil
}

func (s *Server) handleResume(w http.ResponseWriter, _ *http.Request, appName string) {
	if s.checkStatus(w, appName, "已暂停") == nil {
		return
	}
	res, err := s.deployer.Resume(appName)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleRestart(w http.ResponseWriter, _ *http.Request, appName string) {
	if s.checkStatus(w, appName, "运行中", "不健康", "失败") == nil {
		return
	}
	res, err := s.deployer.Restart(appName)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handlePause(w http.ResponseWriter, _ *http.Request, appName string) {
	if s.checkStatus(w, appName, "运行中", "不健康") == nil {
		return
	}
	if err := s.deployer.Pause(appName); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "notice": "已暂停，数据保留"})
}

func (s *Server) handleDestroy(w http.ResponseWriter, _ *http.Request, appName string) {
	app, err := s.store.GetAppByName(appName)
	if err != nil || app == nil {
		writeErr(w, http.StatusNotFound, fmt.Errorf("应用不存在"))
		return
	}
	if err := s.deployer.Destroy(appName); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "notice": "应用已彻底删除"})
}

func (s *Server) handleAppEdit(w http.ResponseWriter, r *http.Request, appName string) {
	app := s.checkStatus(w, appName, "已暂停")
	if app == nil {
		return
	}
	var req struct {
		MemoryLimitMB   *int    `json:"memory_limit_mb"`
		CPUQuotaPercent *int    `json:"cpu_quota_percent"`
		StartCmd        *string `json:"start_cmd"`
		HealthPath      *string `json:"health_path"`
		HealthTimeoutMS *int    `json:"health_timeout_ms"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("请求体解析失败: %w", err))
		return
	}
	if req.MemoryLimitMB != nil && (*req.MemoryLimitMB < 32 || *req.MemoryLimitMB > 4096) {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("memory_limit_mb 应在 32-4096"))
		return
	}
	if req.CPUQuotaPercent != nil && (*req.CPUQuotaPercent < 1 || *req.CPUQuotaPercent > 100) {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("cpu_quota_percent 应在 1-100"))
		return
	}
	if req.HealthTimeoutMS != nil && (*req.HealthTimeoutMS < 100 || *req.HealthTimeoutMS > 60000) {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("health_timeout_ms 应在 100-60000"))
		return
	}
	cfg := &database.AppConfig{AppID: app.ID, MemoryLimitMB: req.MemoryLimitMB,
		CPUQuotaPercent: req.CPUQuotaPercent, StartCmd: req.StartCmd,
		HealthPath: req.HealthPath, HealthTimeoutMS: req.HealthTimeoutMS}
	if err := s.store.SetAppConfig(app.ID, cfg); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	_ = s.store.InsertCallLog(&database.CallLog{AppID: app.ID, Event: "app_updated",
		FromStatus: app.Status, ToStatus: app.Status, Reason: "用户修改应用配置", CreatedAt: time.Now().Unix()})
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "notice": "配置已保存，下次启动生效"})
}

func (s *Server) handleVersions(w http.ResponseWriter, _ *http.Request, appName string) {
	app, err := s.store.GetAppByName(appName)
	if err != nil || app == nil {
		writeErr(w, http.StatusNotFound, fmt.Errorf("应用不存在"))
		return
	}
	versions, err := s.store.ListVersions(app.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, versions)
}

func (s *Server) handleDeployments(w http.ResponseWriter, _ *http.Request, appName string) {
	app, err := s.store.GetAppByName(appName)
	if err != nil || app == nil {
		writeErr(w, http.StatusNotFound, fmt.Errorf("应用不存在"))
		return
	}
	logs, err := s.store.ListCallLogs(app.ID, 100)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	out := []*database.CallLog{}
	for _, l := range logs {
		if strings.Contains(l.Event, "部署") || strings.HasPrefix(l.Event, "app_") || l.Event == "回滚" {
			out = append(out, l)
		}
	}
	writeJSON(w, http.StatusOK, out)
}
