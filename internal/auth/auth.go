// Package auth 实现管理员 Token 认证。
package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"

	"minibase/internal/config"
)

// HashToken 返回 token 的哈希，格式 sha256:<hex>。
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Middleware 校验 Authorization: Bearer <token>。
// 配置里 token_hash 优先；为空时用明文 token 兼容旧配置。
func Middleware(cfg *config.Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := r.Header.Get("Authorization")
			if !strings.HasPrefix(h, "Bearer ") {
				http.Error(w, `{"error":"未授权：缺少 Bearer Token"}`, http.StatusUnauthorized)
				return
			}
			got := strings.TrimPrefix(h, "Bearer ")
			ok := false
			if cfg.TokenHash != "" {
				ok = subtle.ConstantTimeCompare([]byte(HashToken(got)), []byte(cfg.TokenHash)) == 1
			} else {
				ok = subtle.ConstantTimeCompare([]byte(got), []byte(cfg.Token)) == 1
			}
			if !ok {
				http.Error(w, `{"error":"未授权：Token 错误"}`, http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
