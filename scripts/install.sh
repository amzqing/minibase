#!/bin/sh
# MiniBase 目标机安装器。
# 用法: sudo ./install.sh --binary /path/to/minicore [--token TOKEN] [--listen PORT] [--config PATH]
# 做完这些事：装 Node.js、生成配置、建用户和目录、准备 cgroup、写 systemd 单元、启动并验证。
set -eu

BIN=""
TOKEN=""
LISTEN=":8080"
CFG_OPT=""
CLI_BIN=""
MASTER_KEY=""
UNIT_SRC=""

while [ $# -gt 0 ]; do
  case "$1" in
    --binary) BIN="$2"; shift 2 ;;
    --token)  TOKEN="$2"; shift 2 ;;
    --listen) LISTEN="$2"; shift 2 ;;
    --config) CFG_OPT="$2"; shift 2 ;;
    --cli-binary) CLI_BIN="$2"; shift 2 ;;
    *) echo "未知参数: $1" >&2; exit 2 ;;
  esac
done

if [ -z "$BIN" ] || [ ! -f "$BIN" ]; then
  echo "用法: sudo $0 --binary /path/to/minicore [--token TOKEN] [--listen PORT] [--config PATH]" >&2
  exit 2
fi
if [ "$(id -u)" -ne 0 ]; then
  echo "请用 sudo 或 root 运行" >&2
  exit 2
fi

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
UNIT_SRC="$SCRIPT_DIR/minicore.service"

echo "[1/7] 检查 Node.js"
if ! command -v node >/dev/null 2>&1; then
  if command -v apt-get >/dev/null 2>&1; then
    echo "  未检测到 Node.js，正在用 apt 安装..."
    DEBIAN_FRONTEND=noninteractive apt-get update -qq
    DEBIAN_FRONTEND=noninteractive apt-get install -y -qq nodejs npm
  else
    echo "  未检测到 Node.js，且系统没有 apt-get。请先手动安装 Node.js 再重试。" >&2
    exit 1
  fi
fi
echo "  Node $(node --version)"

echo "[2/7] 建运行用户 minibase"
if ! id -u minibase >/dev/null 2>&1; then
  useradd --system --home-dir /var/lib/minibase --shell /usr/sbin/nologin minibase
fi

echo "[3/7] 建目录"
mkdir -p /opt/minibase/bin /var/lib/minibase /etc/minibase

echo "[4/7] 生成 Token 与配置"
if [ -z "$TOKEN" ]; then
  TOKEN="$(head -c 24 /dev/urandom | od -An -tx1 | tr -d ' \n')"
fi
if command -v sha256sum >/dev/null 2>&1; then
  TOKEN_HASH="sha256:$(printf '%s' "$TOKEN" | sha256sum | awk '{print $1}')"
else
  TOKEN_HASH="sha256:$(printf '%s' "$TOKEN" | shasum -a 256 | awk '{print $1}')"
fi
if [ -z "$MASTER_KEY" ]; then
  MASTER_KEY="$(head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')"
fi
if [ -n "$CFG_OPT" ]; then
  install -m 0640 "$CFG_OPT" /etc/minibase/config.json
else
  cat > /etc/minibase/config.json <<CFG_EOF
{
  "listen_addr": "$LISTEN",
  "token_hash": "$TOKEN_HASH",
  "data_dir": "/var/lib/minibase/data",
  "port_start": 30000,
  "port_end": 30100,
  "busy_timeout_ms": 5000,
  "sql_timeout_ms": 3000,
  "health_timeout_ms": 3000,
  "memory_limit_mb": 256,
  "npm_install_timeout_ms": 180000,
  "max_unpacked_mb": 500,
  "health_retry_interval_ms": 100,
  "health_probe_timeout_ms": 1000,
  "process_stop_timeout_ms": 5000,
  "max_upload_mb": 1024,
  "multipart_memory_mb": 32,
  "read_header_timeout_ms": 10000,
  "shutdown_timeout_ms": 10000,
  "log_tail_bytes": 2048,
  "master_key": "$MASTER_KEY",
  "cpu_quota_percent": 50,
  "max_concurrent_apps": 20,
  "monitor_interval_ms": 10000,
  "unhealthy_threshold": 3,
  "max_restarts": 5,
  "restart_window_sec": 300,
  "audit_log_max_rows": 1000,
  "audit_log_max_days": 90,
  "journald_max_mb": 200,
  "disk_warn_percent": 85,
  "disk_force_percent": 95,
  "storage_max_mb_per_app": 500,
  "storage_max_mb_total": 2048,
  "storage_max_file_mb": 50,
  "cgroup_root": "/sys/fs/cgroup/system.slice/minicore.service"
}
CFG_EOF
fi

echo "[5/7] 安装二进制"
install -m 0755 "$BIN" /opt/minibase/bin/minicore
if [ -n "$CLI_BIN" ] && [ -f "$CLI_BIN" ]; then
  install -m 0755 "$CLI_BIN" /opt/minibase/bin/minibase
fi
chown -R minibase:minibase /opt/minibase /var/lib/minibase /etc/minibase

echo "[6/7] 写 systemd 单元并准备 cgroup"
# journald 限额（排障日志回收第一层）
mkdir -p /etc/systemd/journald.conf.d
cat > /etc/systemd/journald.conf.d/minibase.conf <<'JEOF'
[Journal]
SystemMaxUse=200M
MaxRetentionSec=7day
JEOF
systemctl restart systemd-journald 2>/dev/null || true

if [ ! -f "$UNIT_SRC" ]; then
  echo "  找不到 systemd 单元: $UNIT_SRC" >&2
  exit 2
fi
install -m 0644 "$UNIT_SRC" /etc/systemd/system/minicore.service
if [ -f /sys/fs/cgroup/cgroup.controllers ] && grep -qw memory /sys/fs/cgroup/cgroup.controllers; then
  chown -R minibase:minibase /sys/fs/cgroup/system.slice/minicore.service 2>/dev/null || true
else
  echo "  未检测到 cgroups v2，应用内存限制不会启用"
fi
systemctl daemon-reload

echo "[7/7] 启动并设置开机自启"
systemctl enable minicore >/dev/null
systemctl restart minicore
sleep 2
if ! systemctl is-active --quiet minicore; then
  echo "  Core 启动失败，请查看: journalctl -u minicore -n 50" >&2
  exit 1
fi

HOST="$(hostname -I 2>/dev/null | awk '{print $1}')"
PORT="${LISTEN##*:}"
echo ""
echo "安装完成。"
echo "  管理 Token : $TOKEN"
echo "  主密钥     : $MASTER_KEY（请备份，丢失后密钥不可恢复）"
echo "  健康检查   : curl http://127.0.0.1:${PORT}/health"
echo "  访问地址   : http://${HOST}:${PORT}/apps/<项目ID>/<应用名>/"
echo "  服务器 CLI  : /opt/minibase/bin/minibase"
echo "  设置客户端 : export MINIBASE_TOKEN=$TOKEN"
echo "提示：第一版不备份，数据自己负责。"
