// Package deploy 实现版本一最关键的部署链路：
// 上传 zip → 校验 → 存版本目录 → 构建 → 分配端口 → 启动 → 健康检查 → 切流量。
// 任何一步失败，正在服务的版本都不受影响。
package deploy

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"minibase/internal/config"
	"minibase/internal/crypto"
	"minibase/internal/database"
	"minibase/internal/health"
	"minibase/internal/resource"
	"minibase/internal/runtime"
)

const runtimeStateFile = "runtime-state.json"

// runtimeHandleRecord 是落盘的一个应用进程句柄。
// 热重载后新 Core 靠它重新认识还在运行的应用。
type runtimeHandleRecord struct {
	Key  string `json:"key"`
	PID  int    `json:"pid"`
	Port int    `json:"port"`
}

type runtimeState struct {
	Handles   []runtimeHandleRecord `json:"handles"`
	UpdatedAt int64                 `json:"updated_at"`
}

// handleReattacher 让 Runner 能按 PID 重建句柄。
// 真机用 NodeRunner，逻辑测试用 fakeRunner。
type handleReattacher interface {
	Reattach(pid, port int) *runtime.ProcessHandle
}

// Deployer 编排部署流程。
type Deployer struct {
	cfg     *config.Config
	store   *database.Store
	runner  runtime.AppRunner
	limiter resource.Limiter
	cipher  *crypto.Cipher

	portMu    sync.Mutex
	allocated map[int]bool

	appLocks sync.Map // appID -> *sync.Mutex，保证同一应用串行部署
	handles  sync.Map // "appID/version" -> *runtime.ProcessHandle

	stateMu   sync.Mutex
	statePath string
}

// New 创建部署器。
func New(cfg *config.Config, store *database.Store, runner runtime.AppRunner, limiter resource.Limiter) *Deployer {
	d := &Deployer{
		cfg: cfg, store: store, runner: runner, limiter: limiter,
		allocated: map[int]bool{},
		statePath: filepath.Join(cfg.DataDir, runtimeStateFile),
	}
	d.loadAllocatedPorts()
	d.loadRuntimeState()
	return d
}

func (d *Deployer) loadAllocatedPorts() {
	rows, err := d.store.DB.Query(`SELECT DISTINCT port FROM versions WHERE port > 0`)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var p int
		if rows.Scan(&p) == nil {
			d.allocated[p] = true
		}
	}
}

// loadRuntimeState 在 Core 启动时读取应用进程清单。
// 清单里的进程还在，就重建句柄；进程已死，就丢掉这条记录。
func (d *Deployer) loadRuntimeState() {
	if d.statePath == "" {
		return
	}
	b, err := os.ReadFile(d.statePath)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("[runtime-state] 读取失败: %v", err)
		}
		return
	}
	var st runtimeState
	if err := json.Unmarshal(b, &st); err != nil {
		log.Printf("[runtime-state] 解析失败，忽略旧状态: %v", err)
		return
	}

	reattacher, _ := d.runner.(handleReattacher)
	changed := false
	for _, rec := range st.Handles {
		if rec.Key == "" || rec.PID <= 0 || rec.Port <= 0 {
			changed = true
			continue
		}
		var h *runtime.ProcessHandle
		if reattacher != nil {
			h = reattacher.Reattach(rec.PID, rec.Port)
		} else {
			h = runtime.NewProcessHandle(rec.PID, rec.Port, nil)
		}
		if h == nil {
			changed = true
			continue
		}
		d.handles.Store(rec.Key, h)
	}
	if changed {
		d.saveRuntimeStateQuiet()
	}
}

// SaveRuntimeState 把当前应用进程清单写到磁盘。
// 热重载前会再调用一次，确保新 Core 能看到最新清单。
func (d *Deployer) SaveRuntimeState() error {
	d.stateMu.Lock()
	defer d.stateMu.Unlock()
	return d.writeRuntimeStateLocked(d.runtimeStateRecords())
}

func (d *Deployer) saveRuntimeStateQuiet() {
	if err := d.SaveRuntimeState(); err != nil {
		log.Printf("[runtime-state] 保存失败: %v", err)
	}
}

