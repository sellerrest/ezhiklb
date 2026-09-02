# EzhikLB architecture (fork)

This fork changes three things from upstream `ezhikdev/ezhiklb`: the panel
is Python instead of Go, the node-to-panel protocol is inverted (the panel
dials out to nodes instead of nodes polling the panel), and storage is
SQLite-or-PostgreSQL instead of SQLite-only. The node agent itself stays
Go — the IPVS/iptables reconciliation logic is proven and unit-tested; only
its transport layer changed. Everything below documents the parts that
differ from upstream; where something is unchanged (the reconciler's
differential apply, the UDP timeout split, the ICMP health monitor, the
signed self-update), it's noted as such rather than re-explained.

## Component layout

```text
panel/            Python (FastAPI + SQLAlchemy async), the control plane
  ezhiklb_panel/
    domain.py      CoreConfig/Listener/Backend validation — port of the Go
                    project's internal/domain/model.go, Profile renamed Core
    db.py           SQLAlchemy async engine + hand-rolled schema/migrations
    store.py        Repository functions — port of internal/store/store.go
    security.py     Token compare, hashing, pinned-TLS SSLContext builder
    node_client.py  Outbound HTTP client that dials a node's control API
    poller.py       Background loop: the panel-side half of the protocol
                    inversion (see below)
    api.py          FastAPI routers (admin API)
    web.py          Serves the built React frontend
    main.py         App factory + process entrypoint

node-agent/        Go, unchanged reconciliation logic + a new local API
  internal/agent/
    reconciler.go   IPVS/iptables/sysctl reconciliation — byte-for-byte
                    the same as upstream, including the UDP timeout split
    health.go       ICMP health monitor — unchanged
    metrics.go      /proc-based metrics collector — unchanged
    stats.go        `ipvsadm -Ln --stats` parser — unchanged
    updater.go      Signed self-update via GitHub Releases — unchanged
                    logic, points at ReleaseRepo (this fork's own releases)
    diagnostics.go  IPVS/firewall readiness probe — unchanged
    enroll.go       NEW: first-boot API key + self-signed TLS cert
                    generation, connection-block printing
    server.go       NEW: the node's local control API (replaces the old
                     poll loop in cmd/ezhiklb-agent/main.go)
  cmd/ezhiklb-agent/main.go   Boot sequence: restore -> enroll -> serve

web/               React/TypeScript frontend, adapted from upstream
```

## Protocol inversion

Upstream: the node polls `GET /agent/v1/nodes/{id}/desired` every 5s and
posts a heartbeat every 15s against a panel-owned, inbound-reachable
`agent_port`. The panel therefore needs an open port that every node must
be able to reach.

This fork: the **node** runs its own local control API (Go, TLS, API-key
authenticated) that the **panel dials out to** on an interval
(`poll_interval_seconds`, default 5s, in `poller.py`). The panel needs no
inbound-reachable port for this at all — only its own admin web UI, which
the README already recommends keeping behind an SSH tunnel.

Node-local endpoints (`internal/agent/server.go`):

| Method | Path | Purpose |
|---|---|---|
| GET | `/health` | Unauthenticated liveness probe |
| GET | `/v1/state` | Applied revision, apply/update state, health, stats, metrics, diagnostics — the old heartbeat body, now pulled |
| POST | `/v1/apply` | Body = `domain.NodeDesiredState`; runs the reconciler |
| POST | `/v1/update` | Body = `{target_version}`; kicks off self-update |
| POST | `/v1/health-probe` | Synchronous on-demand ICMP probe |
| POST | `/v1/decommission` | Tears down only EzhikLB-owned state, acks |

`POST /v1/apply`'s body is decoded straight into the *unmodified* Go
`domain.NodeDesiredState` struct — the panel's `NodeApplyRequest`
(`ezhiklb_panel/domain.py`) deliberately mirrors its JSON field names
(`profile_id`/`profile_name`, not `core_id`/`core_name`) even though the
panel calls the same entity a "core" in its own DB/API/UI. The
Профили→Ядра rename is a panel-side UX change; it does not touch the wire
protocol, which is why the proven Go reconciler needed zero source edits
beyond the new server/enroll files.

### Resilience is unchanged in spirit

