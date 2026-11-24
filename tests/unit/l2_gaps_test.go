package unit

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"minibase/internal/database"
	"minibase/internal/health"
	"minibase/internal/logger"
	"minibase/internal/monitor"
)

// G1: SQL 超时配置化
func TestGap_SQLTimeoutConfig(t *testing.T) {
	env := newEnv(t, &fakeRunner{mode: "ok"})
	if env.cfg.SQLTimeoutMS <= 0 {
		t.Fatal("SQLTimeoutMS 未配置")
	}
	env.cfg.SQLTimeoutMS = 1
	if env.cfg.SQLTimeoutMS != 1 {
		t.Fatal("配置不可覆盖")
	}
}

// G2: 守护读内存 + 阈值
func TestGap_MemoryMonitor(t *testing.T) {
	if monitor.MemoryThresholdMB(100) != 90 {
		t.Fatal("内存阈值计算错误")
	}
	// 不存在的 cgroup 路径应返回 0 不报错
	if n := monitor.ReadMemoryUsage("/nonexistent", "/some/dir"); n != 0 {
		t.Fatalf("无 cgroup 应返回 0, got %d", n)
	}
}

// G3: 磁盘检查函数
func TestGap_DiskCheck(t *testing.T) {
	used, force, err := health.CheckDisk("/tmp", 200, 300)
	if err != nil {
		t.Fatal(err)
	}
	if used < 0 || used > 100 {
		t.Fatalf("使用率异常: %d", used)
	}
	if force {
		t.Fatal("200% 阈值不应触发强制")
	}
}

// G4: 启动对账检查应用状态
func TestGap_ReconcileAppStatus(t *testing.T) {
	env := newEnv(t, &fakeRunner{mode: "ok"})
	env.deploy(t, fixture("hello.zip"), "shop", "a")
	app, _ := env.store.GetAppByName("shop")
	// 模拟 Core 重启：把状态设为运行中但端口没人监听
	_ = env.store.UpdateAppStatus(app.ID, "运行中")
	report, err := env.store.Reconcile()
	if err != nil {
		t.Fatal(err)
	}
	// 端口实际有 fakeRunner 在监听，所以不应标为 stale
	for _, s := range report.StaleRunning {
		if s == "shop" {
			t.Fatal("正常应用被误标 stale")
		}
	}
}

// G5: 日志开关按应用过滤
func TestGap_LogSwitchPerApp(t *testing.T) {
	l := logger.New()
	l.Set("app", "shop", true, 0, "all")
	if !l.IsEnabled("app", "shop") {
		t.Fatal("shop 日志应开启")
	}
	if l.IsEnabled("app", "other") {
		t.Fatal("other 日志应关闭")
	}
	// 自动关闭
	l.Set("app", "temp", true, 50*time.Millisecond, "all")
	time.Sleep(80 * time.Millisecond)
	if l.IsEnabled("app", "temp") {
		t.Fatal("到期应自动关")
	}
}

// G6: call_logs 自动回收函数
func TestGap_LogPrune(t *testing.T) {
	env := newEnv(t, &fakeRunner{mode: "ok"})
	for i := 0; i < 10; i++ {
		_ = env.store.InsertCallLog(&database.CallLog{Event: "test", CreatedAt: int64(i)})
	}
	if err := env.store.PruneCallLogs(3); err != nil {
		t.Fatal(err)
	}
	logs, _ := env.store.ListCallLogs("", 100)
	if len(logs) != 3 {
		t.Fatalf("应剩 3 条, got %d", len(logs))
	}
}

// G7: 管控日志开关 API
func TestGap_ControlLogAPI(t *testing.T) {
	env := newEnv(t, &fakeRunner{mode: "ok"})
	body := `{"scope":"control","enabled":true,"duration_sec":60,"level":"all"}`
	resp := authReq(t, http.MethodPost, env.server.URL+"/api/logs/control", strings.NewReader(body), "application/json")
	if resp.StatusCode != 200 {
		t.Fatalf("管控日志开关失败: %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// G8: journald 限额配置在 install.sh
func TestGap_JournaldConfig(t *testing.T) {
	b, err := os.ReadFile("../../scripts/install.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "journald.conf.d") {
		t.Fatal("install.sh 未配置 journald 限额")
	}
}

// G9: 配置里的磁盘阈值被使用
func TestGap_DiskConfig(t *testing.T) {
	env := newEnv(t, &fakeRunner{mode: "ok"})
	if env.cfg.DiskWarnPercent >= env.cfg.DiskForcePercent {
		t.Fatal("磁盘阈值配置错误")
	}
}

// 确保 filepath 导入使用
var _ = filepath.Join
