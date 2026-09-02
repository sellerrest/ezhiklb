#!/usr/bin/env bash
# True one-liner panel install — downloads the latest release bundle and
# runs install-panel.sh from inside it, so there's nothing to git-clone or
# build by hand first:
#
#   curl -fsSL https://raw.githubusercontent.com/sellerrest/ezhiklb/main/scripts/bootstrap-panel.sh | sudo bash
#
# install-panel.sh itself still needs to run from a real bundle directory
# (it reads panel/ and web/dist/ next to itself) — this script's only job
# is fetching that bundle from GitHub Releases first. See .github/workflows/
# release.yml for what builds "ezhiklb-<version>.tar.gz".
set -Eeuo pipefail

REPO_SLUG="sellerrest/ezhiklb"

log() { printf '\n\033[1;36mezhik-lb bootstrap\033[0m %s\n' "$*"; }
die() { printf '\nbootstrap error: %s\n' "$*" >&2; exit 1; }

[[ "${EUID}" -eq 0 ]] || die "run as root: curl -fsSL .../bootstrap-panel.sh | sudo bash"
command -v curl >/dev/null || die "curl is required"
command -v tar >/dev/null || die "tar is required"

log "checking latest release of ${REPO_SLUG}"
latest="$(curl -fsS "https://api.github.com/repos/${REPO_SLUG}/releases/latest" | sed -n 's/.*"tag_name": *"v\{0,1\}\([^"]*\)".*/\1/p')"
[[ -n "$latest" ]] || die "could not determine the latest release — has one been published yet? See .github/workflows/release.yml"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
log "downloading release ${latest}"
curl -fsSL "https://github.com/${REPO_SLUG}/releases/download/v${latest}/ezhiklb-${latest}.tar.gz" -o "${tmp}/release.tar.gz" \
  || die "download failed — release v${latest} should contain ezhiklb-${latest}.tar.gz"
tar -xzf "${tmp}/release.tar.gz" -C "$tmp"
[[ -x "${tmp}/scripts/install-panel.sh" ]] || die "release archive is missing scripts/install-panel.sh"

log "running install-panel.sh from the downloaded bundle"
"${tmp}/scripts/install-panel.sh"
# (not exec'd: the EXIT trap above needs to actually fire to clean up $tmp)
