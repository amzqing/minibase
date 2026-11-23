package monitor

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const signalZero = syscall.Signal(0)

// httpHealthCheck 用 HTTP 请求探测应用，有响应才算健康。
// 不能用 TCP 连接探测：进程在但卡死时 TCP 仍能连上，发现不了问题。
func httpHealthCheck(port int, timeout time.Duration) bool { return HTTPHealthCheck(port, timeout) }

// HTTPHealthCheck 导出供测试使用。
func HTTPHealthCheck(port int, timeout time.Duration) bool {
	if port <= 0 {
		return false
	}
	if timeout <= 0 {
		timeout = time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("http://127.0.0.1:%d/", port), nil)
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return true
}

// ReadMemoryUsage 读 cgroup v2 的 memory.current，返回字节数。
// 读不到时返回 0（不阻断守护）。
func ReadMemoryUsage(cgroupRoot, versionDir string) int64 {
	if cgroupRoot == "" || versionDir == "" {
		return 0
	}
	// 与 limiter 的 key 规则一致：用版本目录路径的 safeKey
	key := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, versionDir)
	path := filepath.Join(cgroupRoot, "apps", key, "memory.current")
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// MemoryThresholdMB 内存快超阈值：上限的 90%。
func MemoryThresholdMB(limitMB int) int64 {
	return int64(limitMB) * 90 / 100
}
