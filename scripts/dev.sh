#!/bin/sh
# 本地开发一键启动：后端 air 热重载 + 前端 Vite 热更新。
# 用法: ./scripts/dev.sh
# 依赖: Go 1.26+、Node 22+（air 通过 go run 免安装运行）
set -eu

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT_DIR"

# 检查依赖
command -v go >/dev/null 2>&1 || { echo "错误：需要 Go，请安装 Go 1.26+"; exit 1; }
command -v npm >/dev/null 2>&1 || { echo "错误：需要 Node.js，请安装 Node 22+"; exit 1; }

# 首次运行装前端依赖（装在项目内，不污染宿主）
if [ ! -d web/node_modules ]; then
  echo "[dev] 首次运行，安装前端依赖..."
  (cd web && npm ci)
fi

# 本地配置文件不存在时提示
if [ ! -f config.json ]; then
  echo "错误：缺少 config.json。请从服务器复制一份到本地，或参考 README 创建。" >&2
  exit 1
fi

# 捕获 Ctrl+C，同时停掉前后端
PIDS=""
cleanup() {
  echo "\n[dev] 正在停止..."
  for pid in $PIDS; do kill "$pid" 2>/dev/null || true; done
  wait 2>/dev/null || true
  echo "[dev] 已停止"
}
trap cleanup INT TERM

echo "[dev] 启动后端热重载 (air) ..."
go run github.com/air-verse/air@latest -c .air.toml &
PIDS="$PIDS $!"

echo "[dev] 启动前端热更新 (Vite) ..."
(cd web && npm run dev) &
PIDS="$PIDS $!"

echo ""
echo "============================================"
echo "  本地开发环境已启动"
echo "  前端页面: http://localhost:5173"
echo "  后端 API: http://localhost:8080"
echo "  改 .go 文件 → 后端自动重启"
echo "  改前端文件 → 浏览器自动刷新"
echo "  按 Ctrl+C 停止"
echo "============================================"

wait
