package deploy

import (
	"encoding/json"
	"fmt"
	"time"

	"minibase/internal/database"
)

// Rollback 把应用切回上一个版本。不重新分配端口，不重新构建。
func (d *Deployer) Rollback(appName string) (*Result, error) {
	app, err := d.store.GetAppByName(appName)
	if err != nil {
		return nil, err
	}
	if app == nil {
		return nil, fmt.Errorf("应用不存在")
	}
	if app.PreviousVersion == "" {
		return nil, fmt.Errorf("没有可回滚的上一个版本")
	}

	lock := d.lockApp(app.RootPath)
	lock.Lock()
	defer lock.Unlock()

	targetVer, err := d.store.GetVersion(app.ID, app.PreviousVersion)
	if err != nil || targetVer == nil {
		return nil, fmt.Errorf("上一个版本记录不存在")
	}
	currentVer, _ := d.store.GetVersion(app.ID, app.CurrentVersion)

	// 停当前版本进程；目标版本进程一直保留着，直接复用
	if currentVer != nil && currentVer.Version != targetVer.Version {
		if h, ok := d.handles.Load(app.ID + "/" + currentVer.Version); ok {
			_ = h.(interface{ Stop() error }).Stop()
			d.forgetHandle(app.ID + "/" + currentVer.Version)
		}
	}

	handle, ok := d.handles.Load(app.ID + "/" + targetVer.Version)
	if !ok || handle == nil {
		// 目标进程不在了（比如守护重启过），用版本记录重新启动
		var env []string
		if targetVer.EnvJSON != "" {
			_ = json.Unmarshal([]byte(targetVer.EnvJSON), &env)
		}
		h, err := d.runner.Start(targetVer.SourceDir, targetVer.StartCmd, env, targetVer.Port)
		if err != nil {
			return nil, fmt.Errorf("回滚启动失败: %w", err)
		}
		handle = h
		d.rememberHandle(app.ID+"/"+targetVer.Version, h)
	}

	// 健康检查
	if !d.runner.HealthCheck(targetVer.Port, d.healthTimeout(app.ID)) {
		return nil, fmt.Errorf("回滚后健康检查失败，当前版本未变")
	}

	// 交换 current / previous
	_ = d.store.UpdateAppAfterDeploy(app.ID, targetVer.Version, app.CurrentVersion, targetVer.Port, "运行中")
	_ = d.store.InsertCallLog(&database.CallLog{
		AppID: app.ID, Event: "回滚",
		FromStatus: app.Status, ToStatus: "运行中", Reason: "回滚到 " + targetVer.Version,
		CreatedAt: time.Now().Unix(),
	})

	return &Result{
		AppName: app.Name, RootPath: app.RootPath,
		Version: targetVer.Version, Port: targetVer.Port, Status: "运行中",
	}, nil
}
