package api

import (
	"fmt"
	"net/http"
	"syscall"

	"minibase/internal/health"
	"minibase/internal/monitor"
)

// handleUsage GET /api/usage — 系统资源用量总览。
func (s *Server) handleUsage(w http.ResponseWriter, _ *http.Request) {
	percent, _, err := health.CheckDisk(s.cfg.DataDir, s.cfg.DiskWarnPercent, s.cfg.DiskForcePercent)
	disk := map[string]any{"used_percent": percent, "warn_percent": s.cfg.DiskWarnPercent, "force_percent": s.cfg.DiskForcePercent}
	if err == nil {
		if total, free, e := diskBytes(s.cfg.DataDir); e == nil {
			disk["total_mb"] = total / 1024 / 1024
			disk["free_mb"] = free / 1024 / 1024
			disk["used_mb"] = (total - free) / 1024 / 1024
		}
	} else {
		disk["error"] = err.Error()
	}

	var running, stopped, failed int
	rows, err := s.store.DB.Query(`SELECT status, COUNT(*) FROM apps GROUP BY status`)
	if err == nil {
		for rows.Next() {
			var st string
			var n int
			if rows.Scan(&st, &n) == nil {
				switch st {
				case "运行中":
					running = n
				case "已暂停", "已停止":
					stopped = n
				case "失败":
					failed = n
				}
			}
		}
		rows.Close()
	}
	var appMemMB int64
	var vers []*versionRef
	rows, err = s.store.DB.Query(`SELECT id, current_version, source_dir FROM apps WHERE current_version != ''`)
	if err == nil {
		for rows.Next() {
			var id, ver, dir string
			if rows.Scan(&id, &ver, &dir) != nil {
				continue
			}
			vers = append(vers, &versionRef{id: id, ver: ver, dir: dir})
		}
		rows.Close()
	}
	for _, v := range vers {
		appMemMB += monitor.ReadMemoryUsage(s.cfg.CgroupRoot, v.dir)
	}

	var storageTotal int64
	_ = s.store.DB.QueryRow(`SELECT COALESCE(SUM(size),0) FROM objects`).Scan(&storageTotal)

	var portsUsed int
	var portMin, portMax int
	_ = s.store.DB.QueryRow(`SELECT COUNT(DISTINCT port), COALESCE(MIN(port),0), COALESCE(MAX(port),0) FROM versions WHERE port > 0`).Scan(&portsUsed, &portMin, &portMax)

	writeJSON(w, http.StatusOK, map[string]any{
		"disk": disk,
		"memory": map[string]any{
			"apps_used_mb":     appMemMB / 1024 / 1024,
			"per_app_limit_mb": s.cfg.MemoryLimitMB,
			"note":             "apps_used_mb 是全部应用之和，per_app_limit_mb 是单个应用上限",
		},
		"apps": map[string]any{
			"running": running, "stopped": stopped, "failed": failed,
			"max_concurrent": s.cfg.MaxConcurrentApps,
		},
		"ports": map[string]any{
			"used":  portsUsed,
			"range": fmt.Sprintf("%d-%d", s.cfg.PortStart, s.cfg.PortEnd),
		},
		"storage": map[string]any{
			"used_mb":  storageTotal / 1024 / 1024,
			"limit_mb": s.cfg.StorageMaxMBTotal,
		},
	})
}

type versionRef struct{ id, ver, dir string }

// handleAppUsage GET /api/apps/{app}/usage
func (s *Server) handleAppUsage(w http.ResponseWriter, _ *http.Request, appName string) {
	app, err := s.store.GetAppByName(appName)
	if err != nil || app == nil {
		writeErr(w, http.StatusNotFound, fmt.Errorf("应用不存在"))
		return
	}
	ver, _ := s.store.GetVersion(app.ID, app.CurrentVersion)
	var memBytes int64
	if ver != nil {
		memBytes = monitor.ReadMemoryUsage(s.cfg.CgroupRoot, ver.SourceDir)
	}
	var storage int64
	_ = s.store.DB.QueryRow(`SELECT COALESCE(SUM(size),0) FROM objects WHERE app_name=?`, appName).Scan(&storage)
	limit, _, _, _ := s.deployer.EffectiveConfig(app.ID)
	writeJSON(w, http.StatusOK, map[string]any{
		"app_name": appName, "status": app.Status,
		"memory_mb": memBytes / 1024 / 1024, "memory_limit_mb": limit,
		"storage_mb":      storage / 1024 / 1024,
		"current_version": app.CurrentVersion, "port": app.Port,
		"restart_count": app.RestartCount,
	})
}

func diskBytes(path string) (total, free uint64, err error) {
	var st syscall.Statfs_t
	if err = syscall.Statfs(path, &st); err != nil {
		return
	}
	total = st.Blocks * uint64(st.Bsize)
	free = st.Bavail * uint64(st.Bsize)
	return
}
