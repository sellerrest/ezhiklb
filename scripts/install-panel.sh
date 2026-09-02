#!/usr/bin/env bash
# Installs or upgrades the EzhikLB panel (Python/FastAPI). Run this from a
# release bundle that contains this script alongside `panel/` (the Python
# source + requirements.txt) and `web/dist/` (the built frontend) —
# see docs/ARCHITECTURE.md for the expected bundle layout.
set -Eeuo pipefail

EZHIKLB_VERSION="1.0.4"
PREFIX="/opt/ezhiklb"
CONFIG_DIR="/etc/ezhiklb"
DATA_DIR="/var/lib/ezhiklb"
WEB_DIR="/usr/share/ezhiklb/web"
ENV_FILE="${CONFIG_DIR}/ezhiklb.env"
VERSION_FILE="${CONFIG_DIR}/panel-version"
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
BUNDLE_DIR="$(cd -- "${SCRIPT_DIR}/.." && pwd)"
BACKUP_ROOT="/var/backups/ezhiklb"

log() { printf '\n\033[1;36mEzhikLB panel\033[0m %s\n' "$*"; }
die() { printf '\nEzhikLB panel installer error: %s\n' "$*" >&2; exit 1; }

# Reads a prompt from the controlling terminal, never from stdin: when this
# script runs as `curl ... | sudo bash`, stdin is the pipe itself, and bash
# can leave unread tail bytes of that very script sitting in it — a plain
# `read` would consume those bytes as if they were the user's answer.
tty_read() {
  if ! { read -r "$@" < /dev/tty; } 2>/dev/null; then
    die "нет терминала для интерактивного ввода — задайте соответствующие переменные окружения (EZHIKLB_HOST/EZHIKLB_PORT/EZHIKLB_DATABASE_URL) для неинтерактивной установки"
  fi
}

# Fresh cloud VPS images commonly run apt-get in the background on first
# boot (cloud-init, unattended-upgrades) and hold the dpkg/apt lock for a
# while — retry instead of failing a one-click install outright.
apt_get_retry() {
  local attempt=1 max_attempts=60
  until apt-get "$@"; do
    (( attempt >= max_attempts )) && die "apt-get $1 продолжает падать — похоже, apt/dpkg завис у другого процесса (проверьте 'ps aux | grep apt')"
    log "apt/dpkg занят другим процессом, повтор через 5с... (${attempt}/${max_attempts})"
    sleep 5
    attempt=$(( attempt + 1 ))
  done
}

detect_server_ipv4() {
  local detected=""
  if command -v ip >/dev/null 2>&1; then
    detected="$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src"){print $(i+1);exit}}')"
  fi
  printf '%s' "$detected"
}

[[ "${EUID}" -eq 0 ]] || die "run this installer as root"
[[ -r /etc/os-release ]] || die "unsupported operating system"
. /etc/os-release
case "${ID:-}" in ubuntu|debian) ;; *) die "only Debian and Ubuntu are supported" ;; esac
[[ -d "${BUNDLE_DIR}/panel" && -f "${BUNDLE_DIR}/panel/requirements.txt" ]] || die "missing panel/ next to this script; use a release bundle"
[[ -f "${BUNDLE_DIR}/web/dist/index.html" ]] || die "missing compiled web/dist/index.html; run 'npm run build' in web/ first or use a release bundle"

EXISTING_VERSION=""
[[ -f "$VERSION_FILE" ]] && EXISTING_VERSION="$(<"$VERSION_FILE")"

valid_tcp_port() { local value="$1"; [[ "$value" =~ ^[0-9]+$ ]] && (( value >= 1024 && value <= 65535 )); }
tcp_port_in_use() {
  local port="$1" hex_port=""
  printf -v hex_port '%04X' "$port"
  awk -v wanted="$hex_port" '
    NR > 1 { split($2, address, ":"); if (toupper(address[2]) == wanted && $4 == "0A") found = 1 }
    END { exit(found ? 0 : 1) }
  ' /proc/net/tcp /proc/net/tcp6 2>/dev/null
}

panel_host="${EZHIKLB_HOST:-127.0.0.1}"
if [[ -z "$EXISTING_VERSION" && -z "${EZHIKLB_HOST:-}" ]]; then
  printf '\nКак открыть панель?\n'
  printf '  1) Только на сервере и через SSH-туннель (127.0.0.1) — рекомендуется\n'
  printf '  2) По сети (0.0.0.0, открывает web-интерфейс извне)\n'
  tty_read -p 'Доступ [1]: ' panel_access
  case "${panel_access:-1}" in 1) panel_host="127.0.0.1" ;; 2) panel_host="0.0.0.0" ;; *) die "неверный вариант доступа" ;; esac
