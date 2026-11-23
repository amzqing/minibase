// Package auth 实现第一版唯一的管理员 Token 认证。
package auth

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// Middleware 返回校验 Authorization: Bearer <token> 的中间件。
// 不匹配一律 401。
func Middleware(token string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := r.Header.Get("Authorization")
			if !strings.HasPrefix(h, "Bearer ") {
				http.Error(w, `{"error":"未授权：缺少 Bearer Token"}`, http.StatusUnauthorized)
				return
			}
			got := strings.TrimPrefix(h, "Bearer ")
			if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
				http.Error(w, `{"error":"未授权：Token 错误"}`, http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
