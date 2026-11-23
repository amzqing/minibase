// Package monitor 实现守护机制：盯着应用状态，按分级策略响应。
package monitor

import (
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"minibase/internal/config"
	"minibase/internal/database"
)

// RestartFunc 重启应用的回调，由 deploy 层提供。
type RestartFunc func(app *database.App, version *database.Version) error

// Monitor 后台守护。
type Monitor struct {
	cfg       *config.Config
	store     *database.Store
	restartFn RestartFunc
	mu        sync.Mutex
	failCount map[string]int
	stopCh    chan struct{}
	wg        sync.WaitGroup
}

// New 创建守护。
func New(cfg *config.Config, store *database.Store, restartFn RestartFunc) *Monitor {
	return &Monitor{
		cfg: cfg, store: store, restartFn: restartFn,
		failCount: map[string]int{}, stopCh: make(chan struct{}),
	}
}

// Start 启动后台循环。
func (m *Monitor) Start() {
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		ticker := time.NewTicker(time.Duration(m.cfg.MonitorIntervalMS) * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				m.scan()
			case <-m.stopCh:
				return
			}
		}
	}()
	log.Printf("[monitor] 守护启动，间隔 %dms", m.cfg.MonitorIntervalMS)
}

// Stop 停止守护。
func (m *Monitor) Stop() {
	close(m.stopCh)
	m.wg.Wait()
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(signalZero) == nil
}

// scan 扫一遍所有应用。
func (m *Monitor) scan() {
	rows, err := m.store.DB.Query(`SELECT id FROM apps WHERE status IN ('运行中','不健康')`)
	if err != nil {
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()

	for _, id := range ids {
		app, err := m.store.GetAppByID(id)
		if err != nil || app == nil {
			continue
		}
		ver, err := m.store.GetVersion(app.ID, app.CurrentVersion)
		if err != nil || ver == nil {
			continue
		}

		// 检查资源用量：内存快超上限时按中度响应处理
		usedMB := ReadMemoryUsage(m.cfg.CgroupRoot, ver.SourceDir) / 1024 / 1024
		threshold := MemoryThresholdMB(m.cfg.MemoryLimitMB)
		if usedMB > 0 && usedMB > threshold {
			m.handleFailure(app, ver, fmt.Sprintf("内存快超上限: %dMB / %dMB", usedMB, m.cfg.MemoryLimitMB))
			continue
		}

		// 用 HTTP 探测健康：进程在但卡死时 TCP 仍可连，必须发请求才知道真状态
		probeTimeout := time.Duration(m.cfg.HealthProbeTimeoutMS) * time.Millisecond
		alive := httpHealthCheck(ver.Port, probeTimeout)
		if !alive {
			m.handleFailure(app, ver, "进程不响应")
			continue
		}
		// 探测成功，重置失败计数
		m.mu.Lock()
		if m.failCount[app.ID] > 0 {
			m.failCount[app.ID] = 0
			if app.Status == "不健康" {
				_ = m.store.UpdateAppStatus(app.ID, "运行中")
				_ = m.store.InsertCallLog(&database.CallLog{
					AppID: app.ID, ProjectID: app.ProjectID, Event: "状态变化",
					FromStatus: "不健康", ToStatus: "运行中", Reason: "探测恢复",
					CreatedAt: time.Now().Unix(),
				})
			}
		}
		m.mu.Unlock()
	}
}

func (m *Monitor) handleFailure(app *database.App, ver *database.Version, reason string) {
	m.mu.Lock()
	m.failCount[app.ID]++
	count := m.failCount[app.ID]
	m.mu.Unlock()

	if count < m.cfg.UnhealthyThreshold {
		return
	}
	if app.Status != "不健康" {
		_ = m.store.InsertCallLog(&database.CallLog{
			AppID: app.ID, ProjectID: app.ProjectID, Event: "状态变化",
			FromStatus: app.Status, ToStatus: "不健康", Reason: reason,
			CreatedAt: time.Now().Unix(),
		})
		_ = m.store.UpdateAppStatus(app.ID, "不健康")
	}

	// 崩溃循环检测
	now := time.Now().Unix()
	windowStart := app.RestartWindowStart
	if windowStart == 0 || now-windowStart > int64(m.cfg.RestartWindowSec) {
		windowStart = now
		_ = m.store.SetAppRestartInfo(app.ID, 0, windowStart)
	}
	if app.RestartCount >= m.cfg.MaxRestarts {
		_ = m.store.UpdateAppStatus(app.ID, "失败")
		_ = m.store.InsertCallLog(&database.CallLog{
			AppID: app.ID, ProjectID: app.ProjectID, Event: "停止重启",
			FromStatus: "不健康", ToStatus: "失败", Reason: "崩溃循环达到上限",
			CreatedAt: now,
		})
		log.Printf("[monitor] 应用 %s 重启达上限，标记失败", app.Name)
		return
	}

	// 中度响应：重启应用
	_ = m.store.SetAppRestartInfo(app.ID, app.RestartCount+1, windowStart)
	_ = m.store.InsertCallLog(&database.CallLog{
		AppID: app.ID, ProjectID: app.ProjectID, Event: "重启应用",
		FromStatus: "不健康", ToStatus: "启动中", Reason: reason,
		CreatedAt: now,
	})
	_ = m.store.UpdateAppStatus(app.ID, "启动中")
	if m.restartFn != nil {
		if err := m.restartFn(app, ver); err != nil {
			_ = m.store.UpdateAppStatus(app.ID, "失败")
			_ = m.store.InsertCallLog(&database.CallLog{
				AppID: app.ID, ProjectID: app.ProjectID, Event: "重启失败",
				FromStatus: "启动中", ToStatus: "失败", Reason: err.Error(),
				CreatedAt: time.Now().Unix(),
			})
		}
	}
	m.mu.Lock()
	m.failCount[app.ID] = 0
	m.mu.Unlock()
}