fi

panel_port="${EZHIKLB_PORT:-8080}"
if [[ -z "$EXISTING_VERSION" ]]; then
  while true; do
    tty_read -p "Порт web-панели [${panel_port}]: " answer
    answer="${answer:-$panel_port}"
    if ! valid_tcp_port "$answer"; then printf 'Порт должен быть числом от 1024 до 65535.\n' >&2; continue; fi
    if tcp_port_in_use "$answer"; then printf 'Порт %s уже используется.\n' "$answer" >&2; continue; fi
    panel_port="$answer"; break
  done
fi

# Point 5 of the fork: choose SQLite (default, single-server) or PostgreSQL
# at install time. Both are supported by the same panel code — see
# panel/ezhiklb_panel/db.py — the installer only builds the connection URL.
database_url="${EZHIKLB_DATABASE_URL:-}"
if [[ -z "$EXISTING_VERSION" && -z "$database_url" ]]; then
  printf '\nВыберите базу данных:\n'
  printf '  1) SQLite (по умолчанию, для одного сервера)\n'
  printf '  2) PostgreSQL (для отдельного развёртывания БД)\n'
  tty_read -p 'База данных [1]: ' db_choice
  case "${db_choice:-1}" in
    1) database_url="sqlite+aiosqlite://${DATA_DIR}/ezhiklb.db" ;;
    2)
      tty_read -p 'PostgreSQL хост [127.0.0.1]: ' pg_host; pg_host="${pg_host:-127.0.0.1}"
      tty_read -p 'PostgreSQL порт [5432]: ' pg_port; pg_port="${pg_port:-5432}"
      tty_read -p 'PostgreSQL база данных [ezhiklb]: ' pg_db; pg_db="${pg_db:-ezhiklb}"
      tty_read -p 'PostgreSQL пользователь [ezhiklb]: ' pg_user; pg_user="${pg_user:-ezhiklb}"
      tty_read -s -p 'PostgreSQL пароль: ' pg_password; printf '\n'
      [[ -n "$pg_password" ]] || die "пароль PostgreSQL не может быть пустым"
      database_url="postgresql+asyncpg://${pg_user}:${pg_password}@${pg_host}:${pg_port}/${pg_db}"
      ;;
    *) die "неверный вариант базы данных" ;;
  esac
fi
[[ -n "$database_url" ]] || database_url="sqlite+aiosqlite://${DATA_DIR}/ezhiklb.db"

log "Installing system dependencies"
export DEBIAN_FRONTEND=noninteractive
apt_get_retry update
apt_get_retry install -y ca-certificates curl openssl python3 python3-venv python3-pip

if [[ -n "$EXISTING_VERSION" ]]; then
  stamp="$(date -u +%Y%m%dT%H%M%SZ)"
  backup_dir="${BACKUP_ROOT}/panel-${stamp}"
  log "Backing up the existing panel installation to ${backup_dir}"
  systemctl stop ezhiklb.service 2>/dev/null || true
  install -d -m 0700 "$backup_dir"
  [[ -d "$CONFIG_DIR" ]] && cp -a "$CONFIG_DIR" "${backup_dir}/etc"
  [[ -d "$DATA_DIR" ]] && cp -a "$DATA_DIR" "${backup_dir}/data"
fi

log "Preparing user and directories"
if ! getent passwd ezhiklb >/dev/null; then
  useradd --system --home-dir "$DATA_DIR" --shell /usr/sbin/nologin ezhiklb
fi
install -d -m 0750 -o root -g ezhiklb "$CONFIG_DIR"
install -d -m 0750 -o ezhiklb -g ezhiklb "$DATA_DIR"
install -d -m 0755 -o root -g root "$PREFIX" "$WEB_DIR"

if [[ ! -f "$ENV_FILE" ]]; then
  ingress="${EZHIKLB_INGRESS_ADDRESS:-$(detect_server_ipv4)}"
  cat >"$ENV_FILE" <<EOF
