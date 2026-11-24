package unit

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"minibase/internal/auth"
	"minibase/internal/config"
	"minibase/internal/database"
)

func apiReq(t *testing.T, env *testEnv, method, path string, body []byte) (int, []byte) {
	t.Helper()
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, _ := http.NewRequest(method, env.server.URL+path, r)
	req.Header.Set("Authorization", "Bearer "+testToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

// L4-01 生命周期：pause 保留数据，resume 恢复
func TestL4_01_StopStartKeepsData(t *testing.T) {
	env := newEnv(t, &fakeRunner{mode: "ok"})
	_, _ = env.deploy(t, fixture("hello.zip"), "shop", "a")

	code, _ := apiReq(t, env, http.MethodPost, "/api/apps/shop/actions/pause", nil)
	if code != 200 {
		t.Fatalf("pause 失败: %d", code)
	}
	var app *database.App
	app, _ = env.store.GetAppByName("shop")
	if app.Status != "已暂停" {
		t.Fatalf("状态未更新: %s", app.Status)
	}
	var n int
	_ = env.store.DB.QueryRow(`SELECT COUNT(*) FROM versions WHERE app_id=?`, app.ID).Scan(&n)
	if n == 0 {
		t.Fatal("pause 删除了版本数据")
	}

	code, _ = apiReq(t, env, http.MethodPost, "/api/apps/shop/actions/resume", nil)
	if code != 200 {
		t.Fatalf("resume 失败: %d", code)
	}
	app, _ = env.store.GetAppByName("shop")
	if app.Status != "运行中" {
		t.Fatalf("启动后状态: %s", app.Status)
	}
}

// L4-02 状态机：运行中执行 resume 返回 409
func TestL4_02_StateGuard(t *testing.T) {
	env := newEnv(t, &fakeRunner{mode: "ok"})
	_, _ = env.deploy(t, fixture("hello.zip"), "shop", "a")
	code, body := apiReq(t, env, http.MethodPost, "/api/apps/shop/actions/resume", nil)
	if code != 409 {
		t.Fatalf("期望 409，实际 %d: %s", code, body)
	}
	if !strings.Contains(string(body), "运行中") {
		t.Fatalf("409 响应没有当前状态: %s", body)
	}
}

// L4-03 编辑应用：需先暂停；per-app 配置生效
func TestL4_03_AppEdit(t *testing.T) {
	env := newEnv(t, &fakeRunner{mode: "ok"})
	_, _ = env.deploy(t, fixture("hello.zip"), "shop", "a")

	body := []byte(`{"memory_limit_mb":128,"start_cmd":"node custom.js"}`)
	code, resp := apiReq(t, env, http.MethodPut, "/api/apps/shop", body)
	if code != 409 {
		t.Fatalf("运行中编辑期望 409，实际 %d: %s", code, resp)
	}

	_, _ = apiReq(t, env, http.MethodPost, "/api/apps/shop/actions/pause", nil)
	code, resp = apiReq(t, env, http.MethodPut, "/api/apps/shop", body)
	if code != 200 {
		t.Fatalf("暂停后编辑失败 %d: %s", code, resp)
	}

	app, _ := env.store.GetAppByName("shop")
	cfg, err := env.store.GetAppConfig(app.ID)
	if err != nil || cfg.MemoryLimitMB == nil || *cfg.MemoryLimitMB != 128 {
		t.Fatalf("配置未保存: %+v %v", cfg, err)
	}
	mem, _, _, cmd := env.deployer.EffectiveConfig(app.ID)
	if mem != 128 || cmd != "node custom.js" {
		t.Fatalf("生效配置不对: mem=%d cmd=%s", mem, cmd)
	}
}

// L4-04 版本列表和部署历史
func TestL4_04_VersionsAndHistory(t *testing.T) {
	env := newEnv(t, &fakeRunner{mode: "ok"})
	_, _ = env.deploy(t, fixture("hello.zip"), "shop", "a")
	_, _ = env.deploy(t, fixture("hello.zip"), "shop", "a")

	code, body := apiReq(t, env, http.MethodGet, "/api/apps/shop/versions", nil)
	if code != 200 {
		t.Fatalf("版本列表失败: %d", code)
	}
	var versions []map[string]any
	_ = json.Unmarshal(body, &versions)
	if len(versions) < 2 {
		t.Fatalf("版本数不对: %d", len(versions))
	}

	code, body = apiReq(t, env, http.MethodGet, "/api/apps/shop/deployments", nil)
	if code != 200 {
		t.Fatalf("部署历史失败: %d", code)
	}
	if !strings.Contains(string(body), "部署成功") {
		t.Fatalf("历史里没有部署事件: %s", body)
	}
}

// L4-05 资源用量接口
func TestL4_05_Usage(t *testing.T) {
	env := newEnv(t, &fakeRunner{mode: "ok"})
	_, _ = env.deploy(t, fixture("hello.zip"), "shop", "a")

	code, body := apiReq(t, env, http.MethodGet, "/api/usage", nil)
	if code != 200 {
		t.Fatalf("usage 失败: %d", code)
	}
	for _, key := range []string{"disk", "memory", "apps", "ports", "storage"} {
		if !strings.Contains(string(body), key) {
			t.Fatalf("usage 缺少 %s: %s", key, body)
		}
	}

	code, body = apiReq(t, env, http.MethodGet, "/api/apps/shop/usage", nil)
	if code != 200 {
		t.Fatalf("应用 usage 失败: %d", code)
	}
	if !strings.Contains(string(body), "memory_limit_mb") {
		t.Fatalf("应用用量缺字段: %s", body)
	}
}

// L4-06 CORS 白名单
func TestL4_06_Cors(t *testing.T) {
	env := newEnv(t, &fakeRunner{mode: "ok"})
	_, _ = env.deploy(t, fixture("hello.zip"), "shop", "a")

	body := []byte(`{"domain":"https://admin.example.com"}`)
	code, resp := apiReq(t, env, http.MethodPost, "/api/apps/shop/cors", body)
	if code != 200 {
		t.Fatalf("添加 CORS 失败 %d: %s", code, resp)
	}
	var c map[string]any
	_ = json.Unmarshal(resp, &c)
	id := c["id"].(string)

	code, resp = apiReq(t, env, http.MethodGet, "/api/apps/shop/cors", nil)
	if code != 200 || !strings.Contains(string(resp), "admin.example.com") {
		t.Fatalf("CORS 列表失败: %s", resp)
	}

	// 浏览器跨域：命中白名单应放行并带回头
	req, _ := http.NewRequest(http.MethodGet, env.server.URL+"/apps/shop/", nil)
	req.Header.Set("Origin", "https://admin.example.com")
	r2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b2, _ := io.ReadAll(r2.Body)
	r2.Body.Close()
	if r2.StatusCode != 200 {
		t.Fatalf("白名单 Origin 被拒绝: %d %s", r2.StatusCode, b2)
	}
	if r2.Header.Get("Access-Control-Allow-Origin") != "https://admin.example.com" {
		t.Fatal("缺少 CORS 响应头")
	}

	// 未命中应 403
	req, _ = http.NewRequest(http.MethodGet, env.server.URL+"/apps/shop/", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	r3, _ := http.DefaultClient.Do(req)
	r3.Body.Close()
	if r3.StatusCode != 403 {
		t.Fatalf("非白名单 Origin 应 403，实际 %d", r3.StatusCode)
	}

	// OPTIONS 预检
	req, _ = http.NewRequest(http.MethodOptions, env.server.URL+"/apps/shop/", nil)
	req.Header.Set("Origin", "https://admin.example.com")
	r4, _ := http.DefaultClient.Do(req)
	r4.Body.Close()
	if r4.StatusCode != 204 {
		t.Fatalf("预检应 204，实际 %d", r4.StatusCode)
	}

	code, _ = apiReq(t, env, http.MethodDelete, "/api/apps/shop/cors/"+id, nil)
	if code != 200 {
		t.Fatalf("删除 CORS 失败: %d", code)
	}
}

// L4-07 Token 轮换
func TestL4_07_TokenRotate(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.DataDir = dir
	cfg.Token = testToken
	cfg.ConfigPath = dir + "/config.json"
	b, _ := json.MarshalIndent(cfg, "", "  ")
	if err := writeFile(cfg.ConfigPath, b); err != nil {
		t.Fatal(err)
	}

	// 迁移：明文加载后写成哈希
	loaded, err := config.Load(cfg.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.TokenHash != HashForTest(testToken) {
		t.Fatalf("Token 未迁移为哈希: %s", loaded.TokenHash)
	}

	if err := loaded.SetTokenHash("new-token"); err != nil {
		t.Fatal(err)
	}
	again, _ := config.Load(cfg.ConfigPath)
	if again.TokenHash != HashForTest("new-token") {
		t.Fatal("新 Token 未写回配置")
	}
	if again.Token != "" {
		t.Fatal("配置里还有明文 Token")
	}

	// 鉴权：新 Token 通过，旧 Token 拒绝
	h := auth.Middleware(again)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer new-token")
	rr := newRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("新 Token 未通过: %d", rr.Code)
	}
	req2, _ := http.NewRequest(http.MethodGet, "/", nil)
	req2.Header.Set("Authorization", "Bearer "+testToken)
	rr2 := newRecorder()
	h.ServeHTTP(rr2, req2)
	if rr2.Code != 401 {
		t.Fatalf("旧 Token 应 401，实际 %d", rr2.Code)
	}
}

func HashForTest(token string) string { return config.HashToken(token) }

func writeFile(path string, data []byte) error { return os.WriteFile(path, data, 0o600) }
func newRecorder() *httptest.ResponseRecorder  { return httptest.NewRecorder() }
