#!/bin/sh
# MiniBase 目标机升级器：备份旧二进制 → 替换 → 重启 → 探活；失败自动回滚。
# 不碰 config.json、system.db 和数据目录，保留 master_key 和应用数据。
# 用法: sudo ./upgrade.sh <新minicore二进制> [新CLI二进制]
set -eu

NEW_BIN="${1:-}"
NEW_CLI="${2:-}"
if [ -z "$NEW_BIN" ] || [ ! -f "$NEW_BIN" ]; then
  echo "用法: sudo $0 <新minicore二进制> [新CLI二进制]" >&2
  exit 2
fi
if [ "$(id -u)" -ne 0 ]; then
  echo "请用 sudo 或 root 运行" >&2
  exit 2
fi

TARGET=/opt/minibase/bin/minicore
BACKUP="${TARGET}.bak.$(date +%Y%m%d%H%M%S)"
CLI_TARGET=/opt/minibase/bin/minibase
CLI_BACKUP=""

echo "[1/5] 备份旧二进制"
cp -p "$TARGET" "$BACKUP"
if [ -n "$NEW_CLI" ] && [ -f "$NEW_CLI" ] && [ -f "$CLI_TARGET" ]; then
  CLI_BACKUP="${CLI_TARGET}.bak.$(date +%Y%m%d%H%M%S)"
  cp -p "$CLI_TARGET" "$CLI_BACKUP"
fi

echo "[2/5] 替换二进制"
install -m 0755 "$NEW_BIN" "$TARGET"
if [ -n "$NEW_CLI" ] && [ -f "$NEW_CLI" ]; then
  install -m 0755 "$NEW_CLI" "$CLI_TARGET"
fi

echo "[3/5] 热重载 Core（PID 不变，应用不受影响）"
systemctl reset-failed minicore 2>/dev/null || true
PID=$(systemctl show -p MainPID --value minicore)
if [ -n "$PID" ] && [ "$PID" != "0" ]; then
  kill -USR2 "$PID" 2>/dev/null || true
fi

echo "[4/5] 探活"
OK=0
for i in $(seq 1 30); do
  if curl -sf http://127.0.0.1:8080/health >/dev/null 2>&1; then OK=1; break; fi
  sleep 1
done

if [ "$OK" -eq 0 ]; then
  echo "[5/5] 探活失败，回滚到旧二进制"
  cp -p "$BACKUP" "$TARGET"
  if [ -n "$CLI_BACKUP" ] && [ -f "$CLI_BACKUP" ]; then
    cp -p "$CLI_BACKUP" "$CLI_TARGET"
  fi
  systemctl restart minicore
  sleep 2
  echo "升级失败，已回滚。请查看: journalctl -u minicore -n 50" >&2
  exit 1
fi

echo "[5/5] 升级成功"
echo "  旧二进制备份: $BACKUP"
echo "  系统未中断，应用进程未重启。"
