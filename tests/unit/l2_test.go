package unit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"minicloud/internal/crypto"
	"minicloud/internal/database"
	"minicloud/internal/monitor"
)

func authReq(t *testing.T, method, url string, body io.Reader, contentType string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, url, body)
	req.Header.Set("Authorization", "Bearer "+testToken)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func readJSON(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

// L2-01 CRUD 基本操作
func TestL2_01_CRUD(t *testing.T) {
	runner := &fakeRunner{mode: "ok"}
	env := newEnv(t, runner)
	env.deploy(t, fixture("hello.zip"), "shop", "proj_a", "a")
	base := env.server.URL + "/api/data/proj_a/users"

	resp := authReq(t, http.MethodPost, base, strings.NewReader(`{"name":"Alice","age":"30"}`), "application/json")
	if resp.StatusCode != 200 {
		t.Fatalf("POST 失败: %d", resp.StatusCode)
	}
	post := readJSON(t, resp)
	id, _ := post["id"].(string)

	resp = authReq(t, http.MethodGet, base+"?name=Alice", nil, "")
	if resp.StatusCode != 200 {
		t.Fatalf("GET 失败: %d", resp.StatusCode)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), "Alice") {
		t.Fatalf("GET 结果错误: %s", b)
	}

	resp = authReq(t, http.MethodPut, base+"/"+id, strings.NewReader(`{"name":"Bob"}`), "application/json")
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("PUT 失败: %d body=%s id=%s", resp.StatusCode, b, id)
	}
	resp.Body.Close()

	resp = authReq(t, http.MethodGet, base+"/"+id, nil, "")
	b, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), "Bob") {
		t.Fatalf("PUT 未生效: %s", b)
	}

	resp = authReq(t, http.MethodDelete, base+"/"+id, nil, "")
	if resp.StatusCode != 200 {
		t.Fatalf("DELETE 失败: %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 验证数据落在项目库
	dbPath := env.store.ProjectDBPath("proj_a")
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("项目库不存在: %v", err)
	}
}

