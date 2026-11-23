// minicore 是 MiniCloud 平台的唯一常驻进程。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"minicloud/internal/api"
	"minicloud/internal/config"
	"minicloud/internal/crypto"
	"minicloud/internal/database"
	"minicloud/internal/deploy"
	"minicloud/internal/logger"
	"minicloud/internal/monitor"
	"minicloud/internal/resource"
	"minicloud/internal/runtime"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	configPath := flag.String("config", "config.json", "配置文件路径（JSON）")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("配置加载失败: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		log.Fatalf("配置校验失败: %v", err)
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		log.Fatalf("建数据目录失败: %v", err)
	}

	store, err := database.Open(cfg.DataDir, cfg.BusyTimeoutMS)
	if err != nil {
		log.Fatalf("初始化系统库失败: %v", err)
	}
	defer store.Close()

	report, err := store.Reconcile()
	if err != nil {
		log.Fatalf("启动对账失败: %v", err)
	}
	log.Printf("[reconcile] 正常项目=%d 孤儿库文件=%v 坏引用=%v 状态异常=%v 启动残留=%v",
		len(report.Healthy), report.OrphanFiles, report.BrokenRefs, report.StaleRunning, report.StaleStarting)

	limiter := resource.NewLimiter(cfg.CgroupRoot, cfg.MemoryLimitMB)
	appLogger := logger.New()
	runner := &runtime.NodeRunner{
		LogApp:              func(app, line string) { appLogger.LogAppRaw(app, line) },
		HealthRetryInterval: time.Duration(cfg.HealthRetryIntervalMS) * time.Millisecond,
		HealthProbeTimeout:  time.Duration(cfg.HealthProbeTimeoutMS) * time.Millisecond,
		ProcessStopTimeout:  time.Duration(cfg.ProcessStopTimeoutMS) * time.Millisecond,
	}
	deployer := deploy.New(cfg, store, runner, limiter)
	cipher, err := crypto.New(cfg.MasterKey)
	if err != nil {
		log.Fatalf("初始化加密器失败: %v", err)
	}
	deployer.SetCipher(cipher)
	defer deployer.Shutdown()

	// 守护机制：重启回调复用部署器
	mon := monitor.New(cfg, store, func(app *database.App, ver *database.Version) error {
		var env []string
		if ver.EnvJSON != "" {
			_ = json.Unmarshal([]byte(ver.EnvJSON), &env)
		}
		handle, err := runner.Start(ver.SourceDir, ver.StartCmd, env, ver.Port)
		if err != nil {
			return err
		}
		deployer.AttachHandle(app.ID+"/"+ver.Version, handle)
		return nil
	})
	mon.Start()
	defer mon.Stop()

	// 审计账本自动回收：每小时按条数删最老的
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			if err := store.PruneCallLogs(cfg.AuditLogMaxRows); err != nil {
				log.Printf("[audit] 回收失败: %v", err)
			}
		}
	}()

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           api.New(store, deployer, cfg.Token, cfg).Handler(),
		ReadHeaderTimeout: time.Duration(cfg.ReadHeaderTimeoutMS) * time.Millisecond,
	}

	go func() {
		log.Printf("[minicore] 监听 %s", cfg.ListenAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP 服务异常退出: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	reload := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	signal.Notify(reload, syscall.SIGUSR2)
	select {
	case <-reload:
		log.Printf("[minicore] 收到热重载信号，保存运行时状态后 exec 新二进制")
		// exec 前再保存一次。普通句柄变更已经即时落盘，这里是最后一道保险。
		if err := deployer.SaveRuntimeState(); err != nil {
			log.Printf("[minicore] 热重载前保存运行时状态失败: %v", err)
		}
		// exec 替换当前进程，PID 不变，systemd 不重启，应用进程不受影响
		if err := syscall.Exec("/opt/minicloud/bin/minicore", os.Args, os.Environ()); err != nil {
			log.Fatalf("热重载失败: %v", err)
		}
	case <-stop:
		log.Printf("[minicore] 收到退出信号，开始优雅关闭")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.ShutdownTimeoutMS)*time.Millisecond)
	defer cancel()
	_ = srv.Shutdown(ctx)
}
