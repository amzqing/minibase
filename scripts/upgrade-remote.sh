#!/bin/sh
# 本机一键升级远程 Core：编译 → 上传 → 远程 upgrade.sh。
# 与 deploy.sh（全量安装）不同：升级不碰配置、数据、master_key。
# 用法: ./scripts/upgrade-remote.sh root@服务器IP [--arch amd64]
set -eu

TARGET="${1:-}"
SSH_OPTS="${MINICLOUD_SSH_OPTS:-}"
if [ -z "$TARGET" ] || [ "$TARGET" = "-h" ]; then
  echo "用法: $0 root@服务器IP [--arch amd64]" >&2
  exit 2
fi
shift
ARCH=""
while [ $# -gt 0 ]; do
  case "$1" in
    --arch) ARCH="$2"; shift 2 ;;
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
    x86_64) ARCH=amd64 ;;
    aarch64|arm64) ARCH=arm64 ;;
    *) echo "不支持的架构: $MACHINE" >&2; exit 1 ;;
  esac
fi

echo "[2/4] 交叉编译"
(cd "$ROOT_DIR" && GOOS=linux GOARCH="$ARCH" CGO_ENABLED=0 go build -o "$BUILD_DIR/minicore" ./cmd/minicore)
(cd "$ROOT_DIR" && GOOS=linux GOARCH="$ARCH" CGO_ENABLED=0 go build -o "$BUILD_DIR/minicloud" ./cmd/minicloud)

echo "[3/4] 上传"
REMOTE_DIR="/tmp/minicloud-upgrade-$$"
ssh $SSH_OPTS "$TARGET" "mkdir -p $REMOTE_DIR"
scp -q $SSH_OPTS "$BUILD_DIR/minicore" "$BUILD_DIR/minicloud" "$ROOT_DIR/scripts/upgrade.sh" "$TARGET:$REMOTE_DIR/"

echo "[4/4] 远程升级"
ssh $SSH_OPTS "$TARGET" "chmod +x $REMOTE_DIR/upgrade.sh && $REMOTE_DIR/upgrade.sh $REMOTE_DIR/minicore $REMOTE_DIR/minicloud; RC=\$?; rm -rf $REMOTE_DIR; exit \$RC"
