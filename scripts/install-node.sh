#!/usr/bin/env bash
# Installs the EzhikLB node agent — either as a bare-metal systemd service
# (default) or as a Docker container (`--docker`). Either way, the node
# generates its own API key and self-signed TLS certificate on first boot
# and prints a connection block; paste that block into the panel's
# "Узлы → Добавить узел" dialog and you're done — see docs/ARCHITECTURE.md.
#
# Usage:
#   sudo ./install-node.sh              # bare-metal systemd service
#   sudo ./install-node.sh --docker     # Docker container instead
set -Eeuo pipefail

EZHIKLB_VERSION="1.0.0"
MODE="systemd"
[[ "${1:-}" == "--docker" ]] && MODE="docker"

PREFIX="/opt/ezhiklb"
DATA_DIR="/var/lib/ezhiklb-agent"
ENROLL_DIR="${DATA_DIR}/enroll"
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
BUNDLE_DIR="$(cd -- "${SCRIPT_DIR}/.." && pwd)"

log() { printf '\n\033[1;36mEzhikLB node\033[0m %s\n' "$*"; }
die() { printf '\nEzhikLB node installer error: %s\n' "$*" >&2; exit 1; }

[[ "${EUID}" -eq 0 ]] || die "run this installer as root"
[[ -r /etc/os-release ]] || die "unsupported operating system"
. /etc/os-release
case "${ID:-}" in ubuntu|debian) ;; *) die "only Debian and Ubuntu are supported" ;; esac

control_port="${EZHIKLB_AGENT_PORT:-62050}"
if [[ -z "${EZHIKLB_AGENT_PORT:-}" ]]; then
  read -r -p "Порт локального API агента [${control_port}]: " answer
  control_port="${answer:-$control_port}"
fi
[[ "$control_port" =~ ^[0-9]+$ ]] && (( control_port >= 1 && control_port <= 65535 )) || die "порт должен быть числом от 1 до 65535"

log "Loading kernel modules on the host (required even for the Docker path — an unprivileged container cannot modprobe the host kernel)"
export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y ca-certificates curl iproute2
cat >/etc/modules-load.d/ezhiklb.conf <<'EOF'
ip_vs
ip_vs_rr
ip_vs_wrr
nf_conntrack
xt_ipvs
EOF
cat >/etc/sysctl.d/98-ezhiklb.conf <<'EOF'
net.ipv4.ip_forward = 1
net.ipv4.vs.conntrack = 1
net.ipv4.vs.snat_reroute = 1
net.ipv4.vs.expire_nodest_conn = 1
net.ipv4.vs.expire_quiescent_template = 1
net.ipv4.conf.all.rp_filter = 2
net.ipv4.conf.default.rp_filter = 2
EOF
modprobe ip_vs ip_vs_rr ip_vs_wrr nf_conntrack xt_ipvs
sysctl --load /etc/sysctl.d/98-ezhiklb.conf >/dev/null

install -d -m 0750 -o root -g root "$DATA_DIR" "$ENROLL_DIR"

if [[ "$MODE" == "docker" ]]; then
  command -v docker >/dev/null 2>&1 || die "docker is not installed — install Docker first (https://docs.docker.com/engine/install/)"
  log "Building the node-agent image"
  [[ -f "${BUNDLE_DIR}/docker/node-agent.Dockerfile" ]] || die "missing docker/node-agent.Dockerfile next to this script; use a release bundle"
  docker build -t ezhiklb-node-agent:local -f "${BUNDLE_DIR}/docker/node-agent.Dockerfile" "$BUNDLE_DIR"
  docker rm -f ezhiklb-node >/dev/null 2>&1 || true
  log "Starting the node-agent container"
  docker run -d --name ezhiklb-node --restart unless-stopped \
    --network host --cap-add NET_ADMIN --cap-add NET_RAW --cap-add NET_BROADCAST \
    -v /lib/modules:/lib/modules:ro \
    -v "${DATA_DIR}:/var/lib/ezhiklb-agent" \
    -e EZHIKLB_AGENT_PORT="${control_port}" \
    ezhiklb-node-agent:local
  log "Waiting for the agent to generate its identity"
  for _ in {1..30}; do [[ -s "${ENROLL_DIR}/connection.txt" ]] && break; sleep 1; done
else
  apt-get install -y ipvsadm iptables iputils-ping conntrack
  [[ -x "${BUNDLE_DIR}/bin/ezhiklb-agent" ]] || die "missing bin/ezhiklb-agent next to this script; build it with 'cd node-agent && go build -o ../bin/ezhiklb-agent ./cmd/ezhiklb-agent' or use a release bundle"
  log "Installing the node agent binary"
  install -d -m 0755 -o root -g root "${PREFIX}/bin"
  install -m 0755 "${BUNDLE_DIR}/bin/ezhiklb-agent" "${PREFIX}/bin/ezhiklb-agent"

  cat >/etc/systemd/system/ezhiklb-agent.service <<EOF
[Unit]
Description=EzhikLB node agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
Environment=EZHIKLB_AGENT_STATE=${DATA_DIR}/state.json
Environment=EZHIKLB_AGENT_ENROLL_DIR=${ENROLL_DIR}
Environment=EZHIKLB_AGENT_PORT=${control_port}
ExecStart=${PREFIX}/bin/ezhiklb-agent
Restart=on-failure
RestartSec=3s
# Runs as root: only the agent needs CAP_NET_ADMIN/CAP_NET_RAW to manage
# IPVS and iptables — the panel itself never runs privileged. See
# docs/ARCHITECTURE.md's "Security boundary".
PrivateTmp=yes
ProtectHome=yes
ReadWritePaths=${DATA_DIR} ${PREFIX}/bin

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
  systemctl enable ezhiklb-agent.service
  systemctl restart ezhiklb-agent.service
  log "Waiting for the agent to generate its identity"
  for _ in {1..30}; do [[ -s "${ENROLL_DIR}/connection.txt" ]] && break; sleep 1; done
  if ! systemctl is-active --quiet ezhiklb-agent.service; then
    systemctl status ezhiklb-agent.service --no-pager || true
    journalctl -u ezhiklb-agent.service -n 60 --no-pager || true
    die "agent failed to start"
  fi
fi

if [[ ! -s "${ENROLL_DIR}/connection.txt" ]]; then
  die "agent did not print its connection block within 30s — check logs (journalctl -u ezhiklb-agent or docker logs ezhiklb-node)"
fi

log "${EZHIKLB_VERSION} node agent is running"
printf '\nВставьте блок ниже целиком в панели: Узлы → Добавить узел.\n\n'
cat "${ENROLL_DIR}/connection.txt"
printf '\n(этот блок также лежит в %s)\n' "${ENROLL_DIR}/connection.txt"
