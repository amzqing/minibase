package unit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"minibase/internal/api"
	"minibase/internal/auth"
	"minibase/internal/config"
	"minibase/internal/crypto"
	"minibase/internal/database"
	"minibase/internal/deploy"
	"minibase/internal/proxy"
	"minibase/internal/resource"
	"minibase/internal/runtime"
)

const testToken = "unit-test-token"

func fixture(name string) string {
	return filepath.Join("..", "fixtures", name)
}

func freeBasePort() int {
	return 20000 + rand.Intn(20000)
}

type testEnv struct {
	cfg      *config.Config
	store    *database.Store
	deployer *deploy.Deployer
	server   *httptest.Server
}

func newEnv(t *testing.T, runner runtime.AppRunner) *testEnv {
	t.Helper()
	dir := t.TempDir()
	base := freeBasePort()
	cfg := config.Default()
	cfg.DataDir = dir
	cfg.Token = testToken
	cfg.PortStart = base
	cfg.PortEnd = base + 60
	cfg.HealthTimeoutMS = 400
	cfg.MemoryLimitMB = 64
	cfg.CgroupRoot = ""
	cfg.MasterKey = "unit-test-master-key-32bytes-min!!"
	store, err := database.Open(cfg.DataDir, cfg.BusyTimeoutMS)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	d := deploy.New(cfg, store, runner, resource.NoopLimiter{})
	cipher, _ := crypto.New(cfg.MasterKey)
	d.SetCipher(cipher)
	t.Cleanup(d.Shutdown)
	srv := newLocalServer(api.New(store, d, cfg.Token, cfg).Handler())
	t.Cleanup(srv.Close)
	return &testEnv{cfg: cfg, store: store, deployer: d, server: srv}
}

func newLocalServer(h http.Handler) *httptest.Server {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	srv := &httptest.Server{Listener: ln, Config: &http.Server{Handler: h}}
	srv.Start()
	return srv
}

func (e *testEnv) deploy(t *testing.T, zipPath, app, mode string) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	w := multipartWriter(&buf, zipPath, app, mode)
	req, _ := http.NewRequest(http.MethodPost, e.server.URL+"/api/deploy", &buf)
	req.Header.Set("Content-Type", w)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(body, &out)
	return resp.StatusCode, out
}

func multipartWriter(buf *bytes.Buffer, zipPath, app, mode string) string {
	// 避免依赖 mime/multipart 的复杂签名，这里手写一个最小 multipart。
	boundary := "minibase-test-boundary"
	f, err := os.Open(zipPath)
	if err != nil {
		panic(err)
	}
	data, _ := io.ReadAll(f)
	f.Close()
	fmt.Fprintf(buf, "--%s\r\n", boundary)
	fmt.Fprintf(buf, "Content-Disposition: form-data; name=\"file\"; filename=\"%s\"\r\n", filepath.Base(zipPath))
	buf.WriteString("Content-Type: application/zip\r\n\r\n")
	buf.Write(data)
	buf.WriteString("\r\n")
	for k, v := range map[string]string{"app": app, "mode": mode} {
		fmt.Fprintf(buf, "--%s\r\n", boundary)
		fmt.Fprintf(buf, "Content-Disposition: form-data; name=\"%s\"\r\n\r\n%s\r\n", k, v)
	}
	fmt.Fprintf(buf, "--%s--\r\n", boundary)
	return "multipart/form-data; boundary=" + boundary
}

// fakeRunner 用真实 TCP 监听模拟应用，健康检查是真 HTTP 请求。
type fakeRunner struct {
	mu          sync.Mutex
	mode        string // ok | hang | fail | second-fail
	starts      int
	lastEnv     []string
	beforeStart func(int)
	onReattach  func(pid, port int) *runtime.ProcessHandle
	servers     []*http.Server
}

func (f *fakeRunner) Start(_ string, _ string, env []string, port int) (*runtime.ProcessHandle, error) {
	f.mu.Lock()
	f.starts++
	n := f.starts
	f.lastEnv = append([]string(nil), env...)
	mode := f.mode
	if mode == "second-fail" && n > 1 {
		mode = "fail"
	}
	hook := f.beforeStart
	f.mu.Unlock()
	if hook != nil {
		hook(n)
	}
	if mode == "fail" {
		return nil, fmt.Errorf("假应用启动失败")
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, err
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mode == "hang" {
			time.Sleep(5 * time.Second)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Fake-App", "yes")
		fmt.Fprintf(w, `{"path":%q,"method":%q}`, r.URL.Path, r.Method)
	})
	srv := &http.Server{Handler: handler}
	go func() { _ = srv.Serve(ln) }()
	f.mu.Lock()
	f.servers = append(f.servers, srv)
	f.mu.Unlock()
	return runtime.NewProcessHandle(90000+n, port, func() error { return srv.Close() }), nil
}