func (d *Deployer) runtimeStateRecords() []runtimeHandleRecord {
	records := make([]runtimeHandleRecord, 0)
	d.handles.Range(func(key, value any) bool {
		k, ok := key.(string)
		if !ok {
			return true
		}
		h, ok := value.(*runtime.ProcessHandle)
		if !ok || h == nil {
			return true
		}
		records = append(records, runtimeHandleRecord{Key: k, PID: h.PID, Port: h.Port})
		return true
	})
	return records
}

func (d *Deployer) writeRuntimeStateLocked(records []runtimeHandleRecord) error {
	if d.statePath == "" {
		return fmt.Errorf("未设置运行时状态路径")
	}
	if err := os.MkdirAll(filepath.Dir(d.statePath), 0o755); err != nil {
		return fmt.Errorf("建运行时状态目录失败: %w", err)
	}
	b, err := json.MarshalIndent(runtimeState{Handles: records, UpdatedAt: time.Now().Unix()}, "", "  ")
	if err != nil {
		return fmt.Errorf("编码运行时状态失败: %w", err)
	}
	tmp := d.statePath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("写运行时状态失败: %w", err)
	}
	if err := os.Rename(tmp, d.statePath); err != nil {
		return fmt.Errorf("替换运行时状态失败: %w", err)
	}
	return nil
}

// rememberHandle 登记句柄并立即落盘。
func (d *Deployer) rememberHandle(key string, h *runtime.ProcessHandle) {
	if h == nil {
		return
	}
	d.handles.Store(key, h)
	d.saveRuntimeStateQuiet()
}

// forgetHandle 删除句柄并立即落盘。
func (d *Deployer) forgetHandle(key string) {
	d.handles.Delete(key)
	d.saveRuntimeStateQuiet()
}

// Request 是一次部署的输入。
type Request struct {
	ZipPath string
	AppName string
	Mode    string
}

// Result 部署成功后的结果。
type Result struct {
	AppName  string `json:"app_name"`
	RootPath string `json:"root_path"`
	Version  string `json:"version"`
	Port     int    `json:"port"`
	Status   string `json:"status"`
}

func randomID(prefix string) string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

