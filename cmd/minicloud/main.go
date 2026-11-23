// minicloud 是 HTTP API 的命令行客户端，本身不含业务逻辑。
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const notice = "提示：第一版不备份，数据自己负责。"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]

	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	server := fs.String("server", envOr("MINICLOUD_SERVER", "http://127.0.0.1:8080"), "Core 地址")
	token := fs.String("token", os.Getenv("MINICLOUD_TOKEN"), "管理员 Token")
	app := fs.String("app", "", "应用名")
	project := fs.String("project", "", "项目 ID")
	mode := fs.String("mode", "b", "依赖模式：a=完整包，b=代码包")
	secretFlag := fs.Bool("secret", false, "env set 时标记为密钥")
	_ = fs.Parse(normalizeArgs(args))

	if *token == "" {
		fmt.Fprintln(os.Stderr, "错误：请设置环境变量 MINICLOUD_TOKEN 或用 -token 指定")
		os.Exit(2)
	}

	switch cmd {
	case "deploy":
		if fs.NArg() < 1 {
			fmt.Fprintln(os.Stderr, "用法: minicloud deploy <zip> [-app 名] [-project ID] [-mode a|b]")
			os.Exit(2)
		}
		deployCmd(*server, *token, fs.Arg(0), *app, *project, *mode)
	case "status":
		if fs.NArg() < 1 {
			fmt.Fprintln(os.Stderr, "用法: minicloud status <应用名>")
			os.Exit(2)
		}
		getCmd(*server, *token, "/api/status/"+fs.Arg(0))
	case "logs":
		if fs.NArg() < 1 {
			fmt.Fprintln(os.Stderr, "用法: minicloud logs <应用名>")
			os.Exit(2)
		}
		getCmd(*server, *token, "/api/logs/"+fs.Arg(0))
	case "rollback":
		if fs.NArg() < 1 {
			fmt.Fprintln(os.Stderr, "用法: minicloud rollback <项目ID>/<应用名>")
			os.Exit(2)
		}
		parts := strings.SplitN(fs.Arg(0), "/", 2)
		if len(parts) != 2 {
			fmt.Fprintln(os.Stderr, "格式应为 项目ID/应用名")
			os.Exit(2)
		}
		postCmd(*server, *token, "/api/apps/"+parts[0]+"/"+parts[1]+"/rollback")
	case "data":
		// data <项目ID> <表名> <get|post|put|del> [ID] [JSON]
		if fs.NArg() < 3 {
			fmt.Fprintln(os.Stderr, "用法: minicloud data <项目ID> <表名> <get|post|put|del> [ID] [JSON]")
			os.Exit(2)
		}
		handleDataCmd(*server, *token, fs.Args())
	case "env":
		// env set <项目ID>/<应用名> KEY=VALUE ... / env list <项目ID>/<应用名>
		if fs.NArg() < 2 {
			fmt.Fprintln(os.Stderr, "用法: minicloud env set <项目ID>/<应用名> KEY=VALUE... | env list <项目ID>/<应用名>")
			os.Exit(2)
		}
		handleEnvCmd(*server, *token, fs.Args(), *secretFlag)
	case "down":
		if fs.NArg() < 1 {
			fmt.Fprintln(os.Stderr, "用法: minicloud down <项目ID>/<应用名>")
			os.Exit(2)
		}
		parts := strings.SplitN(fs.Arg(0), "/", 2)
		if len(parts) != 2 {
			fmt.Fprintln(os.Stderr, "格式应为 项目ID/应用名")
			os.Exit(2)
		}
		req, _ := http.NewRequest(http.MethodDelete, *server+"/api/apps/"+parts[0]+"/"+parts[1], nil)
		fmt.Println(string(do(req, *token)))
	case "storage":
		// storage <upload|download|list|delete> <项目ID> <应用名> [文件路径|文件ID]
		if fs.NArg() < 3 {
			fmt.Fprintln(os.Stderr, "用法: minicloud storage <upload|download|list|delete> <项目ID> <应用名> [路径或ID]")
			os.Exit(2)
		}
		handleStorageCmd(*server, *token, fs.Args())
	default:
		usage()
		os.Exit(2)
	}
}

