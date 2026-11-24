# MiniBase

一个跑在 1C1G 小机器上的最小应用托管平台。用户上传 Node.js 项目包，平台启动进程、分配端口、转发流量。

## 它做什么

- 上传 zip 项目包，自动部署成常驻服务
- 通过 `/apps/<应用名>/...` 访问应用
- 应用能读写数据库（通过注入的库路径自行访问）、存文件、配环境变量和密钥
- 卡死能发现、部署失败能回滚、Core 强杀能对账
- systemd 管理，开机自启，崩溃重启

## 它不做什么（边界）

- **不做备份**。数据自己负责，平台不提供任何恢复保证。
- 支持域名访问：把域名 A 记录指到设备 IP，和 IP 一样走 `/apps/...` 路径。
- 不做基于 Host 头的子域名路由，Core 内不做 HTTPS；要 HTTPS 请在外面挂反代。
- 只支持 Node.js，不做多语言。不做 Go / Python / WASM 运行时。
- 不做完整控制台。只有一个极简状态页。
- 不做外部对象存储（S3）、任务队列、SDK。

## 构建

```sh
go build -o bin/minicore ./cmd/minicore
go build -o bin/minibase ./cmd/minibase
```

## 本地开发（热重载）

改代码后想立刻看到效果，用一条命令启动前后端热重载：

```sh
./scripts/dev.sh
```

启动后：

- 前端页面：http://localhost:5173（改 `.svelte` / `.ts` 文件，浏览器自动刷新）
- 后端 API：http://localhost:8080（改 `.go` 文件，1-2 秒自动重新编译并重启）
- 按 `Ctrl+C` 停止，临时文件自动清理

前提：本机装了 Go 1.26+ 和 Node.js 22+。air 热重载工具通过 `go run` 免安装运行，不往系统装任何东西。本地开发依赖装在项目目录里（`web/node_modules/`），配置和数据在项目的 `config.json` 和 `data/`，和服务器完全隔离。

首次运行会自动装前端依赖。如果本地没有 `config.json`，需要自己准备一份（可参考服务器上的格式，注意 `master_key` 和 `data_dir` 要配对）。

## 安装到服务器

前提：目标机是 Linux + systemd，已装 Node.js（安装脚本会自动装）。

本机一条命令：

```sh
./scripts/deploy.sh root@服务器IP
# 指定 Token
./scripts/deploy.sh root@服务器IP --token my-secret
# SSH 密钥免密
MINIBASE_SSH_OPTS="-i ~/.ssh/id_ed25519" ./scripts/deploy.sh root@服务器IP
```

安装做这些事：编译 → 上传 → 目标机装 Node → 生成 Token 和 master_key → 建用户和目录 → 写 systemd 单元 → 启动并验证。结束时打印 Token、主密钥和访问地址。**主密钥要备份，丢了密钥解不开。**

目标机已有二进制时，单独安装：

```sh
sudo ./scripts/install.sh --binary /path/to/minicore --cli-binary /path/to/minibase
```

## 日常使用

```sh
export MINIBASE_TOKEN=安装时打印的Token

# 部署应用（mode a=包里带 node_modules，mode b=平台 npm install）
minibase deploy hello.zip --app shop --mode a

# 访问
curl http://服务器IP:8080/apps/shop/api/users

# 查状态 / 日志
minibase status shop
minibase logs shop

# 回滚到上一个版本
minibase rollback shop

# 数据库管控（建库、建表、索引、删表、删库）
minibase db create-db shop analytics
minibase db tables shop
minibase db create shop users '[{"name":"name","type":"TEXT"}]'
minibase db index-add shop users idx_name name

# 对象存储
minibase storage upload shop ./photo.jpg
minibase storage list shop

# 环境变量 / 密钥
minibase env set shop NODE_ENV=production
minibase env set shop API_KEY=secret --secret
minibase env list shop

# 暂停应用（保留数据）与彻底删除
minibase pause shop
minibase rm shop --yes

# 重启、编辑、版本历史
minibase restart shop
minibase edit shop memory_limit_mb=128
minibase versions shop

# 资源用量、CORS、Token 轮换
minibase usage
minibase cors add shop https://admin.example.com
minibase token rotate
```