func (d *Deployer) lockApp(appID string) *sync.Mutex {
	v, _ := d.appLocks.LoadOrStore(appID, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// Deploy 执行一次部署。返回的结果里不含完整访问地址，由 API 层根据请求 Host 拼接。
func (d *Deployer) Deploy(ctx context.Context, req Request) (*Result, error) {
	// 磁盘满告警：达到强制阈值直接拒绝部署
	if _, force, err := health.CheckDisk(d.cfg.DataDir, d.cfg.DiskWarnPercent, d.cfg.DiskForcePercent); err == nil && force {
		return nil, fmt.Errorf("磁盘空间不足（使用率已达 %d%%），拒绝部署", d.cfg.DiskForcePercent)
	}

	// 并发上限（任务 G）：检查当前运行应用数
	var running int
	if err := d.store.DB.QueryRow(`SELECT COUNT(*) FROM apps WHERE status='运行中'`).Scan(&running); err == nil {
		if running >= d.cfg.MaxConcurrentApps {
			return nil, fmt.Errorf("资源忙：当前运行应用数 %d 已达上限 %d", running, d.cfg.MaxConcurrentApps)
		}
	}

	if req.Mode != "a" && req.Mode != "b" {
		return nil, fmt.Errorf("mode 只能是 a 或 b")
	}
	if req.ZipPath == "" {
		return nil, fmt.Errorf("缺少 zip 文件路径")
	}

	// 1. 校验包内容（上传阶段失败：不进入构建）。
	pkg, base, err := runtime.InspectZip(req.ZipPath, req.Mode)
	if err != nil {
		return nil, err
	}
	startCmd, err := pkg.StartCommand()
	if err != nil {
		return nil, err
	}

	appName := req.AppName
	if appName == "" {
		appName = pkg.Name
	}
	if appName == "" {
		appName = "app"
	}
	if !validName(appName) {
		return nil, fmt.Errorf("应用名只能包含字母数字、短横线和下划线")
	}
	rootPath := fmt.Sprintf("/apps/%s", appName)

	// 2. 同一应用串行部署。
	// 应用锁按 rootPath 取，此时还没创建 app 记录，用 rootPath 作为逻辑 key。
	lock := d.lockApp(rootPath)
	lock.Lock()
	defer lock.Unlock()

	app, err := d.store.GetAppByName(appName)
	if err != nil {
		return nil, fmt.Errorf("查询应用失败: %w", err)
	}
	oldStatus := "失败"
	if app != nil && app.CurrentVersion != "" {
		oldStatus = app.Status
	}
	if app == nil {
		app = &database.App{
			ID: randomID("app_"), Name: appName,
			RootPath: rootPath, Status: "构建中", CreatedAt: time.Now().Unix(),
		}
		if err := d.store.CreateApp(app); err != nil {
			return nil, fmt.Errorf("创建应用记录失败: %w", err)
		}
		if _, err := d.store.EnsureDefaultDatabase(appName); err != nil {
			_, _ = d.store.DB.Exec(`DELETE FROM apps WHERE id=?`, app.ID)
			return nil, fmt.Errorf("创建应用默认库失败: %w", err)
		}
	}

	versionNum, err := d.store.NextVersionNumber(app.ID)
	if err != nil {
		return nil, fmt.Errorf("分配版本号失败: %w", err)
	}
	versionStr := fmt.Sprintf("v%d", versionNum)
	appDir := filepath.Join(d.cfg.AppsDir(), appName)
	versionDir := filepath.Join(appDir, "versions", versionStr)
	// 新版本目录必须干净。
	if _, err := os.Stat(versionDir); err == nil {
		return nil, fmt.Errorf("版本目录已存在: %s", versionDir)
	}

	// 4. 解压到版本目录。失败只清理新目录。
	if err := runtime.ExtractZip(req.ZipPath, base, versionDir, d.cfg.MaxUnpackedMB*1024*1024); err != nil {
		_ = os.RemoveAll(versionDir)
		_ = d.store.UpdateAppStatus(app.ID, oldStatus)
		return nil, fmt.Errorf("解压失败: %w", err)
	}
	cleanup := func() {
		_ = os.RemoveAll(versionDir)
		_ = d.store.UpdateAppStatus(app.ID, oldStatus)
	}

	// 5. 模式 B 构建：npm install。
	if req.Mode == "b" {
		if err := d.npmInstall(ctx, versionDir); err != nil {
			cleanup()
			return nil, fmt.Errorf("npm install 失败: %w", err)
		}
	}

	// 6. 分配端口。
	port, err := d.allocatePort()
	if err != nil {
		cleanup()
		return nil, err
	}
	portOK := false
	defer func() {
		if !portOK {
			d.releasePort(port)
		}
	}()

	// 7. 启动应用（临时端口），传 PORT 和 MOUNT_PATH。
	_ = d.store.UpdateAppStatus(app.ID, "启动中")
	env := []string{
		fmt.Sprintf("PORT=%d", port),
		"MOUNT_PATH=" + rootPath,
		"MINIBASE_DB_PATH=" + d.store.DatabasePath(appName, "main"),
	}
	dbs, _ := d.store.ListDatabases(appName)
	dbJSON, _ := json.Marshal(dbs)
	env = append(env, "MINIBASE_DATABASES="+string(dbJSON))
	// 注入密钥（任务 E）：解密后作为环境变量传给应用
	if d.cipher != nil {
		secrets, _ := d.store.GetSecrets(app.ID, "")
		for _, sec := range secrets {
			plain, err := d.cipher.Decrypt(sec.EncryptedValue)
			if err == nil {
				env = append(env, sec.Key+"="+plain)
			}
		}
		appEnvs, _ := d.store.GetAppEnv(app.ID, "")
		for _, e := range appEnvs {
			env = append(env, e.Key+"="+e.Value)
		}
	}
	handle, err := d.runner.Start(versionDir, startCmd, env, port)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("启动应用失败: %w", err)
	}
	started := true
	defer func() {
		if !started {
			_ = handle.Stop()
		}
	}()

	// 8. 资源限制（best effort：失败只告警，不阻断部署）。
	d.applyLimits(handle.PID, versionDir, app.ID)

	// 9. 健康检查：不通过就删新版本、停新进程，老版本不动。
	if !d.runner.HealthCheck(port, d.healthTimeout(app.ID)) {
		started = false
		detail := readLogTail(filepath.Join(versionDir, "app.log"), d.cfg.LogTailBytes)
		cleanup()
		if detail != "" {
			return nil, fmt.Errorf("健康检查失败：应用在端口 %d 上没有响应，老版本未受影响。应用日志尾部: %s", port, detail)
		}
		return nil, fmt.Errorf("健康检查失败：应用在端口 %d 上没有响应，老版本未受影响", port)
	}

	// 10. 切流量：写版本记录、更新应用。
	envJSON, _ := json.Marshal(env)
	ver := &database.Version{
		ID: randomID("ver_"), AppID: app.ID, Version: versionStr,
		SourceDir: versionDir, Mode: req.Mode, StartCmd: startCmd,
		EnvJSON: string(envJSON), Port: port, CreatedAt: time.Now().Unix(),
	}
	if err := d.store.CreateVersion(ver); err != nil {
		started = false
		cleanup()
		return nil, fmt.Errorf("写版本记录失败: %w", err)
	}
	previous := app.CurrentVersion
	if err := d.store.UpdateAppAfterDeploy(app.ID, versionStr, previous, port, "运行中"); err != nil {
		started = false
		cleanup()
		return nil, fmt.Errorf("切换流量失败: %w", err)
	}
	d.rememberHandle(app.ID+"/"+versionStr, handle)
	portOK = true

	// 11. 保留当前和上一个版本，更早的版本停进程、删目录、删记录。
	d.pruneOldVersions(app.ID, appDir)

	// 审计账本：部署成功写一条
	_ = d.store.InsertCallLog(&database.CallLog{
		AppID: app.ID, Event: "部署成功",
		FromStatus: "启动中", ToStatus: "运行中", Reason: "版本 " + versionStr,
		CreatedAt: time.Now().Unix(),
	})

	log.Printf("[deploy] %s 部署成功: 版本=%s 端口=%d", rootPath, versionStr, port)
	return &Result{
		AppName: appName, RootPath: rootPath,
		Version: versionStr, Port: port, Status: "运行中",
	}, nil
}

func (d *Deployer) npmInstall(ctx context.Context, dir string) error {
	timeout := time.Duration(d.cfg.NPMInstallTimeoutMS) * time.Millisecond
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	logPath := filepath.Join(dir, "build.log")
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer logf.Close()
	cmd := exec.CommandContext(cctx, "npm", "install", "--no-audit", "--no-fund")
	cmd.Dir = dir
	cmd.Stdout = logf
	cmd.Stderr = logf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w（详见 %s）", err, logPath)
	}
	return nil
}

