# EzhikLB fork — status

This fork branched from upstream `ezhikdev/ezhiklb` 1.0.9 with five
changes requested against that codebase:

1. Panel rewritten from Go to Python (FastAPI + SQLAlchemy async).
2. Node connection protocol inverted: the panel dials out to each node's
   own local control API instead of nodes polling an inbound panel port.
   The node agent stays Go (unchanged reconciler/health/metrics/updater),
   only its transport layer and boot sequence changed.
3. "Профили" renamed "Ядра" throughout the panel UI/API, editor
   restructured to read as Входящие (inbound listeners) /
   Исходящие (each listener's own outbound backends), nav grouped as
   Узлы → {Узлы, Ядра, Журналы}.
4. Node enrollment is one step: an install script on the node prints a
   ready-to-paste connection block (address, port, API key, certificate);
   one "Добавить узел" dialog in the panel, one submit.
5. Storage is SQLite or PostgreSQL, chosen at panel install time.

See [ARCHITECTURE.md](ARCHITECTURE.md) for how each of these was
implemented and what was deliberately left unchanged from upstream.

## What's carried over unchanged

Everything in the data-plane path: differential IPVS apply, the scoped
one-shot connection reset, the split UDP/conntrack timeout tuning, ICMP
health-check weight management, and the signed self-update flow. These
were extensively production-tested upstream (see the original project's
`CHANGELOG.md` for the incident history behind the UDP timeout split in
particular) and were ported without behavioural changes — only the
transport around them changed.

## Explicitly out of scope for this fork

- **No in-place upgrade path from an upstream Go install.** This is a
  clean fork with its own schema and protocol, not a drop-in replacement
  for an existing `ezhiklb` deployment. Bootstrapping starts from an
  empty database.
- **No legacy-config migration** (`internal/domain/legacy.go`'s
  `ParseLegacyFile`, for importing configs from the pre-ezhiklb "Ezhik
  UDP" tool) was ported — it has nothing to do with this fork's changes
  and no upstream Go install to migrate from either.
- **No panel-initiated node-credential rotation.** The node is now the
  source of truth for its own API key/certificate; rotating means
  re-enrolling the node and pasting its new connection block into the
  panel's edit-node dialog. See ARCHITECTURE.md.

## Known gaps to close before a production release

- ~~**Release pipeline.**~~ Closed: `.github/workflows/release.yml` builds
  both `ezhiklb-node-agent_<version>_linux_amd64.tar.gz` (+ `.sha256`) and
  the panel bundle (`ezhiklb-<version>.tar.gz`) on every `vX.Y.Z` tag push
  and publishes them as a GitHub Release. `scripts/bootstrap-panel.sh` and
  `scripts/bootstrap-node.sh` are the resulting one-line installers
  (`curl -fsSL .../bootstrap-panel.sh | sudo bash`). Still required before
  any of this actually works: replace every `YOUR_GITHUB_USERNAME/ezhiklb`
  placeholder (`internal/agent/updater.go`'s `ReleaseRepo`,
  `web/src/hooks/use-version-check.ts`, `scripts/ezhik-lb`,
  `scripts/bootstrap-*.sh`) with your fork's real "owner/repo", then push a
  `v1.0.0`-shaped tag once.
- **Real IPVS/iptables verification.** Everything reconciler-adjacent is
  unit-tested against a fake command runner (works everywhere, including
  non-Linux dev machines) but has not been exercised against real
  `ipvsadm`/`iptables` on a Linux box as part of this fork's own work —
  see [TESTING.md](TESTING.md) for the checklist to run on a disposable
  Debian/Ubuntu VPS before trusting this in production.
- **Docker node path is unverified end-to-end** (image builds, but a full
  `docker run --network host ...` cycle against real traffic hasn't been
  exercised) — see TESTING.md.
- **`FORK_URL` placeholder** in `web/src/App.tsx` needs to point at this
  fork's actual repository once published.
