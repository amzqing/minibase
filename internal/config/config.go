// Package config 保存 minicore 的全部运行配置。
// 所有数字默认值为保守占位，标注「待实测」；真机压测后回填。
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Config 是 minicore 的运行配置。
type Config struct {
	ListenAddr string `json:"listen_addr"`

	// Token 管理员令牌。可用环境变量 MINICLOUD_TOKEN 覆盖。
	Token string `json:"token"`

	// DataDir 数据根目录，下面放 system.db / databases / apps。
	DataDir string `json:"data_dir"`

	// PortStart / PortEnd 应用端口池范围。待实测。
	PortStart int `json:"port_start"`
	PortEnd   int `json:"port_end"`

	// BusyTimeoutMS SQLite busy timeout，毫秒。待实测。
	BusyTimeoutMS int `json:"busy_timeout_ms"`

	// SQLTimeoutMS 单次数据库操作超时，毫秒。待实测。
	SQLTimeoutMS int `json:"sql_timeout_ms"`

	// HealthTimeoutMS 健康检查超时，毫秒。待实测。
	HealthTimeoutMS int `json:"health_timeout_ms"`

	// MemoryLimitMB 单个应用内存上限。待实测。
	MemoryLimitMB int `json:"memory_limit_mb"`

	// NPMInstallTimeoutMS 模式 B 构建超时，毫秒。待实测。
	NPMInstallTimeoutMS int `json:"npm_install_timeout_ms"`

	// MaxUnpackedMB 解压后总大小上限。待实测。
	MaxUnpackedMB int64 `json:"max_unpacked_mb"`

	// HealthRetryIntervalMS 健康检查重试间隔，毫秒。待实测。
	HealthRetryIntervalMS int `json:"health_retry_interval_ms"`

	// HealthProbeTimeoutMS 单次健康探测超时，毫秒。待实测。
	HealthProbeTimeoutMS int `json:"health_probe_timeout_ms"`

	// ProcessStopTimeoutMS 停止应用进程等待时长，毫秒。待实测。
	ProcessStopTimeoutMS int `json:"process_stop_timeout_ms"`

	// MaxUploadMB 单个上传 zip 大小上限，MB。待实测。
	MaxUploadMB int64 `json:"max_upload_mb"`

	// MultipartMemoryMB 解析上传表单时放内存的上限，MB。待实测。
	MultipartMemoryMB int64 `json:"multipart_memory_mb"`

	// ReadHeaderTimeoutMS HTTP 读请求头超时，毫秒。待实测。
	ReadHeaderTimeoutMS int `json:"read_header_timeout_ms"`

	// ShutdownTimeoutMS Core 优雅关闭超时，毫秒。待实测。
	ShutdownTimeoutMS int `json:"shutdown_timeout_ms"`

	// LogTailBytes 失败时带上的应用日志尾部大小，字节。待实测。
	LogTailBytes int `json:"log_tail_bytes"`

	// CgroupRoot cgroups v2 挂载点，Linux 下写 memory.max 用。
	CgroupRoot string `json:"cgroup_root"`

	// ===== 版本二新增配置，全部待实测 =====

	// MasterKey 加密密钥的主密钥，管理员提供。丢失则密钥不可恢复。
	MasterKey string `json:"master_key"`

	// CPUQuotaPercent 单个应用 CPU 配额百分比。待实测。
	CPUQuotaPercent int `json:"cpu_quota_percent"`

	// MaxConcurrentApps 同时运行的应用数上限。待实测。
	MaxConcurrentApps int `json:"max_concurrent_apps"`

	// MonitorIntervalMS 守护探测间隔，毫秒。待实测。
	MonitorIntervalMS int `json:"monitor_interval_ms"`

	// UnhealthyThreshold 连续探测失败多少次判定不健康。待实测。
	UnhealthyThreshold int `json:"unhealthy_threshold"`

	// MaxRestarts 崩溃重启上限。待实测。
	MaxRestarts int `json:"max_restarts"`

	// RestartWindowSec 重启次数统计窗口，秒。待实测。
	RestartWindowSec int `json:"restart_window_sec"`

	// AuditLogMaxRows call_logs 保留条数。待实测。
	AuditLogMaxRows int `json:"audit_log_max_rows"`

	// AuditLogMaxDays call_logs 保留天数。待实测。
	AuditLogMaxDays int `json:"audit_log_max_days"`

	// JournaldMaxMB journald 总限额，MB。待实测。
	JournaldMaxMB int `json:"journald_max_mb"`

	// DiskWarnPercent 磁盘告警阈值百分比。待实测。
	DiskWarnPercent int `json:"disk_warn_percent"`

	// DiskForcePercent 强制清理阈值百分比。待实测。
	DiskForcePercent int `json:"disk_force_percent"`

	// StorageMaxMBPerApp 单应用对象存储上限，MB。待实测。
	StorageMaxMBPerApp int64 `json:"storage_max_mb_per_app"`

	// StorageMaxMBTotal 对象存储总上限，MB。待实测。
	StorageMaxMBTotal int64 `json:"storage_max_mb_total"`

	// StorageMaxFileMB 单文件大小上限，MB。待实测。
	StorageMaxFileMB int64 `json:"storage_max_file_mb"`
}