func (d *Deployer) allocatePort() (int, error) {
	d.portMu.Lock()
	defer d.portMu.Unlock()
	for p := d.cfg.PortStart; p <= d.cfg.PortEnd; p++ {
		if d.allocated[p] || runtime.PortInUse(p) {
			continue
		}
		d.allocated[p] = true
		return p, nil
	}
	return 0, fmt.Errorf("端口池 %d-%d 已无空闲端口", d.cfg.PortStart, d.cfg.PortEnd)
}

func (d *Deployer) releasePort(port int) {
	d.portMu.Lock()
	delete(d.allocated, port)
	d.portMu.Unlock()
}

func (d *Deployer) pruneOldVersions(appID, appDir string) {
	versions, err := d.store.ListVersions(appID)
	if err != nil || len(versions) <= 2 {
		return
	}
	keep := map[string]bool{}
	for _, v := range versions[len(versions)-2:] {
		keep[v.Version] = true
	}
	for _, v := range versions[:len(versions)-2] {
		if h, ok := d.handles.Load(appID + "/" + v.Version); ok {
			_ = h.(*runtime.ProcessHandle).Stop()
			d.forgetHandle(appID + "/" + v.Version)
			d.releasePort(v.Port)
		}
		_ = os.RemoveAll(v.SourceDir)
		_ = d.limiter.Remove(v.SourceDir)
	}
}

func validName(s string) bool {
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return s != ""
}

// Shutdown 停止本进程启动过的全部应用。Core 优雅退出时调用。
func (d *Deployer) Shutdown() {
	d.handles.Range(func(key, value any) bool {
		_ = value.(*runtime.ProcessHandle).Stop()
		d.handles.Delete(key)
		return true
	})
	d.saveRuntimeStateQuiet()
}

