"""Async database engine + hand-rolled, dialect-agnostic schema migration.

Mirrors the upstream Go project's own migration philosophy in
internal/store/store.go (`CREATE TABLE IF NOT EXISTS` + an additive
`ensureColumn` helper) instead of pulling in Alembic — this schema is simple
enough that a full migration framework would be more machinery than the
problem needs, and it keeps the "how do I evolve the schema" story identical
to what upstream already proved out. The one new piece is dialect-awareness:
this fork supports both SQLite and PostgreSQL (point 5 of the fork request),
so `ensure_column` branches on `engine.dialect.name` instead of assuming
`PRAGMA table_info`.
"""

from __future__ import annotations

from datetime import datetime, timezone

from sqlalchemy import text
from sqlalchemy.ext.asyncio import AsyncEngine, AsyncSession, async_sessionmaker, create_async_engine


def now_iso() -> str:
    return datetime.now(timezone.utc).isoformat()


def parse_iso(value: str | None) -> datetime | None:
    if not value:
        return None
    return datetime.fromisoformat(value)


class Database:
    def __init__(self, database_url: str):
        self.database_url = database_url
        self.engine: AsyncEngine = create_async_engine(database_url, pool_pre_ping=True)
        self.is_sqlite = self.engine.dialect.name == "sqlite"
        self.is_postgres = self.engine.dialect.name == "postgresql"
        self.session_factory = async_sessionmaker(self.engine, expire_on_commit=False)

    async def close(self) -> None:
        await self.engine.dispose()

    def session(self) -> AsyncSession:
        return self.session_factory()

    # -- migration --------------------------------------------------------

    async def migrate(self) -> None:
        async with self.engine.begin() as conn:
            if self.is_sqlite:
                await conn.execute(text("PRAGMA journal_mode=WAL"))
                await conn.execute(text("PRAGMA foreign_keys=ON"))
                await conn.execute(text("PRAGMA busy_timeout=5000"))

            id_pk = "INTEGER PRIMARY KEY AUTOINCREMENT" if self.is_sqlite else "SERIAL PRIMARY KEY"

            statements = [
                f"""CREATE TABLE IF NOT EXISTS cores (
                    id TEXT PRIMARY KEY,
                    name TEXT NOT NULL UNIQUE,
                    description TEXT NOT NULL DEFAULT '',
                    current_revision INTEGER NOT NULL DEFAULT 0,
                    auto_version INTEGER NOT NULL DEFAULT 1,
                    version_label TEXT NOT NULL DEFAULT '',
                    created_at TEXT NOT NULL,
                    updated_at TEXT NOT NULL
                )""",
                f"""CREATE TABLE IF NOT EXISTS core_revisions (
                    id {id_pk},
                    core_id TEXT NOT NULL REFERENCES cores(id) ON DELETE CASCADE,
                    number INTEGER NOT NULL,
                    version_label TEXT NOT NULL DEFAULT '',
                    config_json TEXT NOT NULL,
                    created_at TEXT NOT NULL,
                    UNIQUE(core_id, number)
                )""",
                # api_key is stored in retrievable (plaintext) form rather
                # than hashed: the protocol inversion means the *panel* is
                # now the one presenting this credential as a Bearer token on
                # every outbound call to the node, so a one-way hash (useful
                # only for verifying a caller-supplied value) would make
                # outbound auth impossible. Same trust boundary as the admin
                # account's password hash: protected by the DB file's own
                # permissions, never sent anywhere except straight to the
                # pinned node.
                """CREATE TABLE IF NOT EXISTS nodes (
                    id TEXT PRIMARY KEY,
                    name TEXT NOT NULL UNIQUE,
                    ingress_address TEXT NOT NULL DEFAULT '',
                    control_address TEXT NOT NULL DEFAULT '',
                    control_port INTEGER NOT NULL DEFAULT 0,
                    api_key TEXT NOT NULL DEFAULT '',
                    cert_pem TEXT NOT NULL DEFAULT '',
                    cert_fingerprint TEXT NOT NULL DEFAULT '',
                    core_id TEXT REFERENCES cores(id) ON DELETE SET NULL,
                    desired_revision INTEGER NOT NULL DEFAULT 0,
                    applied_revision INTEGER NOT NULL DEFAULT 0,
                    agent_version TEXT NOT NULL DEFAULT '',
                    status TEXT NOT NULL DEFAULT 'connecting',
                    apply_state TEXT NOT NULL DEFAULT 'waiting',
                    apply_error TEXT NOT NULL DEFAULT '',
                    last_seen_at TEXT,
                    online_since TEXT,
                    ram_used_percent REAL NOT NULL DEFAULT 0,
                    cpu_used_percent REAL NOT NULL DEFAULT 0,
                    load_1 REAL NOT NULL DEFAULT 0,
                    cpu_cores INTEGER NOT NULL DEFAULT 0,
                    network_rx_bps INTEGER NOT NULL DEFAULT 0,
                    network_tx_bps INTEGER NOT NULL DEFAULT 0,
                    active_ips INTEGER NOT NULL DEFAULT 0,
                    metrics_collected_at TEXT,
                    diagnostics_json TEXT NOT NULL DEFAULT '{}',
                    update_target TEXT NOT NULL DEFAULT '',
                    update_state TEXT NOT NULL DEFAULT 'idle',
                    update_error TEXT NOT NULL DEFAULT '',
                    reset_revision INTEGER NOT NULL DEFAULT 0,
                    enabled INTEGER NOT NULL DEFAULT 1,
                    pending_delete INTEGER NOT NULL DEFAULT 0,
                    created_at TEXT NOT NULL,
                    updated_at TEXT NOT NULL
                )""",
                """CREATE TABLE IF NOT EXISTS audit_events (
                    id """ + id_pk + """,
                    action TEXT NOT NULL,
                    target_type TEXT NOT NULL,
                    target_id TEXT NOT NULL,
                    details_json TEXT NOT NULL DEFAULT '{}',
                    created_at TEXT NOT NULL
                )""",
                """CREATE TABLE IF NOT EXISTS backend_health (
                    node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
                    address TEXT NOT NULL,
                    state TEXT NOT NULL,
                    consecutive_successes INTEGER NOT NULL DEFAULT 0,
                    consecutive_failures INTEGER NOT NULL DEFAULT 0,
                    latency_millis INTEGER NOT NULL DEFAULT 0,
                    checked_at TEXT NOT NULL,
                    PRIMARY KEY(node_id, address)
                )""",
                """CREATE TABLE IF NOT EXISTS service_stats (
                    node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
                    protocol TEXT NOT NULL,
                    listen_address TEXT NOT NULL,
                    listen_port INTEGER NOT NULL,
                    backend_address TEXT NOT NULL DEFAULT '',
                    backend_port INTEGER NOT NULL DEFAULT 0,
                    connections INTEGER NOT NULL DEFAULT 0,
                    incoming_packets INTEGER NOT NULL DEFAULT 0,
                    outgoing_packets INTEGER NOT NULL DEFAULT 0,
                    incoming_bytes INTEGER NOT NULL DEFAULT 0,
                    outgoing_bytes INTEGER NOT NULL DEFAULT 0,
                    collected_at TEXT NOT NULL,
                    PRIMARY KEY(node_id, protocol, listen_address, listen_port, backend_address, backend_port)
                )""",
                # Multiple admins are allowed (managed via the `ezhik-lb
                # admins` CLI on the server, not the web UI). The very first
                # login, while this table is still empty, creates the first
                # row from whatever login+password is submitted.
                """CREATE TABLE IF NOT EXISTS admin_account (
                    id TEXT PRIMARY KEY,
                    username TEXT NOT NULL UNIQUE,
                    password_hash TEXT NOT NULL,
                    created_at TEXT NOT NULL,
                    updated_at TEXT NOT NULL
                )""",
                """CREATE TABLE IF NOT EXISTS sessions (
                    token TEXT PRIMARY KEY,
                    created_at TEXT NOT NULL,
                    expires_at TEXT NOT NULL
                )""",
                # bytes_rx/bytes_tx are the node's cumulative counters as of
                # the last poll — kept only to diff against the next poll.
                # rx_bps/tx_bps are the derived rate (bytes/sec) that
                # diffing produced, which is what the UI actually displays.
                """CREATE TABLE IF NOT EXISTS outbound_stats (
                    node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
                    outbound_id TEXT NOT NULL,
                    active_connections INTEGER NOT NULL DEFAULT 0,
                    active_ips INTEGER NOT NULL DEFAULT 0,
                    bytes_rx INTEGER NOT NULL DEFAULT 0,
                    bytes_tx INTEGER NOT NULL DEFAULT 0,
                    rx_bps REAL NOT NULL DEFAULT 0,
                    tx_bps REAL NOT NULL DEFAULT 0,
                    updated_at TEXT NOT NULL,
                    PRIMARY KEY(node_id, outbound_id)
                )""",
                """CREATE TABLE IF NOT EXISTS system_settings (
                    key TEXT PRIMARY KEY,
                    value TEXT NOT NULL,
                    updated_at TEXT NOT NULL
                )""",
                """CREATE TABLE IF NOT EXISTS node_metric_history (
                    node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
                    ram_used_percent REAL NOT NULL DEFAULT 0,
                    cpu_used_percent REAL NOT NULL DEFAULT 0,
                    load_1 REAL NOT NULL DEFAULT 0,
                    network_rx_bps INTEGER NOT NULL DEFAULT 0,
                    network_tx_bps INTEGER NOT NULL DEFAULT 0,
                    active_ips INTEGER NOT NULL DEFAULT 0,
                    collected_at TEXT NOT NULL,
                    PRIMARY KEY(node_id, collected_at)
                )""",
            ]
            for statement in statements:
                await conn.execute(text(statement))

            # Per-node overrides added after the initial schema — additive,
            # so existing databases pick them up via ensure_column instead
            # of a migration-file ladder (mirrors store.go's ensureColumn).
            await self.ensure_column(conn, "nodes", "poll_interval_seconds", "INTEGER")
            await self.ensure_column(conn, "nodes", "timeout_seconds", "INTEGER")
            await self.ensure_column(conn, "outbound_stats", "bytes_rx", "INTEGER NOT NULL DEFAULT 0")
            await self.ensure_column(conn, "outbound_stats", "bytes_tx", "INTEGER NOT NULL DEFAULT 0")
            await self.ensure_column(conn, "outbound_stats", "rx_bps", "REAL NOT NULL DEFAULT 0")
            await self.ensure_column(conn, "outbound_stats", "tx_bps", "REAL NOT NULL DEFAULT 0")

    async def ensure_column(self, conn, table: str, name: str, ddl_type: str) -> None:
        """Adds a column to an existing table if it isn't there yet. Safe to
        call every startup — this is how the schema evolves across releases
        without a migration-file ladder, matching store.go's ensureColumn."""
        if self.is_sqlite:
            rows = (await conn.execute(text(f"PRAGMA table_info({table})"))).mappings().all()
            existing = {row["name"] for row in rows}
        else:
            rows = (
                await conn.execute(
                    text("SELECT column_name FROM information_schema.columns WHERE table_name = :table"),
                    {"table": table},
                )
            ).all()
            existing = {row[0] for row in rows}
        if name not in existing:
            await conn.execute(text(f"ALTER TABLE {table} ADD COLUMN {name} {ddl_type}"))