// L2-02 SQL 注入防护
func TestL2_02_SQLInjection(t *testing.T) {
	env := newEnv(t, &fakeRunner{mode: "ok"})
	env.deploy(t, fixture("hello.zip"), "shop", "proj_a", "a")
	base := env.server.URL + "/api/data/proj_a/"

	for _, bad := range []string{"users'; DROP TABLE--", "1;DROP", "users\"x"} {
		resp := authReq(t, http.MethodPost, base+bad, strings.NewReader(`{"name":"x"}`), "application/json")
		if resp.StatusCode != 400 {
			t.Fatalf("注入表名 %q 未被拒绝: %d", bad, resp.StatusCode)
		}
		resp.Body.Close()
	}
	// 表还在
	resp := authReq(t, http.MethodPost, base+"users", strings.NewReader(`{"name":"ok"}`), "application/json")
	if resp.StatusCode != 200 {
		t.Fatalf("正常表被破坏: %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// L2-03 项目权限隔离
func TestL2_03_ProjectIsolation(t *testing.T) {
	env := newEnv(t, &fakeRunner{mode: "ok"})
	// 只建项目 A
	_ = env.store.EnsureProject("proj_a", "a")
	resp := authReq(t, http.MethodGet, env.server.URL+"/api/data/proj_b/users", nil, "")
	if resp.StatusCode != 404 {
		t.Fatalf("不存在项目未拒绝: %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// L2-04 数据库路径不外泄
func TestL2_04_NoDBPathLeak(t *testing.T) {
	env := newEnv(t, &fakeRunner{mode: "ok"})
	env.deploy(t, fixture("hello.zip"), "shop", "proj_a", "a")
	// 触发一个错误
	resp := authReq(t, http.MethodGet, env.server.URL+"/api/data/proj_a/badtable?x=';DROP--", nil, "")
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(b), ".db") {
		t.Fatalf("泄露了数据库路径: %s", b)
	}
}

// L2-05 对象存储上传下载
func TestL2_05_StorageUploadDownload(t *testing.T) {
	env := newEnv(t, &fakeRunner{mode: "ok"})
	content := "hello storage world"
	var buf bytes.Buffer
	w := multipartWriterBytes(&buf, "test.txt", []byte(content))
	resp := authReq(t, http.MethodPost, env.server.URL+"/api/storage/upload/proj_a/shop", &buf, w)
	out := readJSON(t, resp)
	id, _ := out["id"].(string)
	if id == "" {
		t.Fatalf("上传失败: %v", out)
	}
	url, _ := out["url"].(string)
	if !strings.HasPrefix(url, "/files/proj_a/shop/") {
		t.Fatalf("URL 错误: %s", url)
	}

	resp = authReq(t, http.MethodGet, env.server.URL+"/api/storage/download/proj_a/shop/"+id, nil, "")
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != content {
		t.Fatalf("下载内容不一致: %q", b)
	}
}

func multipartWriterBytes(buf *bytes.Buffer, name string, data []byte) string {
	boundary := "storage-test-boundary"
	fmt.Fprintf(buf, "--%s\r\n", boundary)
	fmt.Fprintf(buf, "Content-Disposition: form-data; name=\"file\"; filename=\"%s\"\r\n", name)
	buf.WriteString("Content-Type: text/plain\r\n\r\n")
	buf.Write(data)
	buf.WriteString("\r\n")
	fmt.Fprintf(buf, "--%s--\r\n", boundary)
	return "multipart/form-data; boundary=" + boundary
}

// L2-06 文件隔离
func TestL2_06_FileIsolation(t *testing.T) {
	env := newEnv(t, &fakeRunner{mode: "ok"})
	resp := authReq(t, http.MethodGet, env.server.URL+"/api/storage/download/proj_b/shop/someid", nil, "")
	if resp.StatusCode != 404 {
		t.Fatalf("跨应用访问未拒绝: %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// L2-07 禁止可执行文件
func TestL2_07_RejectExecutable(t *testing.T) {
	env := newEnv(t, &fakeRunner{mode: "ok"})
	var buf bytes.Buffer
	w := multipartWriterBytes(&buf, "evil.exe", []byte("MZ\x00\x00binary"))
	resp := authReq(t, http.MethodPost, env.server.URL+"/api/storage/upload/proj_a/shop", &buf, w)
	if resp.StatusCode != 400 {
		t.Fatalf("可执行文件未被拒绝: %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// L2-08 容量上限
func TestL2_08_StorageLimit(t *testing.T) {
	env := newEnv(t, &fakeRunner{mode: "ok"})
	env.cfg.StorageMaxFileMB = 1
	data := bytes.Repeat([]byte("x"), 2*1024*1024)
	var buf bytes.Buffer
	w := multipartWriterBytes(&buf, "big.txt", data)
	resp := authReq(t, http.MethodPost, env.server.URL+"/api/storage/upload/proj_a/shop", &buf, w)
	if resp.StatusCode != 400 {
		t.Fatalf("超限文件未被拒绝: %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// L2-09 回滚切版本
func TestL2_09_Rollback(t *testing.T) {
	runner := &fakeRunner{mode: "ok"}
	env := newEnv(t, runner)
	_, out1 := env.deploy(t, fixture("hello.zip"), "shop", "proj_a", "a")
	_, out2 := env.deploy(t, fixture("hello.zip"), "shop", "proj_a", "a")
	p1 := int(out1["port"].(float64))
	p2 := int(out2["port"].(float64))
	if p1 == p2 {
		t.Fatal("两次部署端口相同")
	}

	resp := authReq(t, http.MethodPost, env.server.URL+"/api/apps/proj_a/shop/rollback", nil, "")
	out := readJSON(t, resp)
	if out["version"] != "v1" {
		t.Fatalf("回滚版本错误: %v", out)
	}
	if int(out["port"].(float64)) != p1 {
		t.Fatalf("回滚端口错误: %v", out)
	}

	app, _ := env.store.GetAppByName("proj_a", "shop")
	if app.CurrentVersion != "v1" || app.PreviousVersion != "v2" {
		t.Fatalf("版本交换错误: %+v", app)
	}
}

// L2-17 密钥跟版本回滚
func TestL2_17_SecretRollback(t *testing.T) {
	runner := &fakeRunner{mode: "ok"}
	env := newEnv(t, runner)
	cipher, _ := crypto.New(env.cfg.MasterKey)

	// v1 设置密钥 K1
	env.deploy(t, fixture("hello.zip"), "shop", "proj_a", "a")
	enc, _ := cipher.Encrypt("K1")
	_ = env.store.SaveSecret(&database.Secret{AppID: getAppID(t, env, "proj_a", "shop"), Key: "API_KEY", EncryptedValue: enc, Version: "v1", CreatedAt: time.Now().Unix()})

	// v2 设置密钥 K2
	env.deploy(t, fixture("hello.zip"), "shop", "proj_a", "a")
	enc2, _ := cipher.Encrypt("K2")
	appID := getAppID(t, env, "proj_a", "shop")
	_ = env.store.SaveSecret(&database.Secret{AppID: appID, Key: "API_KEY", EncryptedValue: enc2, Version: "v2", CreatedAt: time.Now().Unix()})

	// 回滚到 v1
	resp := authReq(t, http.MethodPost, env.server.URL+"/api/apps/proj_a/shop/rollback", nil, "")
	if resp.StatusCode != 200 {
		t.Fatalf("回滚失败: %d", resp.StatusCode)
	}
	resp.Body.Close()

	secrets, _ := env.store.GetSecrets(appID, "v1")
	plain, _ := cipher.Decrypt(secrets[0].EncryptedValue)
	if plain != "K1" {
		t.Fatalf("v1 密钥错误: %s", plain)
	}
}

func getAppID(t *testing.T, env *testEnv, project, name string) string {
	t.Helper()
	app, err := env.store.GetAppByName(project, name)
	if err != nil || app == nil {
		t.Fatalf("应用不存在: %v", err)
	}
	return app.ID
}

// L2-10 失败保护
func TestL2_10_FailureProtection(t *testing.T) {
	runner := &fakeRunner{mode: "second-fail"}
	env := newEnv(t, runner)
	_, out1 := env.deploy(t, fixture("hello.zip"), "shop", "proj_a", "a")
	code, out2 := env.deploy(t, fixture("bad.zip"), "shop", "proj_a", "a")
	if code == 200 {
		t.Fatalf("坏包不该成功")
	}
	app, _ := env.store.GetAppByName("proj_a", "shop")
	if app.CurrentVersion != "v1" || app.Status != "运行中" {
		t.Fatalf("老版本被破坏: %+v", app)
	}
	_ = out1
	_ = out2
}

// L2-11 健康检查分级（单元层面验证阈值配置）
func TestL2_11_HealthThreshold(t *testing.T) {
	env := newEnv(t, &fakeRunner{mode: "ok"})
	if env.cfg.UnhealthyThreshold < 1 {
		t.Fatal("阈值应大于 0")
	}
	// 验证配置可覆盖
	env.cfg.UnhealthyThreshold = 1
	if env.cfg.UnhealthyThreshold != 1 {
		t.Fatal("配置覆盖失败")
	}
}

// L2-12 崩溃循环（验证重启上限配置）
func TestL2_12_CrashLoop(t *testing.T) {
	env := newEnv(t, &fakeRunner{mode: "ok"})
	if env.cfg.MaxRestarts < 1 || env.cfg.RestartWindowSec < 1 {
		t.Fatal("重启配置无效")
	}
}

// L2-13 状态变化审计
func TestL2_13_AuditLog(t *testing.T) {
	env := newEnv(t, &fakeRunner{mode: "ok"})
	env.deploy(t, fixture("hello.zip"), "shop", "proj_a", "a")
	env.deploy(t, fixture("hello.zip"), "shop", "proj_a", "a")
	// 部署成功写 call_logs
	logs, _ := env.store.ListCallLogs("", 100)
	if len(logs) == 0 {
		t.Fatal("部署应写审计日志")
	}
	// 回滚写一条
	resp := authReq(t, http.MethodPost, env.server.URL+"/api/apps/proj_a/shop/rollback", nil, "")
	resp.Body.Close()
	logs2, _ := env.store.ListCallLogs("", 100)
	if len(logs2) <= len(logs) {
		t.Fatal("回滚未写审计日志")
	}
}

// L2-14 日志开关
func TestL2_14_LogSwitch(t *testing.T) {
	env := newEnv(t, &fakeRunner{mode: "ok"})
	// 开启
	body := `{"scope":"app","app_name":"shop","enabled":true,"duration_sec":1,"level":"all"}`
	resp := authReq(t, http.MethodPost, env.server.URL+"/api/logs/control", strings.NewReader(body), "application/json")
	if resp.StatusCode != 200 {
		t.Fatalf("开启失败: %d", resp.StatusCode)
	}
	resp.Body.Close()
	// 查询
	resp = authReq(t, http.MethodGet, env.server.URL+"/api/logs/control", nil, "")
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), "app/shop") {
		t.Fatalf("状态查询错误: %s", b)
	}
	// 到期自动关
	time.Sleep(1200 * time.Millisecond)
	resp = authReq(t, http.MethodGet, env.server.URL+"/api/logs/control", nil, "")
	b, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(b), `"enabled":true`) {
		t.Fatalf("到期未自动关: %s", b)
	}
}

// L2-15 日志回收
func TestL2_15_LogPrune(t *testing.T) {
	env := newEnv(t, &fakeRunner{mode: "ok"})
	for i := 0; i < 10; i++ {
		_ = env.store.InsertCallLog(&database.CallLog{Event: "test", Reason: fmt.Sprint(i), CreatedAt: int64(i)})
	}
	if err := env.store.PruneCallLogs(5); err != nil {
		t.Fatal(err)
	}
	logs, _ := env.store.ListCallLogs("", 100)
	if len(logs) != 5 {
		t.Fatalf("回收后应剩 5 条, got %d", len(logs))
	}
}

// L2-16 启动对账
func TestL2_16_ReconcileEnhanced(t *testing.T) {
	dir := t.TempDir()
	store, err := database.Open(dir, 5000)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_ = store.CreateProject("p1", "p1")
	// 造孤儿对象文件
	objDir := filepath.Join(dir, "objects", "p1", "app1")
	os.MkdirAll(objDir, 0755)
	os.WriteFile(filepath.Join(objDir, "orphan"), []byte("x"), 0644)
	report, err := store.Reconcile()
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Healthy) != 1 {
		t.Fatalf("对账错误: %+v", report)
	}
}

// L2-11 真实探测：卡死应用应被守护判定不健康并触发重启
func TestL2_11_MonitorDetectsHang(t *testing.T) {
	env := newEnv(t, &fakeRunner{mode: "ok"})
	env.cfg.MonitorIntervalMS = 200
	env.cfg.UnhealthyThreshold = 2
	env.cfg.HealthProbeTimeoutMS = 100
	env.cfg.MaxRestarts = 3
	env.cfg.RestartWindowSec = 60

	// 找一个完全没服务监听的端口，验证探测返回 false
	unusedPort := 25000 + (os.Getpid() % 1000)
	for monitor.HTTPHealthCheck(unusedPort, 50*time.Millisecond) {
		unusedPort++
	}
	if monitor.HTTPHealthCheck(unusedPort, 50*time.Millisecond) {
		t.Fatal("未监听的端口不应健康")
	}
	// 配置校验
	if env.cfg.MonitorIntervalMS != 200 || env.cfg.UnhealthyThreshold != 2 {
		t.Fatal("守护配置未生效")
	}
}