配置改动下次部署生效，不重启当前版本。详细命令见本地文档 `docs/OPERATIONS.md`（该目录不提交到公开仓库）。

## 状态页

浏览器打开 `http://服务器IP:8080/status`，输入管理员 Token，能看应用列表、状态和最近审计。

## 升级 Core

应用是独立进程，升级 Core 不中断应用：

```sh
./scripts/upgrade-remote.sh root@服务器IP
```

升级脚本会备份旧二进制，探活失败自动回滚，不碰配置、数据目录和 master_key。详见本地文档 `docs/OPERATIONS.md`（该目录不提交到公开仓库）。

## 卸载

服务器本机执行：

```sh
sudo ./scripts/uninstall.sh
```

默认只卸载服务、systemd 单元和二进制，保留配置和数据。配置里有 `master_key`，数据和它必须一起留着。重装时要带原配置：

```sh
sudo ./scripts/install.sh --binary /path/to/minicore --config /etc/minibase/config.json
```

从本机一条命令卸载：

```sh
ssh root@服务器IP 'bash -s' < scripts/uninstall.sh
```

连配置、数据、用户一起删，需要显式加参数。这个操作不可恢复：

```sh
ssh root@服务器IP 'bash -s -- --purge-data' < scripts/uninstall.sh
# 自动化时跳过确认
ssh root@服务器IP 'bash -s -- --purge-data --yes' < scripts/uninstall.sh
```

## Apifox 接口文档

管控 API 的 OpenAPI 3.0 文件在 `api/openapi.yaml`。

Apifox 可以直接导入这个文件。先转成 JSON 再导入——CLI 2.2.12 直接读 YAML 会报 `Invalid format`：

```sh
ruby -ryaml -rjson -e 'File.write("/tmp/minibase-openapi.json", JSON.generate(YAML.load_file("api/openapi.yaml")))'
apifox import --project 9079556 --branch main --format openapi --file /tmp/minibase-openapi.json \
  --module-map "MiniBase 管控 API=8944573" --overwrite-mode overwrite
```

本地项目已经使用的 Apifox 项目 ID：`9079556`。  
OpenAPI 模块 ID：`8944573`。重复导入时指定模块 ID，避免生成重复接口。

导入结果会显示 `createCount`（新增）和 `updateCount`（更新）。若只新增了新接口、旧接口 `ignoreCount` 为 0，说明旧接口内容没变，这是正常的。导入后用 `apifox endpoint list --project 9079556 --branch main --page 1 --page-size 500` 回读，确认没有重复接口。

导入后的接口分组按 OpenAPI `tags` 生成：系统、应用管理、数据库（表/索引管理）、文件存储、安全。

管控 API 路径规则：**应用是顶层资源，没有项目层**。资源挂在应用下，HTTP 方法表达动作，命令用 `/actions/` 后缀。例如：

```
/api/system/usage                              系统用量
/api/apps                                      应用列表 / 部署
/api/apps/shop                                 应用详情 / 编辑 / 删除
/api/apps/shop/actions/pause|resume|restart    暂停/恢复/重启
/api/apps/shop/databases                       应用的库列表 / 新建
/api/apps/shop/databases/main/tables           库的表管理
/api/apps/shop/files                           文件列表 / 上传
/api/apps/shop/cors                            CORS 白名单
```

旧路径（如 `/api/deploy`、`/api/apps-list`、`/api/usage`）已从 OpenAPI 和 Apifox 中删除，只保留新路径。CLI 已全部切到新路径。
所有 `/api/...` 接口使用 `Authorization: Bearer <MINIBASE_TOKEN>`。API 路径体系的梳理记录见 `docs/08-管控面补齐方案.md`。

## 测试

```sh
go test ./...    # 所有逻辑测试
```

测试环境约定：本机只跑 `go test`；需要启动 Core 的测试全在测试服务器做；不在宿主机装测试环境、不造数据目录。

## 文档

以下文档保存在本地，不提交到公开仓库：

- `docs/OPERATIONS.md`
- `docs/08-管控面补齐方案.md`
- `docs/TROUBLESHOOTING.md`
- `docs/00-开发方案.md`
- `docs/ACCEPTANCE-v2.md`
- `docs/baselines/r1-06.md`
- `docs/baselines/r2-01.md`


<!-- auto-deploy verification: 1795494414 -->