// normalizeArgs 把混在位置参数后面的 flag 挪到前面，
// 这样 deploy hello.zip -mode a 和 deploy -mode a hello.zip 都能用。
func normalizeArgs(args []string) []string {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if len(a) > 0 && a[0] == '-' {
			flags = append(flags, a)
			if !strings.Contains(a, "=") && i+1 < len(args) && (len(args[i+1]) == 0 || args[i+1][0] != '-') {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		positional = append(positional, a)
	}
	return append(flags, positional...)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func usage() {
	fmt.Fprintln(os.Stderr, `用法:
  minicloud deploy <zip> [-app 名] [-project ID] [-mode a|b]
  minicloud status <应用名>
  minicloud logs <应用名>`)
}

func do(req *http.Request, token string) []byte {
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "请求失败: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		fmt.Fprintf(os.Stderr, "服务返回 %d: %s\n", resp.StatusCode, string(body))
		os.Exit(1)
	}
	return body
}

func deployCmd(server, token, zipPath, app, project, mode string) {
	f, err := os.Open(zipPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "打开 zip 失败: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", filepath.Base(zipPath))
	if err != nil {
		fmt.Fprintf(os.Stderr, "构造上传表单失败: %v\n", err)
		os.Exit(1)
	}
	if _, err := io.Copy(part, f); err != nil {
		fmt.Fprintf(os.Stderr, "读取 zip 失败: %v\n", err)
		os.Exit(1)
	}
	_ = w.WriteField("app", app)
	_ = w.WriteField("project", project)
	_ = w.WriteField("mode", mode)
	_ = w.Close()

	req, _ := http.NewRequest(http.MethodPost, server+"/api/deploy", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())

	body := do(req, token)
	var out struct {
		AccessURL string `json:"access_url"`
		Version   string `json:"version"`
		Notice    string `json:"notice"`
	}
	_ = json.Unmarshal(body, &out)
	fmt.Printf("部署成功\n版本: %s\n访问地址: %s\n%s\n", out.Version, out.AccessURL, notice)
	if out.Notice != "" {
		fmt.Printf("服务端提示: %s\n", out.Notice)
	}
}

func getCmd(server, token, path string) {
	req, _ := http.NewRequest(http.MethodGet, server+path, nil)
	body := do(req, token)
	var pretty bytes.Buffer
	if json.Indent(&pretty, body, "", "  ") == nil {
		fmt.Println(pretty.String())
	} else {
		fmt.Println(string(body))
	}
	fmt.Println(notice)
}

func postCmd(server, token, path string) {
	req, _ := http.NewRequest(http.MethodPost, server+path, nil)
	body := do(req, token)
	var pretty bytes.Buffer
	if json.Indent(&pretty, body, "", "  ") == nil {
		fmt.Println(pretty.String())
	} else {
		fmt.Println(string(body))
	}
}

func handleDataCmd(server, token string, args []string) {
	project, table, action := args[0], args[1], args[2]
	path := "/api/data/" + project + "/" + table
	switch action {
	case "get":
		if len(args) > 3 {
			path += "/" + args[3]
		}
		getCmd(server, token, path)
	case "post":
		if len(args) < 4 {
			fmt.Fprintln(os.Stderr, "post 需要 JSON 参数")
			os.Exit(2)
		}
		bodyReq, _ := http.NewRequest(http.MethodPost, server+path, strings.NewReader(args[3]))
		bodyReq.Header.Set("Content-Type", "application/json")
		fmt.Println(string(do(bodyReq, token)))
	case "put":
		if len(args) < 5 {
			fmt.Fprintln(os.Stderr, "put 需要 ID 和 JSON")
			os.Exit(2)
		}
		path += "/" + args[3]
		req, _ := http.NewRequest(http.MethodPut, server+path, strings.NewReader(args[4]))
		req.Header.Set("Content-Type", "application/json")
		fmt.Println(string(do(req, token)))
	case "del":
		if len(args) < 4 {
			fmt.Fprintln(os.Stderr, "del 需要 ID")
			os.Exit(2)
		}
		path += "/" + args[3]
		req, _ := http.NewRequest(http.MethodDelete, server+path, nil)
		fmt.Println(string(do(req, token)))
	default:
		fmt.Fprintln(os.Stderr, "未知动作:", action)
		os.Exit(2)
	}
}

func handleStorageCmd(server, token string, args []string) {
	action, project, app := args[0], args[1], args[2]
	switch action {
	case "upload":
		if len(args) < 4 {
			fmt.Fprintln(os.Stderr, "upload 需要文件路径")
			os.Exit(2)
		}
		uploadStorage(server, token, project, app, args[3])
	case "download":
		if len(args) < 4 {
			fmt.Fprintln(os.Stderr, "download 需要文件 ID")
			os.Exit(2)
		}
		getCmd(server, token, "/api/storage/download/"+project+"/"+app+"/"+args[3])
	case "list":
		getCmd(server, token, "/api/storage/list/"+project+"/"+app)
	case "delete":
		if len(args) < 4 {
			fmt.Fprintln(os.Stderr, "delete 需要文件 ID")
			os.Exit(2)
		}
		req, _ := http.NewRequest(http.MethodDelete, server+"/api/storage/delete/"+project+"/"+app+"/"+args[3], nil)
		fmt.Println(string(do(req, token)))
	default:
		fmt.Fprintln(os.Stderr, "未知动作:", action)
		os.Exit(2)
	}
}

func uploadStorage(server, token, project, app, filePath string) {
	f, err := os.Open(filePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer f.Close()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, _ := w.CreateFormFile("file", filepath.Base(filePath))
	_, _ = io.Copy(part, f)
	_ = w.Close()
	req, _ := http.NewRequest(http.MethodPost, server+"/api/storage/upload/"+project+"/"+app, &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	fmt.Println(string(do(req, token)))
}

func handleEnvCmd(server, token string, args []string, secretFlag bool) {
	parts := strings.SplitN(args[1], "/", 2)
	if len(parts) != 2 {
		fmt.Fprintln(os.Stderr, "格式应为 项目ID/应用名")
		os.Exit(2)
	}
	path := "/api/apps/" + parts[0] + "/" + parts[1] + "/env"
	if args[0] == "set" {
		asSecret := false
		rest := []string{}
		for _, a := range args[2:] {
			if a == "--secret" {
				asSecret = true
				continue
			}
			rest = append(rest, a)
		}
		if len(rest) < 1 {
			fmt.Fprintln(os.Stderr, "set 需要 KEY=VALUE")
			os.Exit(2)
		}
		body := map[string]any{}
		secretKeys := []string{}
		for _, kv := range rest {
			p := strings.SplitN(kv, "=", 2)
			if len(p) != 2 {
				fmt.Fprintln(os.Stderr, "格式错误:", kv)
				os.Exit(2)
			}
			body[p[0]] = p[1]
			if asSecret {
				secretKeys = append(secretKeys, p[0])
			}
		}
		if len(secretKeys) > 0 {
			body["_secret"] = secretKeys
		}
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest(http.MethodPost, server+path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		fmt.Println(string(do(req, token)))
	} else if args[0] == "list" {
		getCmd(server, token, path)
	} else {
		fmt.Fprintln(os.Stderr, "未知 env 动作:", args[0])
		os.Exit(2)
	}
}
