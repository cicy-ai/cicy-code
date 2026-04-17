#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
CONF_SRC="$ROOT_DIR/scripts/supervisor/cicy-code-dev.conf"
CONF_DST="/etc/supervisor/conf.d/cicy-code-dev.conf"
PROGRAM="cicy-code-dev"
LOG_DIR="$HOME/.cicy/logs"

usage() {
  cat <<'EOF'
Usage: ./scripts/dev-supervisor.sh <install|start|restart|stop|status|tail>
EOF
}

ensure_log_dir() {
  mkdir -p "$LOG_DIR"
}

install_conf() {
  ensure_log_dir
  sudo install -D -m 0644 "$CONF_SRC" "$CONF_DST"
  sudo supervisorctl reread >/dev/null
  sudo supervisorctl update >/dev/null
}

require_conf() {
  if [ ! -f "$CONF_DST" ]; then
    install_conf
  fi
}

cmd="${1:-status}"

case "$cmd" in
  install)
    install_conf
    echo "[dev-supervisor] installed: $CONF_DST"
    ;;
  start)
    require_conf
    sudo supervisorctl start "$PROGRAM" || true
    sudo supervisorctl status "$PROGRAM"
    ;;
  restart)
    require_conf
    sudo supervisorctl restart "$PROGRAM" || sudo supervisorctl start "$PROGRAM"
    sudo supervisorctl status "$PROGRAM"
    ;;
  stop)
    require_conf
    sudo supervisorctl stop "$PROGRAM" || true
    sudo supervisorctl status "$PROGRAM" || true
    ;;
  status)
    require_conf
    sudo supervisorctl status "$PROGRAM"
    ;;
  tail)
    ensure_log_dir
    tail -f "$LOG_DIR/cicy-code-dev-supervisor.log"
    ;;
  *)
    usage
    exit 1
    ;;
esac