// HealthHandler 给外部探活 Core 自己用，不需要 Token。
func HealthHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// readLogTail 读日志尾部，用于失败时给出清楚的错误原因。
func readLogTail(path string, max int) string {
	b, err := os.ReadFile(path)
	if err != nil || len(b) == 0 {
		return ""
	}
	if len(b) > max {
		b = b[len(b)-max:]
	}
	return strings.TrimSpace(string(b))
}

// AttachHandle 把守护重启的应用进程句柄登记到部署器。
func (d *Deployer) AttachHandle(key string, h *runtime.ProcessHandle) {
	d.rememberHandle(key, h)
}

// SetCipher 设置加密器，用于启动时注入应用密钥。
func (d *Deployer) SetCipher(c *crypto.Cipher) { d.cipher = c }

// effectiveConfig 返回应用实际生效的配置，覆盖值优先于全局值。
func (d *Deployer) EffectiveConfig(appID string) (memoryMB, cpuPercent, healthTimeoutMS int, startCmd string) {
	memoryMB, cpuPercent, healthTimeoutMS = d.cfg.MemoryLimitMB, d.cfg.CPUQuotaPercent, d.cfg.HealthTimeoutMS
	oc, err := d.store.GetAppConfig(appID)
	if err != nil || oc == nil {
		return
	}
	if oc.MemoryLimitMB != nil {
		memoryMB = *oc.MemoryLimitMB
	}
	if oc.CPUQuotaPercent != nil {
		cpuPercent = *oc.CPUQuotaPercent
	}
	if oc.HealthTimeoutMS != nil {
		healthTimeoutMS = *oc.HealthTimeoutMS
	}
	if oc.StartCmd != nil {
		startCmd = *oc.StartCmd
	}
	return
}

// applyLimits 按生效配置给进程设 cgroup 限制。
func (d *Deployer) applyLimits(pid int, key, appID string) {
	mem, cpu, _, _ := d.EffectiveConfig(appID)
	if err := d.limiter.Apply(pid, key, mem, cpu); err != nil {
		log.Printf("[deploy] 应用内存限制未生效: %v", err)
	}
}

// healthTimeout 取应用生效的健康检查超时。
func (d *Deployer) healthTimeout(appID string) time.Duration {
	_, _, ms, _ := d.EffectiveConfig(appID)
	if ms <= 0 {
		ms = d.cfg.HealthTimeoutMS
	}
	return time.Duration(ms) * time.Millisecond
}

// Pause 暂停应用：停进程、回收端口，保留版本目录和全部数据。
func (d *Deployer) Pause(appName string) error {
	app, err := d.store.GetAppByName(appName)
	if err != nil {
		return err
	}
	if app == nil {
		return fmt.Errorf("应用不存在")
	}
	lock := d.lockApp(app.RootPath)
	lock.Lock()
	defer lock.Unlock()

	versions, _ := d.store.ListVersions(app.ID)
	for _, v := range versions {
		if h, ok := d.handles.Load(app.ID + "/" + v.Version); ok {
			_ = h.(*runtime.ProcessHandle).Stop()
			d.forgetHandle(app.ID + "/" + v.Version)
		}
		d.releasePort(v.Port)
		_ = d.limiter.Remove(v.SourceDir)
	}
	if err := d.store.UpdateAppStatus(app.ID, "已暂停"); err != nil {
		return err
	}
	_ = d.store.InsertCallLog(&database.CallLog{
		AppID: app.ID, Event: "app_paused",
		FromStatus: app.Status, ToStatus: "已暂停", Reason: "用户暂停应用",
		CreatedAt: time.Now().Unix(),
	})
	return nil
}

