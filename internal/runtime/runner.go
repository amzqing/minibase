package runtime

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// ProcessHandle 是一个已启动应用的句柄。
type ProcessHandle struct {
	PID  int
	Port int
	stop func() error
}

// NewProcessHandle 构造进程句柄（逻辑测试的假 Runner 也用它）。
func NewProcessHandle(pid, port int, stop func() error) *ProcessHandle {
	return &ProcessHandle{PID: pid, Port: port, stop: stop}
}

// Stop 停止应用进程。
func (h *ProcessHandle) Stop() error {
	if h == nil || h.stop == nil {
		return nil
	}
	return h.stop()
}

// AppRunner 抽象应用的启动/健康检查/停止。
// 逻辑测试用假实现，不依赖真实 Node 和端口。
type AppRunner interface {
	Start(versionDir, startCmd string, env []string, port int) (*ProcessHandle, error)
	HealthCheck(port int, timeout time.Duration) bool
}

// NodeRunner 用 sh -c 启动 Node 应用。
// 探测间隔、单次超时、停止等待都来自配置，不在代码里写死。
type NodeRunner struct {
	// LogApp 应用输出回调（由 logger 提供，可为 nil）
	LogApp              func(appName, line string)
	HealthRetryInterval time.Duration
	HealthProbeTimeout  time.Duration
	ProcessStopTimeout  time.Duration
}

// Start 启动应用，进程独立成组，便于整组停止。
func (r *NodeRunner) Start(versionDir, startCmd string, env []string, port int) (*ProcessHandle, error) {
	if startCmd == "" {
		return nil, fmt.Errorf("启动命令为空")
	}
	logPath := filepath.Join(versionDir, "app.log")
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("打开应用日志失败: %w", err)
	}
	cmd := exec.Command("sh", "-c", "exec "+startCmd)
	cmd.Dir = versionDir
	cmd.Env = append(os.Environ(), env...)
	if r.LogApp != nil {
		name, prefix := appLogInfo(env)
		cmd.Stdout = io.MultiWriter(logf, &logWriter{fn: func(line string) { r.LogApp(name, prefix+line) }})
		cmd.Stderr = io.MultiWriter(logf, &logWriter{fn: func(line string) { r.LogApp(name, prefix+line) }})
	} else {
		cmd.Stdout = logf
		cmd.Stderr = logf
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		logf.Close()
		return nil, fmt.Errorf("启动应用进程失败: %w", err)
	}
	pid := cmd.Process.Pid
	go func() { _ = cmd.Wait(); _ = logf.Close() }()

	stop := func() error { return r.stopProcessGroup(pid) }
	return &ProcessHandle{PID: pid, Port: port, stop: stop}, nil
}

// Reattach 根据 PID 重建旧应用进程句柄。
// 热重载后，新 Core 用这个方法重新认识升级前已经在跑的应用。
func (r *NodeRunner) Reattach(pid, port int) *ProcessHandle {
	if pid <= 0 || !processAlive(pid) {
		return nil
	}
	return &ProcessHandle{PID: pid, Port: port, stop: func() error { return r.stopProcessGroup(pid) }}
}

// stopProcessGroup 先礼貌停止，再按需强杀整个进程组。
func (r *NodeRunner) stopProcessGroup(pid int) error {
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	deadline := time.Now().Add(r.processStop())
	for processAlive(pid) && time.Now().Before(deadline) {
		time.Sleep(r.healthRetry())
	}
	if processAlive(pid) {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}
	return nil
}

func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

// HealthCheck 在超时时间内反复探测应用端口，有 HTTP 回应就算健康。
// 必须重试：进程启动后到真正监听端口之间有一小段空窗。
func (r *NodeRunner) HealthCheck(port int, timeout time.Duration) bool {
	if timeout <= 0 {
		timeout = r.healthProbe() * 3
	}
	deadline := time.Now().Add(timeout)
	probeTimeout := r.healthProbe()
	client := &http.Client{Timeout: probeTimeout}
	url := fmt.Sprintf("http://127.0.0.1:%d/", port)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return false
		}
		attemptTimeout := remaining
		if attemptTimeout > probeTimeout {
			attemptTimeout = probeTimeout
		}
		ctx, cancel := context.WithTimeout(context.Background(), attemptTimeout)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err == nil {
			resp, doErr := client.Do(req)
			if doErr == nil {
				resp.Body.Close()
				cancel()
				return true
			}
		}
		cancel()
		if time.Until(deadline) <= 0 {
			return false
		}
		time.Sleep(r.healthRetry())
	}
}

// PortInUse 判断端口是否被占用（分配端口时用）。
func PortInUse(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return true
	}
	ln.Close()
	return false
}

// healthRetry 返回探测间隔，配置为零时用保守兜底（仅防配置漏填）。
func (r *NodeRunner) healthRetry() time.Duration {
	if r.HealthRetryInterval > 0 {
		return r.HealthRetryInterval
	}
	return 100 * time.Millisecond
}

// healthProbe 返回单次探测超时，配置为零时用保守兜底。
func (r *NodeRunner) healthProbe() time.Duration {
	if r.HealthProbeTimeout > 0 {
		return r.HealthProbeTimeout
	}
	return time.Second
}

// processStop 返回停止应用等待时长，配置为零时用保守兜底。
func (r *NodeRunner) processStop() time.Duration {
	if r.ProcessStopTimeout > 0 {
		return r.ProcessStopTimeout
	}
	return 5 * time.Second
}

// logWriter 把应用输出逐行回调出去。
type logWriter struct {
	buf []byte
	fn  func(string)
}

func (w *logWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := indexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		w.fn(string(w.buf[:i]))
		w.buf = w.buf[i+1:]
	}
	return len(p), nil
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}

// appLogPrefix 从 MOUNT_PATH 环境变量解析出 [项目ID/应用名] 前缀。
func appLogInfo(env []string) (string, string) {
	for _, e := range env {
		if strings.HasPrefix(e, "MOUNT_PATH=") {
			parts := strings.Split(strings.TrimPrefix(e, "MOUNT_PATH="), "/")
			if len(parts) >= 4 {
				return parts[3], fmt.Sprintf("[%s/%s] ", parts[2], parts[3])
			}
		}
	}
	return "unknown", "[unknown] "
}