On boot, before the node's API listener even starts,
`main.go` calls the *unmodified* `Reconciler.Restore()`: it loads
`state.json` and rebuilds IPVS + iptables + the kernel sysctls from the
last successfully applied revision, entirely independent of the panel.
That is what already made "panel down ⇒ node keeps forwarding
indefinitely" true upstream; this fork preserves it exactly, it just no
longer depends on a poll loop to matter.

### The panel-side poller (`ezhiklb_panel/poller.py`)

One asyncio background task, started in `main.py`'s FastAPI lifespan,
loops over every enabled, enrolled node on `poll_interval_seconds`:

1. `GET /v1/state`. On success, persist the result via
   `Store.record_node_state` (the pull-based analogue of the old heartbeat
   handler) and mark the node online.
2. If the node's `update_target` is set and not already mid-update,
   `POST /v1/update`.
3. Otherwise, if the node's desired revision (from the assigned core) does
   not match what it just reported as applied, `POST /v1/apply` with the
   core's config.
4. A node that fails to answer is simply left alone — its `last_seen_at`
   stops advancing, and `Store.list_nodes()`'s own staleness check (last
   seen more than `node_offline_after_seconds` ago) is what turns that
   into an "offline" status wherever nodes are read. No separate
   in-memory failure counter exists, so a poller restart can't desync one.
5. A node in `deleting` status gets `POST /v1/decommission` instead;
   once it acknowledges, `Store.finalize_decommission` deletes the row.
   An unreachable node stays `deleting` — the panel's existing
   force-delete escape hatch (`POST /api/v1/nodes/{id}/force-delete`)
   covers a node that will never come back.

This is what makes "panel comes back ⇒ node converges seamlessly" true:
the moment a poll succeeds, the panel pushes whatever changed, and the
reconciler's untouched add-before-remove/quiesce-before-delete logic
guarantees no unnecessary disruption.

### TLS pinning (trust-on-first-use)

A node's first boot (`internal/agent/enroll.go`) generates an ECDSA
keypair and a long-lived (15-year) self-signed certificate under
`.../enroll/`, plus a random 256-bit API key, and prints both together
with the detected public IPv4 and control port as one paste-able block
(also saved to `enroll/connection.txt`). The operator pastes that block
into the panel's "Добавить узел" dialog. The panel stores the exact
certificate PEM and connects with an `ssl.SSLContext` whose *only* trusted
CA is that PEM (`ezhiklb_panel/security.py:build_pinned_ssl_context`) —
standard TLS verification then only succeeds against that exact
certificate. No public CA is involved, and the identity is stable across
restarts (the keypair is generated once, never regenerated, so a restart
never breaks the pin).

The node authenticates the panel's calls with the API key as a Bearer
token, compared with a constant-time comparison
(`internal/agent/server.go:sameSecret`) — the same defense-in-depth
pattern the upstream project used for its own token comparisons.

Because the panel is now the one *presenting* the API key rather than
*verifying* one presented to it, `nodes.api_key` is stored in retrievable
(plaintext) form in the panel's database rather than hashed — a one-way
hash would make outbound authentication impossible. This is the same
trust boundary as the admin account's password hash: protected by the
database file's own permissions, never sent anywhere except straight to
the pinned node.

Admin login itself is username+password, created on first visit to the
panel (no more install-time `EZHIKLB_ADMIN_TOKEN` env var) — the password
is stored as a scrypt hash in the `admin_account` table, and a successful
login issues a random opaque session token stored in the `sessions` table,
set as an HttpOnly cookie. `require_admin` checks the cookie against that
table instead of comparing to a static secret.

### Rotating a node's credentials

There is no panel-initiated "rotate token" action in this fork, unlike
upstream — it doesn't have a coherent meaning once the *node* is the
source of truth for its own identity. To rotate: delete
`/var/lib/ezhiklb-agent/enroll/` on the node and restart the agent (it
regenerates a fresh key/cert and reprints the connection block), then use
the panel's node-edit dialog to paste the new API key and certificate
(`PUT /api/v1/nodes/{id}` accepts optional `api_key`/`cert_pem` fields for
exactly this).

## Everything else — unchanged behaviour

The following are ports with preserved behaviour, not redesigns; see the
Go source for the authoritative comments:

- **Differential IPVS apply / rollback-on-failure**
  (`Reconciler.Reconcile`, `applyIPVS`): add-before-remove, quiesce weight
  to zero before deleting a destination, roll back to the previous
  service set on failure.