// Reattach 模拟热重载后按 PID 找回旧应用进程。
func (f *fakeRunner) Reattach(pid, port int) *runtime.ProcessHandle {
	f.mu.Lock()
	hook := f.onReattach
	f.mu.Unlock()
	if hook != nil {
		return hook(pid, port)
	}
	return runtime.NewProcessHandle(pid, port, func() error { return nil })
}

func (f *fakeRunner) HealthCheck(port int, timeout time.Duration) bool {
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
	if err != nil {
		return false
	}
	resp.Body.Close()
	return true
}

func (f *fakeRunner) startCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.starts
}

// L1-01 Token 认证
func TestL1_01_TokenAuth(t *testing.T) {
	h := auth.Middleware(&config.Config{Token: testToken})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	cases := []struct {
		name   string
		header string
		want   int
	}{
		{"无 Token", "", http.StatusUnauthorized},
		{"错 Token", "Bearer wrong", http.StatusUnauthorized},
		{"对 Token", "Bearer " + testToken, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/status/hello", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("got %d, want %d", w.Code, tc.want)
			}
		})
	}
}

// L1-02 建应用建库原子性
func TestL1_02_CreateAppDefaultDBAtomic(t *testing.T) {
	dir := t.TempDir()
	store, err := database.Open(dir, 5000)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	app := &database.App{ID: "app_atomic", Name: "app_atomic",
		RootPath: "/apps/app_atomic", Status: "构建中", CreatedAt: 1}
	if err := store.CreateApp(app); err != nil {
		t.Fatal(err)
	}

	for _, stage := range []string{"insert_record", "create_file"} {
		store.CreateAppFailAt = stage
		if _, err := store.CreateDatabase("app_atomic", "main", true); err == nil {
			t.Fatalf("%s: 期望注入失败但没失败", stage)
		}
		d, _ := store.GetDefaultDatabase("app_atomic")
		if d != nil {
			t.Fatalf("%s: 失败后不该有库记录", stage)
		}
		if _, err := os.Stat(filepath.Join(dir, "databases", "app_atomic", "main.db")); !os.IsNotExist(err) {
			t.Fatalf("%s: 失败后不该有库文件", stage)
		}
	}
	store.CreateAppFailAt = ""
	d, err := store.EnsureDefaultDatabase("app_atomic")
	if err != nil || d == nil {
		t.Fatalf("正常建库失败: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "databases", "app_atomic", "main.db")); err != nil {
		t.Fatal("正常建库文件失败")
	}
}

// L1-03 启动命令解析
func TestL1_03_StartCommand(t *testing.T) {
	var withStart, withMain, withNone runtime.PackageJSON
	withStart.Scripts.Start = "node server.js"
	withMain.Main = "index.js"
	for _, tc := range []struct {
		name string
		pkg  runtime.PackageJSON
		want string
		err  bool
	}{
		{"有 scripts.start", withStart, "node server.js", false},
		{"只有 main", withMain, "node index.js", false},
		{"两者都无", withNone, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.pkg.StartCommand()
			if tc.err {
				if err == nil {
					t.Fatal("期望报错但没报错")
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got %q err=%v, want %q", got, err, tc.want)
			}
		})
	}
}

// L1-04 mode a 校验
func TestL1_04_ModeAValidation(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "no-modules.zip")
	makeZip(t, zipPath, map[string]string{"package.json": `{"name":"x","main":"index.js"}`})
	_, _, err := runtime.InspectZip(zipPath, "a")
	if err == nil || !strings.Contains(err.Error(), "node_modules") {
		t.Fatalf("期望 node_modules 错误, got %v", err)
	}
}

// L1-05 mode b 校验
func TestL1_05_ModeBValidation(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "no-pkg.zip")
	makeZip(t, zipPath, map[string]string{"index.js": `console.log(1)`})
	_, _, err := runtime.InspectZip(zipPath, "b")
	if err == nil || !strings.Contains(err.Error(), "package.json") {
		t.Fatalf("期望 package.json 错误, got %v", err)
	}
}

func makeZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	tmp := t.TempDir()
	for name, content := range files {
		full := filepath.Join(tmp, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("sh", "-c", fmt.Sprintf("cd %q && zip -qr %q .", tmp, path))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("zip 失败: %v %s", err, out)
	}
}