// Default 返回带保守默认值的配置（全部数字标注待实测）。
func Default() *Config {
	return &Config{
		ListenAddr:            ":8080",
		DataDir:               "data",
		PortStart:             30000,
		PortEnd:               30100,
		BusyTimeoutMS:         5000,
		SQLTimeoutMS:          3000,
		HealthTimeoutMS:       3000,
		MemoryLimitMB:         256,
		NPMInstallTimeoutMS:   180000,
		MaxUnpackedMB:         500,
		HealthRetryIntervalMS: 100,
		HealthProbeTimeoutMS:  1000,
		ProcessStopTimeoutMS:  5000,
		MaxUploadMB:           1024,
		MultipartMemoryMB:     32,
		ReadHeaderTimeoutMS:   10000,
		ShutdownTimeoutMS:     10000,
		LogTailBytes:          2048,
		CPUQuotaPercent:       50,
		MaxConcurrentApps:     20,
		MonitorIntervalMS:     10000,
		UnhealthyThreshold:    3,
		MaxRestarts:           5,
		RestartWindowSec:      300,
		AuditLogMaxRows:       1000,
		AuditLogMaxDays:       90,
		JournaldMaxMB:         200,
		DiskWarnPercent:       85,
		DiskForcePercent:      95,
		StorageMaxMBPerApp:    500,
		StorageMaxMBTotal:     2048,
		StorageMaxFileMB:      50,
		CgroupRoot:            "/sys/fs/cgroup/system.slice/minicore.service",
	}
}

// Load 从 JSON 文件加载配置，再用环境变量覆盖。文件不存在则用默认值。
func Load(path string) (*Config, error) {
	cfg := Default()
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("读取配置文件失败: %w", err)
		}
		if err == nil {
			if err := json.Unmarshal(b, cfg); err != nil {
				return nil, fmt.Errorf("解析配置文件失败: %w", err)
			}
		}
	}
	if v := os.Getenv("MINICLOUD_TOKEN"); v != "" {
		cfg.Token = v
	}
	if v := os.Getenv("MINICLOUD_DATA_DIR"); v != "" {
		cfg.DataDir = v
	}
	if v := os.Getenv("MINICLOUD_LISTEN"); v != "" {
		cfg.ListenAddr = v
	}
	if cfg.Token == "" {
		return nil, fmt.Errorf("管理员 Token 未配置：请写进配置文件或用环境变量 MINICLOUD_TOKEN 指定")
	}
	abs, err := filepath.Abs(cfg.DataDir)
	if err != nil {
		return nil, fmt.Errorf("数据目录路径无效: %w", err)
	}
	cfg.DataDir = abs
	return cfg, nil
}

// Validate 校验配置的基本合法性。
func (c *Config) Validate() error {
	if c.PortStart < 1 || c.PortEnd < c.PortStart {
		return fmt.Errorf("端口池范围无效: %d-%d", c.PortStart, c.PortEnd)
	}
	if c.BusyTimeoutMS <= 0 || c.HealthTimeoutMS <= 0 || c.SQLTimeoutMS <= 0 {
		return fmt.Errorf("超时配置必须为正数")
	}
	if c.MemoryLimitMB <= 0 {
		return fmt.Errorf("内存上限必须为正数")
	}
	if c.HealthRetryIntervalMS <= 0 || c.HealthProbeTimeoutMS <= 0 ||
		c.ProcessStopTimeoutMS <= 0 || c.ReadHeaderTimeoutMS <= 0 ||
		c.ShutdownTimeoutMS <= 0 || c.LogTailBytes <= 0 {
		return fmt.Errorf("超时和大小配置必须为正数")
	}
	if c.MaxUploadMB <= 0 || c.MultipartMemoryMB <= 0 {
		return fmt.Errorf("上传大小配置必须为正数")
	}
	if c.CPUQuotaPercent <= 0 || c.CPUQuotaPercent > 100 {
		return fmt.Errorf("CPU 配额必须在 1-100 之间")
	}
	if c.MaxConcurrentApps <= 0 || c.MonitorIntervalMS <= 0 || c.UnhealthyThreshold <= 0 ||
		c.MaxRestarts <= 0 || c.RestartWindowSec <= 0 {
		return fmt.Errorf("守护配置必须为正数")
	}
	if c.AuditLogMaxRows <= 0 || c.AuditLogMaxDays <= 0 || c.JournaldMaxMB <= 0 ||
		c.DiskWarnPercent <= 0 || c.DiskForcePercent <= c.DiskWarnPercent {
		return fmt.Errorf("日志与磁盘配置无效")
	}
	if c.StorageMaxMBPerApp <= 0 || c.StorageMaxMBTotal <= 0 || c.StorageMaxFileMB <= 0 {
		return fmt.Errorf("对象存储上限配置必须为正数")
	}
	return nil
}

// SystemDBPath 返回 system.db 的完整路径。
func (c *Config) SystemDBPath() string { return filepath.Join(c.DataDir, "system.db") }

// DatabasesDir 返回项目库目录。
func (c *Config) DatabasesDir() string { return filepath.Join(c.DataDir, "databases") }

// AppsDir 返回应用版本目录。
func (c *Config) AppsDir() string { return filepath.Join(c.DataDir, "apps") }
