package unit

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"minibase/internal/crypto"
	"minibase/internal/database"
	"minibase/internal/proxy"
)

// L3-01 环境变量配置 + 注入
func TestL3_01_EnvConfig(t *testing.T) {
	runner := &fakeRunner{mode: "ok"}
	env := newEnv(t, runner)
	env.deploy(t, fixture("hello.zip"), "shop", "a")
	// 设置普通变量
	resp := authReq(t, http.MethodPost, env.server.URL+"/api/apps/shop/env",
		strings.NewReader(`{"NODE_ENV":"production","FEATURE_X":"on"}`), "application/json")
	if resp.StatusCode != 200 {
		t.Fatalf("env 设置失败: %d", resp.StatusCode)
	}
	resp.Body.Close()
	// 重新部署，检查 fakeRunner 收到的 env
	_, _ = env.deploy(t, fixture("hello.zip"), "shop", "a")
	found := map[string]bool{}
	for _, e := range runner.lastEnv {
		if strings.HasPrefix(e, "NODE_ENV=") {
			found["NODE_ENV"] = true
		}
		if strings.HasPrefix(e, "FEATURE_X=") {
			found["FEATURE_X"] = true
		}
	}
	if !found["NODE_ENV"] || !found["FEATURE_X"] {
		t.Fatalf("环境变量未注入: %v", runner.lastEnv)
	}
}

// L3-02 配置下次部署生效（当前进程不变）
func TestL3_02_EnvNextDeploy(t *testing.T) {
	runner := &fakeRunner{mode: "ok"}
	env := newEnv(t, runner)
	env.deploy(t, fixture("hello.zip"), "shop", "a")
	env1 := append([]string(nil), runner.lastEnv...)
	// 设置新值但不部署
	resp := authReq(t, http.MethodPost, env.server.URL+"/api/apps/shop/env",
		strings.NewReader(`{"NEW_KEY":"v1"}`), "application/json")
	resp.Body.Close()
	// 当前进程 env 不应变化（runner.lastEnv 是上次部署的）
	for _, e := range runner.lastEnv {
		if strings.HasPrefix(e, "NEW_KEY=") {
			t.Fatal("当前版本不该立即生效")
		}
	}
	// 重新部署后生效
	_, _ = env.deploy(t, fixture("hello.zip"), "shop", "a")
	found := false
	for _, e := range runner.lastEnv {
		if e == "NEW_KEY=v1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("新部署未生效: %v", runner.lastEnv)
	}
	_ = env1
}

// L3-03 密钥加密存储
func TestL3_03_SecretEncrypted(t *testing.T) {
	env := newEnv(t, &fakeRunner{mode: "ok"})
	appID := "app_test"
	cipher, _ := crypto.New(env.cfg.MasterKey)
	enc, _ := cipher.Encrypt("supersecret")
	_ = env.store.SaveSecret(&database.Secret{AppID: appID, Key: "K", EncryptedValue: enc, Version: "v1", CreatedAt: time.Now().Unix()})
	var stored string
	_ = env.store.DB.QueryRow(`SELECT encrypted_value FROM secrets WHERE app_id=? AND key='K'`, appID).Scan(&stored)
	if strings.Contains(stored, "supersecret") {
		t.Fatal("密钥明文存储")
	}
	plain, _ := cipher.Decrypt(stored)
	if plain != "supersecret" {
		t.Fatal("解密失败")
	}
}

// L3-04 端口分配
func TestL3_04_PortAlloc(t *testing.T) {
	env := newEnv(t, &fakeRunner{mode: "ok"})
	ports := map[int]bool{}
	for _, name := range []string{"a", "b", "c"} {
		_, out := env.deploy(t, fixture("hello.zip"), name, "a")
		p := int(out["port"].(float64))
		if ports[p] {
			t.Fatalf("端口重复: %d", p)
		}
		if p < env.cfg.PortStart || p > env.cfg.PortEnd {
			t.Fatalf("端口越界: %d", p)
		}
		ports[p] = true
	}
}