// L1-06 版本目录生成
func TestL1_06_VersionDirs(t *testing.T) {
	runner := &fakeRunner{mode: "ok"}
	env := newEnv(t, runner)
	for i := 0; i < 2; i++ {
		code, out := env.deploy(t, fixture("hello.zip"), "shop", "a")
		if code != http.StatusOK {
			t.Fatalf("第 %d 次部署失败: %d %v", i+1, code, out)
		}
	}
	base := filepath.Join(env.cfg.DataDir, "apps", "shop", "versions")
	for _, v := range []string{"v1", "v2"} {
		if _, err := os.Stat(filepath.Join(base, v)); err != nil {
			t.Fatalf("缺少版本目录 %s: %v", v, err)
		}
	}
	app, _ := env.store.GetAppByName("shop")
	if app.CurrentVersion != "v2" {
		t.Fatalf("当前版本应为 v2, got %s", app.CurrentVersion)
	}
}

// L1-07 健康检查判定
func TestL1_07_HealthCheck(t *testing.T) {
	// 正常回应 → 切流量
	okRunner := &fakeRunner{mode: "ok"}
	okEnv := newEnv(t, okRunner)
	code, out := okEnv.deploy(t, fixture("hello.zip"), "shop", "a")
	if code != http.StatusOK {
		t.Fatalf("健康应用部署失败: %d %v", code, out)
	}

	// 永不回应 → 不切换，返回失败
	hangRunner := &fakeRunner{mode: "hang"}
	hangEnv := newEnv(t, hangRunner)
	code, out = hangEnv.deploy(t, fixture("hello.zip"), "shop", "a")
	if code == http.StatusOK {
		t.Fatalf("假死应用不该部署成功: %v", out)
	}
	app, _ := hangEnv.store.GetAppByName("shop")
	if app != nil && app.CurrentVersion != "" {
		t.Fatalf("假死应用不该切流量, got %s", app.CurrentVersion)
	}
}

// L1-08 路径剥前缀
func TestL1_08_StripPrefix(t *testing.T) {
	u, err := url.Parse("/apps/shop/api/users?page=1")
	if err != nil {
		t.Fatal(err)
	}
	root, rest := proxy.MatchRoot(u.Path)
	if root != "/apps/shop" || rest != "/api/users" || u.RawQuery != "page=1" {
		t.Fatalf("got root=%q rest=%q query=%q", root, rest, u.RawQuery)
	}
}

// L1-09 干净根路径
func TestL1_09_CleanRoot(t *testing.T) {
	for _, p := range []string{"/apps/shop", "/apps/shop/"} {
		root, rest := proxy.MatchRoot(p)
		if root != "/apps/shop" || rest != "/" {
			t.Fatalf("%s: got root=%q rest=%q", p, root, rest)
		}
	}
}

// L1-10 根路径环境变量
func TestL1_10_EnvVars(t *testing.T) {
	runner := &fakeRunner{mode: "ok"}
	env := newEnv(t, runner)
	code, out := env.deploy(t, fixture("hello.zip"), "shop", "a")
	if code != http.StatusOK {
		t.Fatalf("deploy failed: %v", out)
	}
	port := int(out["port"].(float64))
	wantPort := fmt.Sprintf("PORT=%d", port)
	wantMount := "MOUNT_PATH=/apps/shop"
	foundPort, foundMount := false, false
	for _, e := range runner.lastEnv {
		if e == wantPort {
			foundPort = true
		}
		if e == wantMount {
			foundMount = true
		}
	}
	if !foundPort || !foundMount {
		t.Fatalf("环境变量错误: %v (want %s, %s)", runner.lastEnv, wantPort, wantMount)
	}
}

// L1-11 失败不影响老版本
func TestL1_11_OldVersionUnchanged(t *testing.T) {
	runner := &fakeRunner{mode: "second-fail"}
	env := newEnv(t, runner)
	code, out := env.deploy(t, fixture("hello.zip"), "shop", "a")
	if code != http.StatusOK {
		t.Fatalf("v1 部署失败: %v", out)
	}
	code, out = env.deploy(t, fixture("bad.zip"), "shop", "a")
	if code == http.StatusOK {
		t.Fatalf("v2 不该成功: %v", out)
	}
	app, _ := env.store.GetAppByName("shop")
	if app.CurrentVersion != "v1" || app.Status != "运行中" {
		t.Fatalf("老版本状态被破坏: version=%s status=%s", app.CurrentVersion, app.Status)
	}
	resp, err := http.Get(env.server.URL + "/apps/shop/api/users")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("X-Fake-App") == "" {
		t.Fatalf("老版本不再服务: %d", resp.StatusCode)
	}
}

