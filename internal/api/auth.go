package api

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"time"

	"minibase/internal/database"
)

// handleTokenRotate POST /api/auth/token/rotate
func (s *Server) handleTokenRotate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, fmt.Errorf("需要 POST"))
		return
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	newToken := base64.RawURLEncoding.EncodeToString(b)
	if err := s.cfg.SetTokenHash(newToken); err != nil {
		writeErr(w, http.StatusInternalServerError, fmt.Errorf("写配置失败: %w", err))
		return
	}
	_ = s.store.InsertCallLog(&database.CallLog{
		Event: "token_rotated", ToStatus: "成功", CreatedAt: time.Now().Unix(),
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"token":  newToken,
		"notice": "请立即更新客户端的 MINIBASE_TOKEN；此 Token 只显示一次",
	})
}