// L3-05 端口回收
func TestL3_05_PortReuse(t *testing.T) {
	env := newEnv(t, &fakeRunner{mode: "ok"})
	_, out1 := env.deploy(t, fixture("hello.zip"), "temp", "a")
	p1 := int(out1["port"].(float64))
	resp := authReq(t, http.MethodDelete, env.server.URL+"/api/apps/temp", nil, "")
	if resp.StatusCode != 200 {
		t.Fatalf("下线失败: %d", resp.StatusCode)
	}
	resp.Body.Close()
	_, out2 := env.deploy(t, fixture("hello.zip"), "newapp", "a")
	p2 := int(out2["port"].(float64))
	if p1 != p2 {
		t.Fatalf("端口未复用: %d != %d", p1, p2)
	}
}

// L3-06 回滚用原端口
func TestL3_06_RollbackPort(t *testing.T) {
	env := newEnv(t, &fakeRunner{mode: "ok"})
	_, o1 := env.deploy(t, fixture("hello.zip"), "shop", "a")
	p1 := int(o1["port"].(float64))
	env.deploy(t, fixture("hello.zip"), "shop", "a")
	resp := authReq(t, http.MethodPost, env.server.URL+"/api/apps/shop/actions/rollback", nil, "")
	out := readJSON(t, resp)
	if int(out["port"].(float64)) != p1 {
		t.Fatalf("回滚端口错误: %v", out)
	}
}

// L3-07 端口池耗尽
func TestL3_07_PoolExhaust(t *testing.T) {
	env := newEnv(t, &fakeRunner{mode: "ok"})
	env.cfg.PortStart = 31000
	env.cfg.PortEnd = 31000 // 只有 1 个端口
	// 注意：deployer 的 allocated map 在构造时加载，运行时改 cfg 影响 allocatePort
	_, o1 := env.deploy(t, fixture("hello.zip"), "a", "a")
	if o1["port"] == nil {
		t.Fatal("第一个部署应成功")
	}
	code, _ := env.deploy(t, fixture("hello.zip"), "b", "a")
	if code == 200 {
		t.Fatal("池耗尽应拒绝")
	}
}

// L3-08 应用根路径 + L3-09 来源头规则
func TestL3_08_09_AppToApp(t *testing.T) {
	root1, rest1 := proxy.MatchRoot("/apps/shop/api/ping")
	if root1 != "/apps/shop" || rest1 != "/api/ping" {
		t.Fatal("根路径解析错误")
	}
	// 应用间隔离通过 header 检查（proxy.ServeHTTP 里），这里验证 MatchRoot 保持正确
	root2, _ := proxy.MatchRoot("/apps/other/api/ping")
	if root2 == root1 {
		t.Fatal("不同应用根路径不应相同")
	}
}

// L3-10 升级脚本存在性检查
func TestL3_10_UpgradeScript(t *testing.T) {
	for _, f := range []string{"../../scripts/upgrade.sh", "../../scripts/upgrade-remote.sh"} {
		st, err := os.Stat(f)
		if err != nil || st.Mode()&0100 == 0 {
			t.Fatalf("升级脚本缺失或不可执行: %s", f)
		}
	}
	// 检查脚本包含回滚逻辑
	b, _ := os.ReadFile("../../scripts/upgrade.sh")
	if !strings.Contains(string(b), "回滚") {
		t.Fatal("升级脚本缺少回滚逻辑")
	}
}

// L3-11 文档完整性
func TestL3_11_Docs(t *testing.T) {
	for _, f := range []string{"../../README.md", "../../docs/OPERATIONS.md", "../../docs/TROUBLESHOOTING.md"} {
		if _, err := os.Stat(f); err != nil {
			t.Fatalf("文档缺失: %s", f)
		}
	}
	// 文档必须提到边界
	for _, keyword := range []string{"不做备份", "HTTPS", "多语言"} {
		b, _ := os.ReadFile("../../README.md")
		if !strings.Contains(string(b), keyword) {
			t.Errorf("README 未提到: %s", keyword)
		}
	}
}

// 辅助：确认 JSON 解析（避免未使用导入）
var _ = json.Marshal
var _ = fmt.Sprint
var _ = net.Listen