// L1-12 连续推送排队
func TestL1_12_SerialDeploy(t *testing.T) {
	runner := &fakeRunner{mode: "ok"}
	firstStarted := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	runner.beforeStart = func(n int) {
		if n == 1 {
			once.Do(func() { close(firstStarted) })
			<-release
		}
	}
	env := newEnv(t, runner)

	done := make(chan error, 2)
	go func() {
		code, out := env.deploy(t, fixture("hello.zip"), "shop", "a")
		if code != http.StatusOK {
			done <- fmt.Errorf("first deploy failed: %v", out)
			return
		}
		done <- nil
	}()
	<-firstStarted
	go func() {
		code, out := env.deploy(t, fixture("hello.zip"), "shop", "a")
		if code != http.StatusOK {
			done <- fmt.Errorf("second deploy failed: %v", out)
			return
		}
		done <- nil
	}()
	time.Sleep(400 * time.Millisecond)
	if got := runner.startCount(); got != 1 {
		t.Fatalf("第二个部署抢跑了: starts=%d", got)
	}
	close(release)
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

// L1-13 端口分配不冲突
func TestL1_13_PortNoConflict(t *testing.T) {
	runner := &fakeRunner{mode: "ok"}
	env := newEnv(t, runner)
	_, out1 := env.deploy(t, fixture("hello.zip"), "shop1", "a")
	_, out2 := env.deploy(t, fixture("hello.zip"), "shop2", "a")
	p1 := int(out1["port"].(float64))
	p2 := int(out2["port"].(float64))
	if p1 == p2 || p1 == 0 || p2 == 0 {
		t.Fatalf("端口冲突: %d %d", p1, p2)
	}
}

// L1-14 数据库孤儿对账
func TestL1_14_Reconcile(t *testing.T) {
	dir := t.TempDir()
	store, err := database.Open(dir, 5000)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, name := range []string{"app_normal", "app_broken"} {
		app := &database.App{ID: "id_" + name, Name: name, RootPath: "/apps/" + name, CreatedAt: 1}
		if err := store.CreateApp(app); err != nil {
			t.Fatal(err)
		}
		if _, err := store.EnsureDefaultDatabase(name); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.Remove(filepath.Join(dir, "databases", "app_broken", "main.db"))
	if err := os.MkdirAll(filepath.Join(dir, "databases", "app_orphan"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "databases", "app_orphan", "orphan.db"), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := store.Reconcile()
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Healthy) != 1 || report.Healthy[0] != "app_normal/main" {
		t.Fatalf("healthy 错误: %v", report.Healthy)
	}
	if len(report.OrphanFiles) != 1 || !strings.Contains(report.OrphanFiles[0], "app_orphan") {
		t.Fatalf("孤儿文件错误: %v", report.OrphanFiles)
	}
	if len(report.BrokenRefs) != 1 || report.BrokenRefs[0] != "app_broken/main" {
		t.Fatalf("坏引用错误: %v", report.BrokenRefs)
	}
}

// L1-15 CLI 是 HTTP 客户端
func TestL1_15_CLIHttpClient(t *testing.T) {
	var mu sync.Mutex
	var gotPath, gotAuth string
	fakeAPI := newLocalServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/deploy" {
			_, _ = w.Write([]byte(`{"version":"v1","access_url":"http://x/apps/b","notice":"n"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"运行中"}`))
	}))
	defer fakeAPI.Close()

	bin := filepath.Join(t.TempDir(), "minibase")
	build := exec.Command("go", "build", "-o", bin, "./cmd/minibase")
	build.Dir = "../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("编译 CLI 失败: %v %s", err, out)
	}

	for _, args := range [][]string{
		{"status", "shop", "-server", fakeAPI.URL},
		{"deploy", fixture("hello.zip"), "-server", fakeAPI.URL, "-mode", "a"},
		{"logs", "shop", "-server", fakeAPI.URL},
	} {
		cmd := exec.Command(bin, args...)
		cmd.Env = append(os.Environ(), "MINIBASE_TOKEN="+testToken)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("CLI %v 失败: %v %s", args, err, out)
		}
		mu.Lock()
		path := gotPath
		authHeader := gotAuth
		mu.Unlock()
		if authHeader != "Bearer "+testToken {
			t.Fatalf("%v: Token 头错误: %q", args, authHeader)
		}
		want := map[string]string{"status": "/api/status/shop", "deploy": "/api/deploy", "logs": "/api/logs/shop"}[args[0]]
		if path != want {
			t.Fatalf("%v: path=%q want %q", args, path, want)
		}
	}
}

// 保证 context 包被使用（部署超时用得上）。
var _ = context.Background