EZHIKLB_HOST=${panel_host}
EZHIKLB_PORT=${panel_port}
EZHIKLB_SECURE_COOKIE=0
EZHIKLB_DATABASE_URL=${database_url}
EZHIKLB_WEB_DIR=${WEB_DIR}
EZHIKLB_POLL_INTERVAL_SECONDS=${EZHIKLB_POLL_INTERVAL_SECONDS:-5}
EZHIKLB_INGRESS_ADDRESS=${ingress}
EOF
  chmod 0640 "$ENV_FILE"
  chown root:ezhiklb "$ENV_FILE"
fi

log "Installing panel ${EZHIKLB_VERSION}"
rm -rf "${PREFIX}/panel" "${PREFIX}/venv"
cp -a "${BUNDLE_DIR}/panel" "${PREFIX}/panel"
rm -rf "${PREFIX}/panel/.venv" "${PREFIX}/panel/tests" "${PREFIX}/panel/__pycache__"
python3 -m venv "${PREFIX}/venv"
"${PREFIX}/venv/bin/pip" install --quiet --upgrade pip
"${PREFIX}/venv/bin/pip" install --quiet -r "${PREFIX}/panel/requirements.txt"
rm -rf "$WEB_DIR"
install -d -m 0755 "$WEB_DIR"
cp -a "${BUNDLE_DIR}/web/dist/." "$WEB_DIR/"
chown -R root:root "$PREFIX" "$WEB_DIR"

install -m 0755 "${BUNDLE_DIR}/scripts/ezhik-lb" /usr/local/bin/ezhik-lb

cat >/etc/systemd/system/ezhiklb.service <<EOF
[Unit]
Description=EzhikLB panel (control plane)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=ezhiklb
Group=ezhiklb
EnvironmentFile=${ENV_FILE}
WorkingDirectory=${PREFIX}/panel
ExecStart=${PREFIX}/venv/bin/python -m ezhiklb_panel.main
# 75 is the panel's own "please restart me with new settings" exit code
# (see ezhiklb_panel/main.py) — Restart=on-failure brings it straight back
# up bound to whatever port was just saved in Settings.
Restart=on-failure
RestartSec=2s
NoNewPrivileges=yes
PrivateTmp=yes
ProtectHome=yes
ProtectSystem=strict
ReadWritePaths=${DATA_DIR}

[Install]
WantedBy=multi-user.target
EOF

printf '%s\n' "$EZHIKLB_VERSION" >"$VERSION_FILE"
systemctl daemon-reload
systemctl enable ezhiklb.service
systemctl restart ezhiklb.service

installed_panel_port="$(sed -n 's/^EZHIKLB_PORT=//p' "$ENV_FILE")"
for _ in {1..20}; do
  curl -fsS "http://127.0.0.1:${installed_panel_port}/healthz" >/dev/null 2>&1 && break
  sleep 1
done
if ! systemctl is-active --quiet ezhiklb.service; then
  systemctl status ezhiklb.service --no-pager || true
  journalctl -u ezhiklb.service -n 60 --no-pager || true
  die "panel health check failed"
fi

log "${EZHIKLB_VERSION} installed successfully"
installed_host="$(sed -n 's/^EZHIKLB_HOST=//p' "$ENV_FILE")"
panel_ipv4="$(sed -n 's/^EZHIKLB_INGRESS_ADDRESS=//p' "$ENV_FILE")"
if [[ "$installed_host" == "127.0.0.1" ]]; then
  printf 'Local panel: http://127.0.0.1:%s\n' "$installed_panel_port"
  printf 'SSH tunnel: ssh -L %s:127.0.0.1:%s root@YOUR_SERVER\n' "$installed_panel_port" "$installed_panel_port"
elif [[ -n "$panel_ipv4" ]]; then
  printf 'Open in browser: http://%s:%s\n' "$panel_ipv4" "$installed_panel_port"
fi
printf 'Database: %s\n' "$(sed -n 's/^EZHIKLB_DATABASE_URL=//p' "$ENV_FILE" | sed -E 's#://[^:]+:[^@]+@#://***:***@#')"
printf 'Configuration: %s\n' "$ENV_FILE"
printf '\nОткройте панель по адресу выше — при первом заходе она попросит\n'
printf 'придумать логин и пароль администратора и сохранит их в базе.\n'
printf '\nДобавьте ноду через scripts/install-node.sh на нужной VPS и вставьте её\n'
printf 'вывод (адрес, порт, API ключ, сертификат) в панели: Узлы → Добавить узел.\n'
printf '\nУправление панелью: ezhik-lb help (stop, restart, logs, edit-env, edit,\n'
printf 'admins, update, uninstall).\n'
