// minibase 是 HTTP API 的命令行客户端，本身不含业务逻辑。
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
	server := fs.String("server", envOr("MINIBASE_SERVER", "http://127.0.0.1:8080"), "Core 地址")
	token := fs.String("token", os.Getenv("MINIBASE_TOKEN"), "管理员 Token")
	app := fs.String("app", "", "应用名")
	mode := fs.String("mode", "b", "依赖模式：a=完整包，b=代码包")
	secretFlag := fs.Bool("secret", false, "env set 时标记为密钥")
	_ = fs.Parse(normalizeArgs(args))

	if *token == "" {
		fmt.Fprintln(os.Stderr, "错误：请设置环境变量 MINIBASE_TOKEN 或用 -token 指定")
		os.Exit(2)
	}

	switch cmd {
	case "deploy":
		if fs.NArg() < 1 {
			fmt.Fprintln(os.Stderr, "用法: minibase deploy <zip> [-app 名] [-mode a|b]")
			os.Exit(2)
		}
		deployCmd(*server, *token, fs.Arg(0), *app, *mode)
	case "status":
		if fs.NArg() < 1 {
			fmt.Fprintln(os.Stderr, "用法: minibase status <应用名>")
			os.Exit(2)
		}
		getCmd(*server, *token, "/api/status/"+fs.Arg(0))
	case "logs":
		if fs.NArg() < 1 {
			fmt.Fprintln(os.Stderr, "用法: minibase logs <应用名>")
			os.Exit(2)
		}
		getCmd(*server, *token, "/api/logs/"+fs.Arg(0))
	case "rollback":
		if fs.NArg() < 1 {
			fmt.Fprintln(os.Stderr, "用法: minibase rollback <应用名>")
			os.Exit(2)
		}
		postCmd(*server, *token, appBase(fs.Arg(0))+"/actions/rollback")
	case "db":
		handleDbCmd(*server, *token, fs.Args())
	case "env":
		// env set <应用名> KEY=VALUE ... / env list <应用名>
		if fs.NArg() < 2 {
			fmt.Fprintln(os.Stderr, "用法: minibase env set <应用名> KEY=VALUE... | env list <应用名>")
			os.Exit(2)
		}
		handleEnvCmd(*server, *token, fs.Args(), *secretFlag)
	case "usage":
		if fs.NArg() == 0 {
			getCmd(*server, *token, "/api/system/usage")
		} else {
			getCmd(*server, *token, appBase(fs.Arg(0))+"/usage")
		}
	case "resume":
		appActionCmd(*server, *token, fs.Args(), "/actions/resume")
	case "restart":
		appActionCmd(*server, *token, fs.Args(), "/actions/restart")
	case "pause":
		appActionCmd(*server, *token, fs.Args(), "/actions/pause")
	case "versions":
		appActionCmd(*server, *token, fs.Args(), "/versions")
	case "deployments":
		appActionCmd(*server, *token, fs.Args(), "/deployments")
	case "edit":
		if fs.NArg() < 2 {
			fmt.Fprintln(os.Stderr, "用法: minibase edit <应用名> KEY=VALUE... (需先 pause)")
			os.Exit(2)
		}
		editCmd(*server, *token, fs.Arg(0), fs.Args()[1:])
	case "rm":
		if fs.NArg() < 1 {
			fmt.Fprintln(os.Stderr, "用法: minibase rm <应用名> [--yes]")
			os.Exit(2)
		}
		rmCmd(*server, *token, fs.Arg(0), fs.Args()[1:])
	case "cors":
		handleCorsCmd(*server, *token, fs.Args())
	case "token":
		if fs.NArg() < 1 || fs.Arg(0) != "rotate" {
			fmt.Fprintln(os.Stderr, "用法: minibase token rotate")
			os.Exit(2)
		}
		postCmd(*server, *token, "/api/system/token/rotate")

	case "down":
		if fs.NArg() < 1 {
			fmt.Fprintln(os.Stderr, "用法: minibase down <应用名>")
			os.Exit(2)
		}
		req, _ := http.NewRequest(http.MethodDelete, *server+appBase(fs.Arg(0)), nil)
		fmt.Println(string(do(req, *token)))
	case "storage":
		// storage <upload|download|list|delete> <应用名> [文件路径|文件ID]
		if fs.NArg() < 2 {
			fmt.Fprintln(os.Stderr, "用法: minibase storage <upload|download|list|delete> <应用名> [路径或ID]")
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
  minibase deploy <zip> [-app 名] [-mode a|b]
  minibase status <应用名>
  minibase logs <应用名>
  minibase usage [应用名]
  minibase resume|restart|pause <应用名>
  minibase edit <应用名> KEY=VALUE...
  minibase rm <应用名> [--yes]
  minibase versions|deployments <应用名>
  minibase cors add|list|rm <应用名> [域名|ID]
  minibase token rotate`)
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

func deployCmd(server, token, zipPath, app, mode string) {
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

func handleStorageCmd(server, token string, args []string) {
	action, app := args[0], args[1]
	switch action {
	case "upload":
		if len(args) < 4 {
			fmt.Fprintln(os.Stderr, "upload 需要文件路径")
			os.Exit(2)
		}
		uploadStorage(server, token, app, args[2])
	case "download":
		if len(args) < 4 {
			fmt.Fprintln(os.Stderr, "download 需要文件 ID")
			os.Exit(2)
		}
		getCmd(server, token, appBase(app)+"/files/"+args[2])
	case "list":
		getCmd(server, token, appBase(app)+"/files")
	case "delete":
		if len(args) < 4 {
			fmt.Fprintln(os.Stderr, "delete 需要文件 ID")
			os.Exit(2)
		}
		req, _ := http.NewRequest(http.MethodDelete, server+appBase(app)+"/files/"+args[2], nil)
		fmt.Println(string(do(req, token)))
	default:
		fmt.Fprintln(os.Stderr, "未知动作:", action)
		os.Exit(2)
	}
}

func uploadStorage(server, token, app, filePath string) {
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
	req, _ := http.NewRequest(http.MethodPost, server+appBase(app)+"/files", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	fmt.Println(string(do(req, token)))
}

func handleEnvCmd(server, token string, args []string, secretFlag bool) {
	app := args[1]
	path := appBase(app) + "/env"
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

func appActionCmd(server, token string, args []string, suffix string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "用法: minibase resume|restart|pause|versions|deployments <应用名>")
		os.Exit(2)
	}
	app := args[0]
	if suffix == "/versions" || suffix == "/deployments" {
		getCmd(server, token, appBase(app)+suffix)
	} else {
		postCmd(server, token, appBase(app)+suffix)
	}
}

func editCmd(server, token, target string, kv []string) {
	app := target
	body := map[string]any{}
	for _, item := range kv {
		p := strings.SplitN(item, "=", 2)
		if len(p) != 2 {
			fmt.Fprintln(os.Stderr, "格式错误:", item)
			os.Exit(2)
		}
		switch p[0] {
		case "memory_limit_mb", "cpu_quota_percent", "health_timeout_ms":
			var n int
			if _, err := fmt.Sscanf(p[1], "%d", &n); err != nil {
				fmt.Fprintln(os.Stderr, "需要整数:", item)
				os.Exit(2)
			}
			body[p[0]] = n
		default:
			body[p[0]] = p[1]
		}
	}
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPut, server+appBase(app), bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	fmt.Println(string(do(req, token)))
}

func rmCmd(server, token, target string, args []string) {
	app := target
	yes := false
	for _, a := range args {
		if a == "--yes" || a == "-yes" {
			yes = true
		}
	}
	if !yes {
		fmt.Fprintf(os.Stderr, "将彻底删除 %s，包括版本、数据和文件。确认请加 --yes: ", target)
		var line string
		_, _ = fmt.Scanln(&line)
		if strings.ToLower(line) != "y" && strings.ToLower(line) != "yes" {
			fmt.Println("已取消")
			return
		}
	}
	req, _ := http.NewRequest(http.MethodDelete, server+appBase(app), nil)
	fmt.Println(string(do(req, token)))
}

func handleCorsCmd(server, token string, args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "用法: minibase cors add|list|rm <应用名> [域名|ID]")
		os.Exit(2)
	}
	action, app := args[0], args[1]
	path := appBase(app) + "/cors"
	switch action {
	case "list":
		getCmd(server, token, path)
	case "add":
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "add 需要域名")
			os.Exit(2)
		}
		b, _ := json.Marshal(map[string]string{"domain": args[2]})
		req, _ := http.NewRequest(http.MethodPost, server+path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		fmt.Println(string(do(req, token)))
	case "rm":
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "rm 需要 ID")
			os.Exit(2)
		}
		req, _ := http.NewRequest(http.MethodDelete, server+path+"/"+args[2], nil)
		fmt.Println(string(do(req, token)))
	default:
		fmt.Fprintln(os.Stderr, "未知动作:", action)
		os.Exit(2)
	}
}

// appBase 返回应用的资源路径。
func appBase(app string) string { return "/api/apps/" + app }

func handleDbCmd(server, token string, args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, `用法:
	minibase db create-db <应用名> <库名>
	minibase db drop-db <应用名> <库名> [--yes]
	minibase db info <应用名>
	minibase db tables <应用名>
	minibase db create <应用名> <表名> <字段JSON数组>
	minibase db table <应用名> <表名>
	minibase db rename <应用名> <表名> <新表名>
	minibase db drop <应用名> <表名> [--yes]
	minibase db index-add <应用名> <表名> <索引名> <字段,逗号分隔> [--unique]
	minibase db index-drop <应用名> <表名> <索引名>`)
		os.Exit(2)
	}
	action, app := args[0], args[1]
	base := appBase(app) + "/databases"

	switch action {
	case "create-db":
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "create-db 需要库名")
			os.Exit(2)
		}
		b, _ := json.Marshal(map[string]string{"name": args[2]})
		req, _ := http.NewRequest(http.MethodPost, server+base, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		fmt.Println(string(do(req, token)))
	case "drop-db":
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "drop-db 需要库名")
			os.Exit(2)
		}
		if !yesFlag(args[3:]) {
			fmt.Fprintf(os.Stderr, "将删除应用 %s 的库和全部数据。确认请加 --yes: ", app)
			var line string
			_, _ = fmt.Scanln(&line)
			if strings.ToLower(line) != "y" && strings.ToLower(line) != "yes" {
				fmt.Println("已取消")
				return
			}
		}
		req, _ := http.NewRequest(http.MethodDelete, server+base+"/"+args[2], nil)
		fmt.Println(string(do(req, token)))
	case "info":
		getCmd(server, token, base+"/main")
	case "tables":
		getCmd(server, token, base+"/main/tables")
	case "create":
		if len(args) < 4 {
			fmt.Fprintln(os.Stderr, "create 需要表名和字段 JSON")
			os.Exit(2)
		}
		var fields []map[string]any
		if err := json.Unmarshal([]byte(args[3]), &fields); err != nil {
			fmt.Fprintln(os.Stderr, "字段 JSON 解析失败:", err)
			os.Exit(2)
		}
		b, _ := json.Marshal(map[string]any{"name": args[2], "fields": fields})
		bodyReq, _ := http.NewRequest(http.MethodPost, server+base+"/main/tables", bytes.NewReader(b))
		bodyReq.Header.Set("Content-Type", "application/json")
		fmt.Println(string(do(bodyReq, token)))
	case "table":
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "table 需要表名")
			os.Exit(2)
		}
		getCmd(server, token, base+"/main/tables/"+args[2])
	case "rename":
		if len(args) < 4 {
			fmt.Fprintln(os.Stderr, "rename 需要表名和新表名")
			os.Exit(2)
		}
		b, _ := json.Marshal(map[string]string{"name": args[3]})
		req, _ := http.NewRequest(http.MethodPut, server+base+"/main/tables/"+args[2], bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		fmt.Println(string(do(req, token)))
	case "drop":
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "drop 需要表名")
			os.Exit(2)
		}
		if !yesFlag(args[3:]) {
			fmt.Fprintf(os.Stderr, "将删除表 %s 及全部数据。确认请加 --yes: ", args[2])
			var line string
			_, _ = fmt.Scanln(&line)
			if strings.ToLower(line) != "y" && strings.ToLower(line) != "yes" {
				fmt.Println("已取消")
				return
			}
		}
		req, _ := http.NewRequest(http.MethodDelete, server+base+"/main/tables/"+args[2], nil)
		fmt.Println(string(do(req, token)))
	case "index-add":
		if len(args) < 5 {
			fmt.Fprintln(os.Stderr, "index-add 需要表名、索引名和字段")
			os.Exit(2)
		}
		fields := strings.Split(args[4], ",")
		unique := false
		for _, a := range args[5:] {
			if a == "--unique" {
				unique = true
			}
		}
		b, _ := json.Marshal(map[string]any{"name": args[3], "fields": fields, "unique": unique})
		req, _ := http.NewRequest(http.MethodPost, server+base+"/main/tables/"+args[2]+"/indexes", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		fmt.Println(string(do(req, token)))
	case "index-drop":
		if len(args) < 4 {
			fmt.Fprintln(os.Stderr, "index-drop 需要表名和索引名")
			os.Exit(2)
		}
		req, _ := http.NewRequest(http.MethodDelete, server+base+"/main/tables/"+args[2]+"/indexes/"+args[3], nil)
		fmt.Println(string(do(req, token)))
	default:
		fmt.Fprintln(os.Stderr, "未知动作:", action)
		os.Exit(2)
	}
}

func yesFlag(args []string) bool {
	for _, a := range args {
		if a == "--yes" || a == "-yes" {
			return true
		}
	}
	return false
}
