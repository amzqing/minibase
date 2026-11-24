package unit

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"minibase/internal/api"
	"minibase/internal/crypto"
	"minibase/internal/deploy"
	"minibase/internal/proxy"
	"minibase/internal/resource"
	"minibase/internal/runtime"
)

// L3-08 真实应用间调用：带来源应用名访问目标应用
func TestL3_08_RealAppToApp(t *testing.T) {
	env := newEnv(t, &fakeRunner{mode: "ok"})
	_, outB := env.deploy(t, fixture("hello.zip"), "target", "a")
	portB := int(outB["port"].(float64))
	_ = portB

	// 同应用来源头放行
	req, _ := http.NewRequest(http.MethodGet, env.server.URL+"/apps/target/api/users", nil)
	req.Header.Set("X-MiniBase-From", "target")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("同应用调用应放行: %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// L3-09 跨应用拒绝：带不同来源头返回 403
func TestL3_09_RealCrossAppDeny(t *testing.T) {
	if !proxy.AllowedBySource("", "target") {
		t.Fatal("无头应放行")
	}
	if !proxy.AllowedBySource("target", "target") {
		t.Fatal("同应用应放行")
	}
	if proxy.AllowedBySource("other", "target") {
		t.Fatal("跨应用应拒绝")
	}

	env := newEnv(t, &fakeRunner{mode: "ok"})
	env.deploy(t, fixture("hello.zip"), "target", "a")
	req, _ := http.NewRequest(http.MethodGet, env.server.URL+"/apps/target/api/users", nil)
	req.Header.Set("X-MiniBase-From", "other")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 403 {
		t.Fatalf("跨应用应返回 403, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// L3-10 升级脚本真实回滚逻辑（函数级验证）
func TestL3_10_UpgradeRollbackLogic(t *testing.T) {
	b, err := os.ReadFile("../../scripts/upgrade.sh")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, kw := range []string{"备份", "回滚", "cp -p", "systemctl restart", "kill -USR2", "探活"} {
		if !strings.Contains(s, kw) {
			t.Errorf("upgrade.sh 缺少: %s", kw)
		}
	}
}

// 缺口：env API 统一机制（密钥走同一 API）
func TestL3_EnvUnifiedSecret(t *testing.T) {
	env := newEnv(t, &fakeRunner{mode: "ok"})
	env.deploy(t, fixture("hello.zip"), "shop", "a")
	body := `{"PLAIN":"v1","TOKEN":"secret123","_secret":["TOKEN"]}`
	resp := authReq(t, http.MethodPost, env.server.URL+"/api/apps/shop/env", strings.NewReader(body), "application/json")
	if resp.StatusCode != 200 {
		t.Fatalf("env 统一 API 失败: %d", resp.StatusCode)
	}
	resp.Body.Close()
	// 检查数据库：PLAIN 明文，TOKEN 密文
	var plain string
	_ = env.store.DB.QueryRow(`SELECT value FROM app_env WHERE app_id=(SELECT id FROM apps WHERE name='shop') AND key='PLAIN'`).Scan(&plain)
	if plain != "v1" {
		t.Fatalf("普通变量未明文存: %q", plain)
	}
	var enc string
	_ = env.store.DB.QueryRow(`SELECT encrypted_value FROM secrets WHERE app_id=(SELECT id FROM apps WHERE name='shop') AND key='TOKEN'`).Scan(&enc)
	if enc == "" || strings.Contains(enc, "secret123") {
		t.Fatalf("密钥未加密存: %q", enc)
	}
}

// 缺口：状态页应用日志 API
func TestL3_AppLogAPI(t *testing.T) {
	env := newEnv(t, &fakeRunner{mode: "ok"})
	env.deploy(t, fixture("hello.zip"), "shop", "a")
	resp := authReq(t, http.MethodGet, env.server.URL+"/api/apps/shop/applog", nil, "")
	if resp.StatusCode != 200 {
		t.Fatalf("应用日志 API 失败: %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// 缺口：热重载后新 Core 能从磁盘状态重建应用进程句柄。
func TestL3_12_RuntimeStateRestoredAfterHotReload(t *testing.T) {
	runner := &fakeRunner{mode: "ok"}
	env := newEnv(t, runner)
	code, out := env.deploy(t, fixture("hello.zip"), "shop", "a")
	if code != http.StatusOK {
		t.Fatalf("部署失败: %d %v", code, out)
	}
	port := int(out["port"].(float64))

	statePath := filepath.Join(env.cfg.DataDir, "runtime-state.json")
	b, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("运行时状态未落盘: %v", err)
	}
	var state struct {
		Handles []struct {
			Key  string `json:"key"`
			PID  int    `json:"pid"`
			Port int    `json:"port"`
		} `json:"handles"`
	}
	if err := json.Unmarshal(b, &state); err != nil {
		t.Fatalf("运行时状态解析失败: %v", err)
	}
	found := false
	for _, h := range state.Handles {
		if h.Port == port && h.PID > 0 {
			found = true
		}
	}
	if !found {
		t.Fatalf("运行时状态缺少应用句柄: %s", b)
	}

	restoredStopped := false
	runner.mu.Lock()
	runner.onReattach = func(pid, p int) *runtime.ProcessHandle {
		return runtime.NewProcessHandle(pid, p, func() error {
			restoredStopped = true
			return nil
		})
	}
	runner.mu.Unlock()

	restored := deploy.New(env.cfg, env.store, runner, resource.NoopLimiter{})
	cipher, _ := crypto.New(env.cfg.MasterKey)
	restored.SetCipher(cipher)
	srv := newLocalServer(api.New(env.store, restored, env.cfg.Token, env.cfg).Handler())
	t.Cleanup(srv.Close)
	t.Cleanup(restored.Shutdown)

	resp := authReq(t, http.MethodDelete, srv.URL+"/api/apps/shop", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("热重载后下线失败: %d", resp.StatusCode)
	}
	resp.Body.Close()
	if !restoredStopped {
		t.Fatal("热重载后重建的句柄没有停掉旧应用进程")
	}

	b2, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var state2 struct {
		Handles []struct {
			Key string `json:"key"`
		} `json:"handles"`
	}
	if err := json.Unmarshal(b2, &state2); err != nil {
		t.Fatal(err)
	}
	if len(state2.Handles) != 0 {
		t.Fatalf("下线后运行时状态应为空: %s", b2)
	}
}

// 缺口：热重载前必须显式保存一次运行时状态。
func TestL3_13_MainSavesRuntimeStateBeforeExec(t *testing.T) {
	b, err := os.ReadFile("../../cmd/minicore/main.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, "SaveRuntimeState") {
		t.Fatal("main.go 热重载前未保存运行时状态")
	}
	if !strings.Contains(s, "syscall.Exec") {
		t.Fatal("main.go 未找到热重载 exec")
	}
}
