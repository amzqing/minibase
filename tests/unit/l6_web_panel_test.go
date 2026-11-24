package unit

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// L6-01 管控面板依赖的公开 health 路由不需要 Token。
func TestL6_01_PublicSystemHealth(t *testing.T) {
	env := newEnv(t, &fakeRunner{mode: "ok"})
	req, _ := http.NewRequest(http.MethodGet, env.server.URL+"/api/system/health", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"ok"`) {
		t.Fatalf("health = %d %s", resp.StatusCode, body)
	}
}

// L6-02 密钥列表接口返回脱敏值，不泄露密文。
func TestL6_02_ListSecretsMasked(t *testing.T) {
	env := newEnv(t, &fakeRunner{mode: "ok"})
	_, _ = env.deploy(t, fixture("hello.zip"), "shop", "a")

	code, body := apiReq(t, env, http.MethodPost, "/api/apps/shop/secrets", []byte(`{"API_KEY":"secret-value"}`))
	if code != http.StatusOK {
		t.Fatalf("写密钥失败 %d: %s", code, body)
	}

	code, body = apiReq(t, env, http.MethodGet, "/api/apps/shop/secrets", nil)
	if code != http.StatusOK {
		t.Fatalf("列密钥失败 %d: %s", code, body)
	}
	var items []map[string]any
	if err := json.Unmarshal(body, &items); err != nil || len(items) != 1 {
		t.Fatalf("密钥列表格式错误: %s %v", body, err)
	}
	if items[0]["key"] != "API_KEY" || items[0]["value"] != "<encrypted>" || items[0]["is_secret"] != true {
		t.Fatalf("脱敏列表错误: %s", body)
	}
	if strings.Contains(string(body), "secret-value") {
		t.Fatalf("响应泄露明文: %s", body)
	}
}

// L6-03 管控面板路径用应用 ID，列表接口必须返回 id。
func TestL6_03_AppsListHasID(t *testing.T) {
	env := newEnv(t, &fakeRunner{mode: "ok"})
	_, _ = env.deploy(t, fixture("hello.zip"), "shop", "a")

	code, body := apiReq(t, env, http.MethodGet, "/api/apps", nil)
	if code != http.StatusOK {
		t.Fatalf("列应用失败 %d: %s", code, body)
	}
	var items []map[string]any
	if err := json.Unmarshal(body, &items); err != nil || len(items) == 0 {
		t.Fatalf("应用列表格式错误: %s %v", body, err)
	}
	id, ok := items[0]["id"].(string)
	if !ok || id == "" {
		t.Fatalf("应用列表缺少 id: %s", body)
	}

	// 用 ID 访问详情、文件列表、日志，均应成功（旧的应用名路径保持兼容）。
	for _, path := range []string{
		"/api/apps/" + id,
		"/api/apps/" + id + "/files",
		"/api/apps/" + id + "/logs",
	} {
		code, body = apiReq(t, env, http.MethodGet, path, nil)
		if code != http.StatusOK {
			t.Fatalf("ID 路径 %s 失败 %d: %s", path, code, body)
		}
	}
}

// L6-04 没有文件时文件列表返回空数组，不是 null；日志返回结构化 lines。
func TestL6_04_EmptyFilesAndStructuredLogs(t *testing.T) {
	env := newEnv(t, &fakeRunner{mode: "ok"})
	_, _ = env.deploy(t, fixture("hello.zip"), "shop", "a")

	code, body := apiReq(t, env, http.MethodGet, "/api/apps/shop/files", nil)
	if code != http.StatusOK || strings.TrimSpace(string(body)) == "null" {
		t.Fatalf("空文件列表应为 []，得到 %d %s", code, body)
	}
	var files []map[string]any
	if err := json.Unmarshal(body, &files); err != nil || len(files) != 0 {
		t.Fatalf("文件列表格式错误: %s %v", body, err)
	}

	code, body = apiReq(t, env, http.MethodGet, "/api/apps/shop/logs", nil)
	if code != http.StatusOK {
		t.Fatalf("日志接口失败 %d: %s", code, body)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("日志格式错误: %s %v", body, err)
	}
	if _, ok := payload["lines"]; !ok {
		t.Fatalf("日志缺少 lines: %s", body)
	}
}
