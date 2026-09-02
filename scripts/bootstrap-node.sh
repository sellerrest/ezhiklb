#!/usr/bin/env bash
# True one-liner node install — downloads the latest released agent binary
# plus install-node.sh/node-agent.Dockerfile from the matching tag, then
# runs the real installer. Nothing to git-clone or build by hand first:
#
#   curl -fsSL https://raw.githubusercontent.com/sellerrest/ezhiklb/main/scripts/bootstrap-node.sh | sudo bash
#   curl -fsSL .../bootstrap-node.sh | sudo bash -s -- --docker
#
# Reuses the exact same release asset internal/agent/updater.go's
# self-update downloads (ezhiklb-node-agent_<version>_linux_amd64.tar.gz +
# .sha256) — see .github/workflows/release.yml for what builds it.
set -Eeuo pipefail

REPO_SLUG="sellerrest/ezhiklb"
MODE="systemd"
[[ "${1:-}" == "--docker" ]] && MODE="docker"

log() { printf '\n\033[1;36mezhik-lb bootstrap\033[0m %s\n' "$*"; }
die() { printf '\nbootstrap error: %s\n' "$*" >&2; exit 1; }

[[ "${EUID}" -eq 0 ]] || die "run as root: curl -fsSL .../bootstrap-node.sh | sudo bash"
command -v curl >/dev/null || die "curl is required"
command -v tar >/dev/null || die "tar is required"
command -v sha256sum >/dev/null || die "sha256sum is required"

log "checking latest release of ${REPO_SLUG}"
release_json="$(curl -fsS "https://api.github.com/repos/${REPO_SLUG}/releases/latest" 2>/dev/null)" || release_json=""
latest="$(printf '%s' "$release_json" | sed -n 's/.*"tag_name": *"v\{0,1\}\([^"]*\)".*/\1/p')"
[[ -n "$latest" ]] || die "could not determine the latest release — has one been published yet? See .github/workflows/release.yml"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
asset="ezhiklb-node-agent_${latest}_linux_amd64.tar.gz"
base="https://github.com/${REPO_SLUG}/releases/download/v${latest}"

log "downloading agent binary ${latest}"
curl -fsSL "${base}/${asset}" -o "${tmp}/${asset}" || die "download failed: ${base}/${asset}"
curl -fsSL "${base}/${asset}.sha256" -o "${tmp}/${asset}.sha256" || die "download failed: ${base}/${asset}.sha256"
( cd "$tmp" && sha256sum -c "${asset}.sha256" ) || die "checksum mismatch — download corrupted or tampered with"

mkdir -p "${tmp}/bundle/bin" "${tmp}/bundle/scripts" "${tmp}/bundle/docker"
tar -xzf "${tmp}/${asset}" -C "${tmp}/bundle/bin" ezhiklb-agent
chmod 0755 "${tmp}/bundle/bin/ezhiklb-agent"

log "fetching install-node.sh (tag v${latest})"
curl -fsSL "https://raw.githubusercontent.com/${REPO_SLUG}/v${latest}/scripts/install-node.sh" -o "${tmp}/bundle/scripts/install-node.sh" \
  || die "could not fetch scripts/install-node.sh for tag v${latest}"
chmod 0755 "${tmp}/bundle/scripts/install-node.sh"
if [[ "$MODE" == "docker" ]]; then
  curl -fsSL "https://raw.githubusercontent.com/${REPO_SLUG}/v${latest}/docker/node-agent.Dockerfile" -o "${tmp}/bundle/docker/node-agent.Dockerfile" \
    || die "could not fetch docker/node-agent.Dockerfile for tag v${latest}"
fi

log "running install-node.sh from the assembled bundle"
if [[ "$MODE" == "docker" ]]; then
  "${tmp}/bundle/scripts/install-node.sh" --docker
else
  "${tmp}/bundle/scripts/install-node.sh"
fi
