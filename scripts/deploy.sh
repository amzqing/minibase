#!/bin/sh
# MiniBase 本机一键部署：编译 → 上传 → 目标机一键安装。
# 用法: ./scripts/deploy.sh root@服务器IP [--token TOKEN] [--listen :8080] [--arch amd64]
set -eu

TARGET="${1:-}"
SSH_OPTS="${MINIBASE_SSH_OPTS:-}"
if [ -z "$TARGET" ] || [ "$TARGET" = "-h" ]; then
  echo "用法: $0 root@服务器IP [--token TOKEN] [--listen :8080] [--arch amd64]" >&2
  exit 2
fi
shift

TOKEN=""
LISTEN=""
ARCH=""
while [ $# -gt 0 ]; do
  case "$1" in
    --token)  TOKEN="$2"; shift 2 ;;
    --listen) LISTEN="$2"; shift 2 ;;
    --arch)   ARCH="$2"; shift 2 ;;
    *) echo "未知参数: $1" >&2; exit 2 ;;
  esac
done

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
BUILD_DIR="$(mktemp -d)"
trap 'rm -rf "$BUILD_DIR"' EXIT

echo "[1/4] 探测目标架构"
if [ -z "$ARCH" ]; then
  MACHINE="$(ssh $SSH_OPTS "$TARGET" 'uname -m' | tr -d '\r')"
  case "$MACHINE" in
    x86_64)  ARCH=amd64 ;;
    aarch64|arm64) ARCH=arm64 ;;
    *) echo "  不支持的架构: $MACHINE" >&2; exit 1 ;;
  esac
fi
echo "  架构: $ARCH"

echo "[2/5] 构建前端"
(cd "$ROOT_DIR/web" && npm ci && npm run build)

echo "[3/5] 交叉编译"
(cd "$ROOT_DIR" && GOOS=linux GOARCH="$ARCH" CGO_ENABLED=0 go build -o "$BUILD_DIR/minicore" ./cmd/minicore)
(cd "$ROOT_DIR" && GOOS=linux GOARCH="$ARCH" CGO_ENABLED=0 go build -o "$BUILD_DIR/minibase" ./cmd/minibase)

echo "[4/5] 上传安装文件"
REMOTE_DIR="/tmp/minibase-install-$$"
ssh $SSH_OPTS "$TARGET" "mkdir -p $REMOTE_DIR"
scp -q $SSH_OPTS "$BUILD_DIR/minicore" "$BUILD_DIR/minibase" \
    "$ROOT_DIR/scripts/install.sh" "$ROOT_DIR/deployments/systemd/minicore.service" \
    "$TARGET:$REMOTE_DIR/"

echo "[5/5] 目标机安装"
REMOTE_CMD="$REMOTE_DIR/install.sh --binary $REMOTE_DIR/minicore --cli-binary $REMOTE_DIR/minibase"
[ -n "$TOKEN" ] && REMOTE_CMD="$REMOTE_CMD --token $TOKEN"
[ -n "$LISTEN" ] && REMOTE_CMD="$REMOTE_CMD --listen $LISTEN"
ssh $SSH_OPTS "$TARGET" "chmod +x $REMOTE_DIR/install.sh && $REMOTE_CMD; RC=\$?; rm -rf $REMOTE_DIR; exit \$RC"
