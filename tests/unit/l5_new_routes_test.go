package unit

import (
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestL5_01_SystemPaths(t *testing.T) {
	env := newEnv(t, &fakeRunner{mode: "ok"})
	_, _ = env.deploy(t, fixture("hello.zip"), "shop", "a")

	for _, path := range []string{"/api/system/usage", "/api/system/audit", "/api/system/log-control"} {
		code, body := apiReq(t, env, http.MethodGet, path, nil)
		if code != 200 {
			t.Fatalf("GET %s = %d: %s", path, code, body)
		}
	}
	code, _ := apiReq(t, env, http.MethodGet, "/api/usage", nil)
	if code != 200 {
		t.Fatalf("旧路径 /api/usage = %d", code)
	}
}

func TestL5_02_AppIsTopLevel(t *testing.T) {
	env := newEnv(t, &fakeRunner{mode: "ok"})
	_, _ = env.deploy(t, fixture("hello.zip"), "shop", "a")

	code, body := apiReq(t, env, http.MethodGet, "/api/projects", nil)
	if code != 404 {
		t.Fatalf("项目层应不存在: %d %s", code, body)
	}
	code, body = apiReq(t, env, http.MethodGet, "/api/apps", nil)
	if code != 200 || !strings.Contains(string(body), "shop") {
		t.Fatalf("应用列表失败: %d %s", code, body)
	}
	if strings.Contains(string(body), "project_id") {
		t.Fatalf("应用列表不应有 project_id: %s", body)
	}

	for _, suffix := range []string{"", "/versions", "/usage", "/logs"} {
		code, body = apiReq(t, env, http.MethodGet, "/api/apps/shop"+suffix, nil)
		if code != 200 {
			t.Fatalf("GET 应用%s = %d: %s", suffix, code, body)
		}
	}
}

func TestL5_03_Actions(t *testing.T) {
	env := newEnv(t, &fakeRunner{mode: "ok"})
	_, _ = env.deploy(t, fixture("hello.zip"), "shop", "a")
	_, _ = env.deploy(t, fixture("hello.zip"), "shop", "a")

	base := "/api/apps/shop/actions/"
	for _, action := range []string{"restart", "pause", "resume", "rollback"} {
		code, body := apiReq(t, env, http.MethodPost, base+action, nil)
		if code != 200 {
			t.Fatalf("%s = %d: %s", action, code, body)
		}
	}
}

func TestL5_04_CorsAndDatabase(t *testing.T) {
	env := newEnv(t, &fakeRunner{mode: "ok"})
	_, _ = env.deploy(t, fixture("hello.zip"), "shop", "a")
	base := "/api/apps/shop/databases/main"

	code, body := apiReq(t, env, http.MethodPost, base+"/tables",
		[]byte(`{"name":"users","fields":[{"name":"name","type":"TEXT","not_null":true},{"name":"age","type":"INTEGER"}]}`))
	if code != 200 {
		t.Fatalf("建表 = %d: %s", code, body)
	}
	code, body = apiReq(t, env, http.MethodGet, base+"/tables", nil)
	if code != 200 || !strings.Contains(string(body), "users") {
		t.Fatalf("表列表 = %d: %s", code, body)
	}
	code, body = apiReq(t, env, http.MethodGet, base+"/tables/users", nil)
	if code != 200 || !strings.Contains(string(body), "name") {
		t.Fatalf("表结构 = %d: %s", code, body)
	}
	code, body = apiReq(t, env, http.MethodPost, base+"/tables/users/indexes",
		[]byte(`{"name":"idx_name","fields":["name"],"unique":false}`))
	if code != 200 {
		t.Fatalf("建索引 = %d: %s", code, body)
	}
	code, _ = apiReq(t, env, http.MethodDelete, base+"/tables/users/indexes/idx_name", nil)
	if code != 200 {
		t.Fatalf("删索引 = %d", code)
	}
	code, body = apiReq(t, env, http.MethodGet, "/api/apps/shop/databases", nil)
	if code != 200 || !strings.Contains(string(body), "main") {
		t.Fatalf("库列表 = %d: %s", code, body)
	}
	code, body = apiReq(t, env, http.MethodGet, base, nil)
	if code != 200 || !strings.Contains(string(body), "table_count") {
		t.Fatalf("库信息 = %d: %s", code, body)
	}
	code, _ = apiReq(t, env, http.MethodDelete, base+"/tables/users", nil)
	if code != 200 {
		t.Fatalf("删表 = %d", code)
	}

	cors := "/api/apps/shop/cors"
	code, body = apiReq(t, env, http.MethodGet, cors, nil)
	if code != 200 {
		t.Fatalf("CORS 列表 = %d: %s", code, body)
	}
	code, body = apiReq(t, env, http.MethodPost, cors, []byte(`{"domain":"https://x.example.com"}`))
	if code != 200 {
		t.Fatalf("CORS 添加 = %d: %s", code, body)
	}
}

func TestL5_06_DatabaseSecurity(t *testing.T) {
	env := newEnv(t, &fakeRunner{mode: "ok"})
	_, _ = env.deploy(t, fixture("hello.zip"), "shop", "a")
	base := "/api/apps/shop/databases/main/tables"

	badNames := []string{"users; DROP", "a-b", "1abc", `x"y`}
	for _, name := range badNames {
		body := []byte(`{"name":"` + name + `","fields":[{"name":"f","type":"TEXT"}]}`)
		code, _ := apiReq(t, env, http.MethodPost, base, body)
		if code != 400 {
			t.Fatalf("非法表名 %q 未被拒绝: %d", name, code)
		}
	}
	body := []byte(`{"name":"good","fields":[{"name":"f","type":"TEXT; DROP"}]}`)
	code, _ := apiReq(t, env, http.MethodPost, base, body)
	if code != 400 {
		t.Fatalf("非法字段类型未被拒绝: %d", code)
	}
	body = []byte(`{"name":"good","fields":[{"name":"f","type":"TEXT"},{"name":"f","type":"INTEGER"}]}`)
	code, _ = apiReq(t, env, http.MethodPost, base, body)
	if code != 400 {
		t.Fatalf("重复字段未被拒绝: %d", code)
	}
}

func TestL5_07_DBPathInjected(t *testing.T) {
	runner := &fakeRunner{mode: "ok"}
	env := newEnv(t, runner)
	_, _ = env.deploy(t, fixture("hello.zip"), "shop", "a")
	found := false
	for _, e := range runner.lastEnv {
		if strings.HasPrefix(e, "MINIBASE_DB_PATH=") && strings.Contains(e, "databases/shop/") && strings.HasSuffix(e, "main.db") {
			found = true
		}
	}
	if !found {
		t.Fatalf("应用未收到 MINIBASE_DB_PATH: %v", runner.lastEnv)
	}
}

func TestL5_08_DatabaseCreateDrop(t *testing.T) {
	env := newEnv(t, &fakeRunner{mode: "ok"})
	_, _ = env.deploy(t, fixture("hello.zip"), "shop", "a")

	d, _ := env.store.GetDefaultDatabase("shop")
	if d == nil || d.Name != "main" || !d.IsDefault {
		t.Fatalf("默认库异常: %+v", d)
	}
	base := "/api/apps/shop/databases"
	code, body := apiReq(t, env, http.MethodPost, base, []byte(`{"name":"analytics"}`))
	if code != 200 {
		t.Fatalf("新建库 = %d: %s", code, body)
	}
	if _, err := os.Stat(env.store.DatabasePath("shop", "analytics")); err != nil {
		t.Fatal("库文件未创建")
	}
	code, body = apiReq(t, env, http.MethodGet, base, nil)
	if code != 200 || !strings.Contains(string(body), "analytics") || !strings.Contains(string(body), "main") {
		t.Fatalf("库列表 = %d: %s", code, body)
	}
	code, _ = apiReq(t, env, http.MethodPost, base, []byte(`{"name":"analytics"}`))
	if code != 400 {
		t.Fatalf("重复创建应 400，实际 %d", code)
	}
	code, body = apiReq(t, env, http.MethodDelete, base+"/main", nil)
	if code != 400 || !strings.Contains(string(body), "默认库") {
		t.Fatalf("默认库应不可删: %d %s", code, body)
	}
	code, _ = apiReq(t, env, http.MethodDelete, base+"/analytics", nil)
	if code != 200 {
		t.Fatalf("删库 = %d", code)
	}
	if _, err := os.Stat(env.store.DatabasePath("shop", "analytics")); !os.IsNotExist(err) {
		t.Fatal("库文件未删除")
	}
}
