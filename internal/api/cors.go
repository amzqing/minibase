package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"minibase/internal/database"
)

func (s *Server) handleCors(w http.ResponseWriter, r *http.Request, appName string, _ []string) {
	switch r.Method {
	case http.MethodGet:
		list, err := s.store.ListCorsDomains(appName)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		if list == nil {
			list = []*database.CorsDomain{}
		}
		writeJSON(w, http.StatusOK, list)
	case http.MethodPost:
		var req struct {
			Domain string `json:"domain"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Domain) == "" {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("需要提供 domain"))
			return
		}
		c, err := s.store.AddCorsDomain(appName, req.Domain)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		_ = s.store.InsertCallLog(&database.CallLog{Event: "cors_added", ToStatus: appName + ":" + req.Domain, CreatedAt: time.Now().Unix()})
		writeJSON(w, http.StatusOK, c)
	default:
		writeErr(w, http.StatusMethodNotAllowed, fmt.Errorf("不支持的方法"))
	}
}

func (s *Server) handleCorsDelete(w http.ResponseWriter, _ *http.Request, appName string, rest []string) {
	if len(rest) != 1 || rest[0] == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("路径格式错误"))
		return
	}
	id := rest[0]
	if err := s.store.DeleteCorsDomain(appName, id); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	_ = s.store.InsertCallLog(&database.CallLog{Event: "cors_deleted", ToStatus: appName + ":" + id, CreatedAt: time.Now().Unix()})
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}
