# MiniBase

一个跑在 1C1G 小机器上的最小应用托管平台。用户上传 Node.js 项目包，平台启动进程、分配端口、转发流量。

## 它做什么

- 上传 zip 项目包，自动部署成常驻服务
- 通过 `/apps/<项目ID>/<应用名>/...` 访问应用
- 应用能读写数据库、存文件、配环境变量和密钥
- 卡死能发现、部署失败能回滚、Core 强杀能对账
- systemd 管理，开机自启，崩溃重启

## 它不做什么（边界）

- **不做备份**。数据自己负责，平台不提供任何恢复保证。
- 不做域名和 HTTPS。只用 IP + 路径访问；要 HTTPS 请自己在外面挂反代。
- 只支持 Node.js，不做多语言。不做 Go / Python / WASM 运行时。
- 不做完整控制台。只有一个极简状态页。
- 不做外部对象存储（S3）、任务队列、SDK。

## 构建

```sh
go build -o bin/minicore ./cmd/minicore
go build -o bin/minibase ./cmd/minibase
```

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
minibase deploy hello.zip --app shop --project proj_a --mode a

# 访问
curl http://服务器IP:8080/apps/proj_a/shop/api/users

# 查状态 / 日志
minibase status shop
minibase logs shop

# 回滚到上一个版本
minibase rollback proj_a/shop

# 数据库 CRUD
minibase data proj_a users post '{"name":"Alice"}'
minibase data proj_a users get

# 对象存储
minibase storage upload proj_a shop ./photo.jpg
minibase storage list proj_a shop

# 环境变量 / 密钥
minibase env set proj_a/shop NODE_ENV=production
minibase env set proj_a/shop API_KEY=secret --secret
minibase env list proj_a/shop

# 下线应用（回收端口）
minibase down proj_a/shop
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

Apifox 可以直接导入这个文件：

```sh
apifox import --project <项目ID> --format openapi --file api/openapi.yaml
```

本地项目已经使用的 Apifox 项目 ID：`9079556`。

导入后的接口分组按 OpenAPI `tags` 生成：运维、部署、应用管理、日志与审计、数据库、对象存储。
所有 `/api/...` 接口使用 `Authorization: Bearer <MINIBASE_TOKEN>`。

## 测试

```sh
go test ./...    # 所有逻辑测试
```

测试环境约定：本机只跑 `go test`；需要启动 Core 的测试全在测试服务器做；不在宿主机装测试环境、不造数据目录。

## 文档

以下文档保存在本地，不提交到公开仓库：

- `docs/OPERATIONS.md`
- `docs/TROUBLESHOOTING.md`
- `docs/00-开发方案.md`
- `docs/ACCEPTANCE-v2.md`
- `docs/baselines/r1-06.md`
- `docs/baselines/r2-01.md`