- **Scoped one-shot connection reset** (`resetConnectionState`,
  `purgeConntrack`): never touches `ipvsadm -C` or `conntrack -F`
  host-wide, only EzhikLB-owned services/destinations.
- **UDP idle timeouts** — the split between
  `nf_conntrack_udp_timeout_stream` (24h, what the firewall's ESTABLISHED
  check actually depends on) and IPVS's own UDP connection timeout (300s,
  kept short on purpose to bound `/proc/net/ip_vs_conn`'s size) is
  preserved exactly, with the original incident history documented inline
  in `reconciler.go`'s comments. This was hard-won production knowledge
  (see the upstream changelog for the 1.0.8 → 1.0.9 story) and was not
  touched during the port.
- **Signed self-update**: downloads a release archive + `.sha256` from
  `agent.ReleaseRepo` (set this to your own fork's GitHub repo before
  relying on it — see `internal/agent/updater.go`), verifies the checksum
  before touching anything on disk, atomically renames the new binary
  over the running one, then asks systemd to restart the service.

## Health checks and the L7 proxy (not a straight port — these changed)

Two pieces of the original Go project were deliberately *not* carried over
unchanged, because the panel's inbound/outbound/binding model (see the
README's "Ядра" section) needs more than IPVS alone can express:

- **TCP health checks, not ICMP** (`HealthMonitor.checkOne`,
  `internal/agent/health.go`): a plain TCP dial to the outbound's exact
  `address:port`, not a ping to the host. A host can drop ICMP entirely
  while the service on that port is fine, or answer ICMP while the service
  itself is down — dialing the real port is what actually matters, and
  needs no raw-socket privilege. Results are keyed by `address:port`, not
  address alone, since two outbounds can share a host on different ports.
- **The L7 proxy** (`internal/proxy`): IPVS matches on destination
  `IP:port` only — it has no visibility into TLS SNI or HTTP Host/path, so
  it cannot express "route this SNI to pool A, that SNI to pool B" through
  one service. Bindings with match rules are instead enforced by a
  userspace proxy the reconciler starts alongside IPVS: one listener per
  Inbound, SNI-sniffed-and-relayed for `tcp` mode (`tcp_router.go`, never
  terminates TLS), a full `httputil.ReverseProxy` for `http` mode
  (`http_router.go`, matches Host+path). Both report live per-outbound
  connection counts, distinct client IPs, and cumulative bytes in each
  direction (`selector.go`'s `connCounters`) through `GET /v1/state`'s
  `outbound_stats` field; the panel derives a Мбит/с rate by diffing two
  consecutive polls (`Store.record_node_state`). Simple bindings — one
  inbound, one outbound, no match groups — are the only shape close enough
  to plain forwarding that IPVS's differential-apply/rollback machinery
  above still applies to them unmodified.

**Domain validation**: `validate_core_config()` (ported identically into
`ezhiklb_panel/domain.py` and the Go agent's `internal/domain/model.go`)
checks duplicate inbound/outbound/binding IDs, no two inbounds sharing a
listen host+port (wildcard-vs-specific address conflicts included), IPv4
outbound addresses, health-check bounds, that `path` match conditions only
appear on `http`-mode inbounds, and that manual-strategy binding weights
sum to 100 — before a core is ever sent to a node.

## Storage: SQLite or PostgreSQL

`ezhiklb_panel/db.py` builds an async SQLAlchemy engine from
`EZHIKLB_DATABASE_URL` (`sqlite+aiosqlite:///...` or
`postgresql+asyncpg://...`) and migrates the schema with
`CREATE TABLE IF NOT EXISTS` plus an `ensure_column()` helper that
branches on `engine.dialect.name` (`PRAGMA table_info` for SQLite,
`information_schema.columns` for PostgreSQL) — the same hand-rolled
migration philosophy upstream's `store.go` used, extended to be
dialect-aware instead of assuming SQLite. `scripts/install-panel.sh`
prompts for the choice at install time.

## Security boundary

Unchanged in spirit from upstream: the panel runs unprivileged and never
sends shell commands to a node, only validated structured desired state
(a `NodeApplyRequest`/`domain.NodeDesiredState`). Only the node agent runs
as root, because only it needs `CAP_NET_ADMIN`/`CAP_NET_RAW` to manage
IPVS and iptables. What's new is *who dials whom*: the panel is the
network client for the node's control API, and the pinned-certificate TLS
connection plus the node's own API key are what stand in for the old
node→panel bearer-token check.