// Resume 从当前版本恢复一个已暂停的应用。
func (d *Deployer) Resume(appName string) (*Result, error) {
	app, err := d.store.GetAppByName(appName)
	if err != nil {
		return nil, err
	}
	if app == nil {
		return nil, fmt.Errorf("应用不存在")
	}
	if app.CurrentVersion == "" {
		return nil, fmt.Errorf("应用没有可启动的版本")
	}
	lock := d.lockApp(app.RootPath)
	lock.Lock()
	defer lock.Unlock()

	ver, err := d.store.GetVersion(app.ID, app.CurrentVersion)
	if err != nil || ver == nil {
		return nil, fmt.Errorf("版本记录不存在")
	}
	if runtime.PortInUse(ver.Port) {
		return nil, fmt.Errorf("端口 %d 已被占用", ver.Port)
	}

	if err := d.store.UpdateAppStatus(app.ID, "启动中"); err != nil {
		return nil, err
	}
	env := []string{}
	if ver.EnvJSON != "" {
		_ = json.Unmarshal([]byte(ver.EnvJSON), &env)
	}
	// 覆盖注入应用库路径，确保恢复后路径正确
	env = append(env, "MINIBASE_DB_PATH="+d.store.DatabasePath(appName, "main"))
	dbs, _ := d.store.ListDatabases(appName)
	dbJSON, _ := json.Marshal(dbs)
	env = append(env, "MINIBASE_DATABASES="+string(dbJSON))
	startCmd := ver.StartCmd
	_, _, _, ocStart := d.EffectiveConfig(app.ID)
	if ocStart != "" {
		startCmd = ocStart
	}

	handle, err := d.runner.Start(ver.SourceDir, startCmd, env, ver.Port)
	if err != nil {
		_ = d.store.UpdateAppStatus(app.ID, "失败")
		return nil, fmt.Errorf("启动失败: %w", err)
	}
	mem, cpu, _, _ := d.EffectiveConfig(app.ID)
	if err := d.limiter.Apply(handle.PID, ver.SourceDir, mem, cpu); err != nil {
		log.Printf("[deploy] 应用内存限制未生效: %v", err)
	}
	if !d.runner.HealthCheck(ver.Port, d.healthTimeout(app.ID)) {
		_ = handle.Stop()
		_ = d.store.UpdateAppStatus(app.ID, "失败")
		return nil, fmt.Errorf("健康检查失败，应用停在端口 %d", ver.Port)
	}
	d.rememberHandle(app.ID+"/"+ver.Version, handle)
	d.allocated[ver.Port] = true
	if err := d.store.UpdateAppStatus(app.ID, "运行中"); err != nil {
		return nil, err
	}
	_ = d.store.InsertCallLog(&database.CallLog{
		AppID: app.ID, Event: "app_resumed",
		FromStatus: "已暂停", ToStatus: "运行中", Reason: "用户恢复应用",
		CreatedAt: time.Now().Unix(),
	})
	return &Result{AppName: app.Name, RootPath: app.RootPath,
		Version: ver.Version, Port: ver.Port, Status: "运行中"}, nil
}

// Restart 重启应用。
func (d *Deployer) Restart(appName string) (*Result, error) {
	if err := d.Pause(appName); err != nil {
		return nil, err
	}
	res, err := d.Resume(appName)
	if err != nil {
		return nil, err
	}
	app, _ := d.store.GetAppByName(appName)
	if app != nil {
		_ = d.store.InsertCallLog(&database.CallLog{
			AppID: app.ID, Event: "app_restarted",
			FromStatus: "运行中", ToStatus: "运行中", Reason: "用户重启应用",
			CreatedAt: time.Now().Unix(),
		})
	}
	return res, nil
}

// Destroy 彻底删除应用：停进程、删记录、释放端口、删版本目录。
func (d *Deployer) Destroy(appName string) error {
	app, err := d.store.GetAppByName(appName)
	if err != nil {
		return err
	}
	if app == nil {
		return fmt.Errorf("应用不存在")
	}
	lock := d.lockApp(app.RootPath)
	lock.Lock()
	defer lock.Unlock()

	versions, _ := d.store.ListVersions(app.ID)
	for _, v := range versions {
		if h, ok := d.handles.Load(app.ID + "/" + v.Version); ok {
			_ = h.(interface{ Stop() error }).Stop()
			d.forgetHandle(app.ID + "/" + v.Version)
		}
		d.releasePort(v.Port)
		_ = d.limiter.Remove(v.SourceDir)
		_ = os.RemoveAll(v.SourceDir)
	}
	if err := d.store.DeleteAppCascade(appName); err != nil {
		return err
	}
	return nil
}
