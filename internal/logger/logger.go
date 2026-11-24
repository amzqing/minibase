// Package logger 管理日志开关与实际采集。
// 排障日志走 journald（systemd stdout 自动收），审计账本走 call_logs。
package logger

import (
	"log"
	"sync"
	"time"
)

// Logger 管理日志开关。
type Logger struct {
	mu      sync.Mutex
	control map[string]*Control
}

// Control 一个日志开关的状态。
type Control struct {
	Scope   string `json:"scope"`
	AppName string `json:"app_name"`
	Enabled bool   `json:"enabled"`
	Level   string `json:"level"`
	Expires int64  `json:"expires_at"`
}

// New 创建日志管理器。
func New() *Logger { return &Logger{control: map[string]*Control{}} }

// Set 开/关日志，可带自动关闭时长。
func (l *Logger) Set(scope, appName string, enabled bool, duration time.Duration, level string) {
	if level == "" {
		level = "error"
	}
	key := keyOf(scope, appName)
	l.mu.Lock()
	defer l.mu.Unlock()
	c := &Control{Scope: scope, AppName: appName, Enabled: enabled, Level: level}
	if enabled && duration > 0 {
		c.Expires = time.Now().Add(duration).Unix()
		go func(k string, d time.Duration) {
			time.Sleep(d)
			l.mu.Lock()
			if cur, ok := l.control[k]; ok && cur.Expires > 0 && cur.Expires <= time.Now().Unix() {
				cur.Enabled = false
				cur.Expires = 0
			}
			l.mu.Unlock()
			log.Printf("[logger] 日志开关到期自动关闭: %s", k)
		}(key, duration)
	}
	l.control[key] = c
	log.Printf("[logger] 日志开关 %s: enabled=%v level=%s", key, enabled, level)
}

func keyOf(scope, appName string) string {
	if appName != "" {
		return scope + "/" + appName
	}
	return scope
}

// IsEnabled 查某个日志是否开着（并检查到期）。
func (l *Logger) IsEnabled(scope, appName string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	c, ok := l.control[keyOf(scope, appName)]
	if !ok || !c.Enabled {
		return false
	}
	if c.Expires > 0 && c.Expires <= time.Now().Unix() {
		c.Enabled = false
		c.Expires = 0
		return false
	}
	return true
}

// Level 返回当前日志级别。
func (l *Logger) Level(scope, appName string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if c, ok := l.control[keyOf(scope, appName)]; ok {
		return c.Level
	}
	return "error"
}

// Status 返回所有开关状态。
func (l *Logger) Status() map[string]*Control {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := map[string]*Control{}
	for k, c := range l.control {
		if c.Expires > 0 && c.Expires <= time.Now().Unix() {
			c.Enabled = false
			c.Expires = 0
		}
		out[k] = c
	}
	return out
}

// LogControl 管控详细日志：开关打开时才写，带 minicore 标识。
func (l *Logger) LogControl(format string, args ...any) {
	if !l.IsEnabled("control", "") {
		return
	}
	log.Printf("[minicore] "+format, args...)
}

func containsError(s string) bool {
	return len(s) >= 5 && (contains(s, "error") || contains(s, "ERROR") || contains(s, "Error"))
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// LogAppRaw 输出应用日志行，按应用开关过滤，带应用标识。
func (l *Logger) LogAppRaw(appName, line string) {
	if !l.IsEnabled("app", appName) {
		return
	}
	log.Printf("[app-%s] %s", appName, line)
}
