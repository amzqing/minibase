#!/bin/sh
# MiniBase 目标机卸载器。
# 默认只卸载程序，保留配置和数据；--purge-data 连配置、数据、用户一起删。
# 用法: sudo ./uninstall.sh [--purge-data] [--yes]
set -eu

PURGE_DATA=0
ASSUME_YES=0
while [ $# -gt 0 ]; do
  case "$1" in
    --purge-data) PURGE_DATA=1; shift ;;
    --yes|-y) ASSUME_YES=1; shift ;;
    -h|--help)
      cat <<'HELP_EOF'
用法: sudo ./uninstall.sh [--purge-data] [--yes]

默认：停止服务，清理残留进程，删除 systemd 单元和二进制；
      保留 /etc/minibase/config.json（含 master_key）和 /var/lib/minibase。
--purge-data：额外删除配置、数据和 minibase 用户。不可恢复，需要确认。
--yes, -y   ：跳过 --purge-data 的确认，用于自动化。
HELP_EOF
      exit 0
      ;;
    *) echo "未知参数: $1" >&2; exit 2 ;;
  esac
done

if [ "$(id -u)" -ne 0 ]; then
  echo "请用 sudo 或 root 运行" >&2
  exit 2
fi

SERVICE=minicore.service
UNIT=/etc/systemd/system/minicore.service
BIN_DIR=/opt/minibase/bin
CFG_DIR=/etc/minibase
DATA_DIR=/var/lib/minibase
APP_USER=minibase

echo "[1/7] 停止并禁用服务"
if command -v systemctl >/dev/null 2>&1; then
  systemctl stop "$SERVICE" >/dev/null 2>&1 || true
  systemctl disable "$SERVICE" >/dev/null 2>&1 || true
else
  echo "  未检测到 systemctl，跳过服务操作"
fi

echo "[2/7] 清理残留应用进程"
if command -v pkill >/dev/null 2>&1; then
  pkill -u "$APP_USER" >/dev/null 2>&1 || true
  sleep 1
  pkill -9 -u "$APP_USER" >/dev/null 2>&1 || true
else
  echo "  未检测到 pkill，跳过残留进程清理"
fi

echo "[3/7] 删除 systemd 单元"
rm -f "$UNIT"
if command -v systemctl >/dev/null 2>&1; then
  systemctl daemon-reload >/dev/null 2>&1 || true
  systemctl reset-failed "$SERVICE" >/dev/null 2>&1 || true
fi

echo "[4/7] 删除二进制"
rm -f "$BIN_DIR/minicore" "$BIN_DIR/minibase"
rmdir "$BIN_DIR" 2>/dev/null || true
rmdir /opt/minibase 2>/dev/null || true

echo "[5/7] 处理配置和数据"
if [ "$PURGE_DATA" -eq 1 ]; then
  if [ "$ASSUME_YES" -ne 1 ]; then
    echo "  这会删除 $CFG_DIR 和 $DATA_DIR，且不可恢复。"
    printf '  确认删除请输入 yes: '
    ANSWER=""
    read -r ANSWER || true
    if [ "$ANSWER" != "yes" ]; then
      echo "已取消，未删除配置和数据。" >&2
      exit 1
    fi
  fi
  rm -rf "$CFG_DIR" "$DATA_DIR"
  echo "  已删除配置和数据"
else
  echo "  保留 $CFG_DIR（含 master_key）和 $DATA_DIR"
fi

echo "[6/7] 处理运行用户"
if [ "$PURGE_DATA" -eq 1 ]; then
  if id -u "$APP_USER" >/dev/null 2>&1; then
    userdel "$APP_USER" >/dev/null 2>&1 || true
  fi
  echo "  已处理用户 $APP_USER"
else
  echo "  保留用户 $APP_USER（数据目录仍归它所有）"
fi

echo "[7/7] 验证"
if command -v pgrep >/dev/null 2>&1 && pgrep -u "$APP_USER" >/dev/null 2>&1; then
  echo "  仍有 $APP_USER 进程在运行，卸载未完成" >&2
  exit 1
fi
if [ -f "$UNIT" ]; then
  echo "  systemd 单元仍然存在: $UNIT" >&2
  exit 1
fi
if [ -e "$BIN_DIR/minicore" ] || [ -e "$BIN_DIR/minibase" ]; then
  echo "  二进制仍然存在" >&2
  exit 1
fi
if command -v systemctl >/dev/null 2>&1 && systemctl is-active --quiet "$SERVICE" 2>/dev/null; then
  echo "  服务仍在运行" >&2
  exit 1
fi

echo "卸载完成。"
if [ "$PURGE_DATA" -eq 0 ]; then
  echo "  已卸载：服务、systemd 单元、Core、CLI"
  echo "  已保留：配置 /etc/minibase/config.json、数据 /var/lib/minibase"
  echo "  重装时请带 --config /etc/minibase/config.json，避免 master_key 丢失。"
else
  echo "  已卸载：服务、二进制、配置、数据和用户"
  echo "  数据已删除，不可恢复。"
fi
