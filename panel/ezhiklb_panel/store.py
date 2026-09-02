"""Panel persistence layer — async repository functions mirroring every
public method of the upstream Go project's internal/store/store.go Store
type, renamed Profile -> Core, and adapted for the inverted node protocol:
node rows now carry a control address/port/API key/certificate (what the
panel needs to dial *out* to the node) instead of a credential hash (what
the old panel needed to verify a node dialing *in*), and there is no more
node_probe_requests nonce table — an on-demand health probe is now a direct,
synchronous RPC the API layer makes through node_client, not a stored flag
a polling agent picks up later.
"""

from __future__ import annotations

import json
import re
from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
from typing import Optional

from sqlalchemy import text
from sqlalchemy.ext.asyncio import AsyncConnection

from .db import Database, now_iso, parse_iso
from .domain import (
    CoreConfig,
    NodeDiagnostics,
    NodeMetrics,
    default_core_config,
    validate_core_config,
)
from .models import AuditEvent, Core, CoreRevision, Node, NodeMetricPoint, SystemSettings
from .security import new_id, new_token, sha256_hex

CORE_VERSION_PATTERN = re.compile(r"^[A-Za-z0-9][A-Za-z0-9.-]{0,63}$")
AUDIT_RETENTION = timedelta(days=14)
METRIC_HISTORY_RETENTION = timedelta(hours=24)
SESSION_TTL = timedelta(hours=12)


class NotFoundError(Exception):
    pass


class ConflictError(Exception):
    pass


def resolve_version(auto: bool, requested: str, number: int) -> str:
    if auto:
        return f"v{number}"
    requested = requested.strip()
    if not CORE_VERSION_PATTERN.match(requested):
        raise ConflictError("core version may contain only English letters, digits, dots and hyphens")
    return requested


@dataclass
class NewNodeCredential:
    node: Node
    api_key: str


class Store:
    def __init__(self, db: Database):
        self.db = db

    # -- bootstrap ----------------------------------------------------------

    async def bootstrap(self) -> None:
        async with self.db.engine.begin() as conn:
            count = (await conn.execute(text("SELECT COUNT(*) FROM cores"))).scalar_one()
            if count:
                return
            config = default_core_config()
            validate_core_config(config)
            core_id = new_id("core")
            now = now_iso()
            await conn.execute(
                text(
                    "INSERT INTO cores(id,name,description,current_revision,auto_version,version_label,created_at,updated_at) "
                    "VALUES(:id,:name,:description,1,1,'v1',:now,:now)"
                ),
                {"id": core_id, "name": "Default", "description": "Начальное ядро EzhikLB", "now": now},
            )
            await conn.execute(
                text(
                    "INSERT INTO core_revisions(core_id,number,version_label,config_json,created_at) "
                    "VALUES(:core_id,1,'v1',:config,:now)"
                ),
                {"core_id": core_id, "config": config.model_dump_json(), "now": now},
            )

    # -- admin account / sessions --------------------------------------------

    async def admin_count(self) -> int:
        async with self.db.engine.connect() as conn:
            return (await conn.execute(text("SELECT COUNT(*) FROM admin_account"))).scalar_one()

    async def list_admins(self) -> list[dict]:
        async with self.db.engine.connect() as conn:
            rows = (
                await conn.execute(text("SELECT id, username, created_at, updated_at FROM admin_account ORDER BY username"))
            ).mappings().all()
        return [dict(row) for row in rows]

    async def get_admin_by_username(self, username: str) -> Optional[dict]:
        async with self.db.engine.connect() as conn:
            row = (await conn.execute(text("SELECT * FROM admin_account WHERE username=:u"), {"u": username})).mappings().first()
        return dict(row) if row else None

    async def create_admin_account(self, username: str, password_hash: str) -> None:
        """First-ever call (empty table) is the web login's first-run flow;
        later calls are the `ezhik-lb admins add` CLI command."""
        now = now_iso()
        async with self.db.engine.begin() as conn:
            existing = (await conn.execute(text("SELECT 1 FROM admin_account WHERE username=:u"), {"u": username})).first()
            if existing is not None:
                raise ConflictError(f"admin '{username}' already exists")
            await conn.execute(
                text("INSERT INTO admin_account(id,username,password_hash,created_at,updated_at) VALUES(:id,:u,:p,:now,:now)"),
                {"id": new_id("admin"), "u": username, "p": password_hash, "now": now},
            )

    async def update_admin_password(self, username: str, password_hash: str) -> None:
        async with self.db.engine.begin() as conn:
            result = await conn.execute(
                text("UPDATE admin_account SET password_hash=:p, updated_at=:now WHERE username=:u"),
                {"p": password_hash, "now": now_iso(), "u": username},
            )
            if result.rowcount == 0:
                raise NotFoundError(f"admin '{username}' not found")

    async def delete_admin(self, username: str) -> None:
        async with self.db.engine.begin() as conn:
            exists = (await conn.execute(text("SELECT 1 FROM admin_account WHERE username=:u"), {"u": username})).first()
            if exists is None:
                raise NotFoundError(f"admin '{username}' not found")
            count = (await conn.execute(text("SELECT COUNT(*) FROM admin_account"))).scalar_one()
            if count <= 1:
                raise ConflictError("cannot remove the last remaining admin")
            await conn.execute(text("DELETE FROM admin_account WHERE username=:u"), {"u": username})

    async def create_session(self) -> str:
        token = new_token(32)
        now = datetime.now(timezone.utc)
        async with self.db.engine.begin() as conn:
            await conn.execute(text("DELETE FROM sessions WHERE expires_at<:now"), {"now": now.isoformat()})
            await conn.execute(
                text("INSERT INTO sessions(token,created_at,expires_at) VALUES(:t,:now,:exp)"),
                {"t": token, "now": now.isoformat(), "exp": (now + SESSION_TTL).isoformat()},
            )
        return token

    async def session_valid(self, token: str) -> bool:
        async with self.db.engine.connect() as conn:
            row = (await conn.execute(text("SELECT expires_at FROM sessions WHERE token=:t"), {"t": token})).first()
        return row is not None and parse_iso(row[0]) > datetime.now(timezone.utc)

    async def delete_session(self, token: str) -> None:
        await self._exec("DELETE FROM sessions WHERE token=:t", {"t": token})

    # -- cores ----------------------------------------------------------------

    async def list_cores(self) -> list[Core]:
        async with self.db.engine.connect() as conn:
            rows = (await conn.execute(text("SELECT * FROM cores ORDER BY name"))).mappings().all()
        return [_row_to_core(row) for row in rows]

    async def get_core(self, core_id: str) -> tuple[Core, CoreRevision]:
        async with self.db.engine.connect() as conn:
            row = (await conn.execute(text("SELECT * FROM cores WHERE id=:id"), {"id": core_id})).mappings().first()
            if row is None:
                raise NotFoundError("core not found")
            core = _row_to_core(row)
            revision = await self._get_revision(conn, core_id, core.current_revision)
        return core, revision

    async def create_core(
        self, name: str, description: str, config: CoreConfig, auto_version: bool, requested_version: str
    ) -> tuple[Core, CoreRevision]:
        validate_core_config(config)
        version = resolve_version(auto_version, requested_version, 1)
        core_id = new_id("core")
        now = now_iso()
        async with self.db.engine.begin() as conn:
            await conn.execute(
                text(
                    "INSERT INTO cores(id,name,description,current_revision,auto_version,version_label,created_at,updated_at) "
                    "VALUES(:id,:name,:description,1,:auto,:version,:now,:now)"
                ),
                {"id": core_id, "name": name, "description": description, "auto": int(auto_version), "version": version, "now": now},
            )
            await conn.execute(
                text(
                    "INSERT INTO core_revisions(core_id,number,version_label,config_json,created_at) "
                    "VALUES(:core_id,1,:version,:config,:now)"
                ),
                {"core_id": core_id, "version": version, "config": config.model_dump_json(), "now": now},
            )
            await self._audit(conn, "core.created", "core", core_id, {"name": name, "version": version})
        return await self.get_core(core_id)

    async def publish_core_revision(
        self,
        core_id: str,
        name: str,
        description: str,
        config: CoreConfig,
        auto_version: bool,
        requested_version: str,
        reset_connections: bool,
    ) -> tuple[Core, CoreRevision]:
        validate_core_config(config)
        async with self.db.engine.begin() as conn:
            row = (
                await conn.execute(text("SELECT current_revision, version_label FROM cores WHERE id=:id"), {"id": core_id})
            ).first()
            if row is None:
                raise NotFoundError("core not found")
            current, current_version = row[0], row[1]
            next_number = current + 1
            version = resolve_version(auto_version, requested_version, next_number)
            if version == current_version:
                raise ConflictError("core version must change before publishing")

            duplicate = (
                await conn.execute(
                    text("SELECT COUNT(*) FROM core_revisions WHERE core_id=:id AND version_label=:v"),
                    {"id": core_id, "v": version},
                )
            ).scalar_one()
            if duplicate:
                raise ConflictError("core version is already used")

            now = now_iso()
            await conn.execute(
                text(
                    "INSERT INTO core_revisions(core_id,number,version_label,config_json,created_at) "
                    "VALUES(:core_id,:number,:version,:config,:now)"
                ),
                {"core_id": core_id, "number": next_number, "version": version, "config": config.model_dump_json(), "now": now},
            )
            await conn.execute(
                text(
                    "UPDATE cores SET name=:name, description=:description, current_revision=:next, "
                    "auto_version=:auto, version_label=:version, updated_at=:now WHERE id=:id"
                ),
                {"name": name, "description": description, "next": next_number, "auto": int(auto_version), "version": version, "now": now, "id": core_id},
            )
            result = await conn.execute(
                text(
                    "UPDATE nodes SET desired_revision=:next, reset_revision=CASE WHEN :reset THEN :next ELSE 0 END, "
                    "updated_at=:now WHERE core_id=:id"
                ),
                {"next": next_number, "reset": int(reset_connections), "now": now, "id": core_id},
            )
            assigned = result.rowcount
            await self._audit(
                conn, "core.published", "core", core_id,
                {"name": name, "version": version, "reset_connections": reset_connections, "assigned_nodes": assigned},
            )
        return await self.get_core(core_id)

    async def clone_core(self, core_id: str, name: str) -> tuple[Core, CoreRevision]:
        core, revision = await self.get_core(core_id)
        if not name:
            name = f"{core.name} — копия"
        return await self.create_core(name, core.description, revision.config, core.auto_version, revision.version)

    async def delete_core(self, core_id: str) -> None:
        async with self.db.engine.begin() as conn:
            assigned = (
                await conn.execute(text("SELECT COUNT(*) FROM nodes WHERE core_id=:id"), {"id": core_id})
            ).scalar_one()
            if assigned:
                raise ConflictError(f"core is assigned to {assigned} node(s)")
            result = await conn.execute(text("DELETE FROM cores WHERE id=:id"), {"id": core_id})
            if result.rowcount == 0:
                raise NotFoundError("core not found")
            await self._audit(conn, "core.deleted", "core", core_id, {})

    async def _get_revision(self, conn: AsyncConnection, core_id: str, number: int) -> CoreRevision:
        row = (
            await conn.execute(
                text("SELECT * FROM core_revisions WHERE core_id=:id AND number=:n"), {"id": core_id, "n": number}
            )
        ).mappings().first()
        if row is None:
            raise NotFoundError("core revision not found")
        return _row_to_revision(row)

    # -- nodes ----------------------------------------------------------------

    async def list_nodes(self, offline_after_seconds: int) -> list[Node]:
        async with self.db.engine.connect() as conn:
            rows = (await conn.execute(text("SELECT * FROM nodes ORDER BY name"))).mappings().all()
        now = datetime.now(timezone.utc)
        nodes = []
        for row in rows:
            node = _row_to_node(row)
            if node.status not in ("disabled", "deleting"):
                if node.last_seen_at is None:
                    node.status = "connecting"
                elif (now - node.last_seen_at) > timedelta(seconds=offline_after_seconds):
                    node.status = "offline"
                    node.online_since = None
            nodes.append(node)
        return nodes

    async def get_node(self, node_id: str) -> Node:
        async with self.db.engine.connect() as conn:
            row = (await conn.execute(text("SELECT * FROM nodes WHERE id=:id"), {"id": node_id})).mappings().first()
        if row is None:
            raise NotFoundError("node not found")
        return _row_to_node(row)

    async def node_dial_info(self, node_id: str) -> dict:
        """Everything node_client needs to reach a node: address, port, the
        plaintext API key it presents as a Bearer token, and the pinned
        certificate PEM it trusts as that node's sole CA."""
        async with self.db.engine.connect() as conn:
            row = (
                await conn.execute(
                    text("SELECT control_address, control_port, api_key, cert_pem FROM nodes WHERE id=:id"),
                    {"id": node_id},
                )
            ).mappings().first()
        if row is None:
            raise NotFoundError("node not found")
        return dict(row)

    async def create_node(
        self, name: str, ingress_address: str, control_address: str, control_port: int, api_key: str, cert_pem: str, core_id: str,
        poll_interval_seconds: Optional[int] = None, timeout_seconds: Optional[int] = None,
    ) -> Node:
        async with self.db.engine.begin() as conn:
            revision = (
                await conn.execute(text("SELECT current_revision FROM cores WHERE id=:id"), {"id": core_id})
            ).scalar_one_or_none()
            if revision is None:
                raise NotFoundError("core not found")
            node_id = new_id("node")
            now = now_iso()
            await conn.execute(
                text(
                    "INSERT INTO nodes(id,name,ingress_address,control_address,control_port,api_key,cert_pem,cert_fingerprint,"
                    "core_id,desired_revision,status,apply_state,poll_interval_seconds,timeout_seconds,created_at,updated_at) "
                    "VALUES(:id,:name,:ingress,:addr,:port,:key,:cert,:fp,:core_id,:revision,'connecting','waiting',"
                    ":poll_interval,:timeout,:now,:now)"
                ),
                {
                    "id": node_id, "name": name, "ingress": ingress_address, "addr": control_address, "port": control_port,
                    "key": api_key, "cert": cert_pem, "fp": _fingerprint(cert_pem), "core_id": core_id, "revision": revision,
                    "poll_interval": poll_interval_seconds, "timeout": timeout_seconds, "now": now,
                },
            )
            await self._audit(conn, "node.created", "node", node_id, {"core_id": core_id})
        return await self.get_node(node_id)

    async def update_node(
        self, node_id: str, name: str, ingress_address: str, control_address: str, control_port: int,
        api_key: Optional[str] = None, cert_pem: Optional[str] = None,
        poll_interval_seconds: Optional[int] = None, timeout_seconds: Optional[int] = None,
    ) -> None:
        async with self.db.engine.begin() as conn:
            fields = {
                "name": name, "ingress": ingress_address, "addr": control_address, "port": control_port,
                "poll_interval": poll_interval_seconds, "timeout": timeout_seconds,
                "now": now_iso(), "id": node_id,
            }
            extra_sql = ""
            if api_key:
                fields["key"] = api_key
                extra_sql += ", api_key=:key"
            if cert_pem:
                fields["cert"] = cert_pem
                fields["fp"] = _fingerprint(cert_pem)
                extra_sql += ", cert_pem=:cert, cert_fingerprint=:fp"
            result = await conn.execute(
                text(
                    "UPDATE nodes SET name=:name, ingress_address=:ingress, control_address=:addr, control_port=:port, "
                    "poll_interval_seconds=:poll_interval, timeout_seconds=:timeout"
                    + extra_sql + ", updated_at=:now WHERE id=:id"
                ),
                fields,
            )
            if result.rowcount == 0:
                raise NotFoundError("node not found")
            await self._audit(conn, "node.updated", "node", node_id, {"name": name})

    async def delete_node(self, node_id: str) -> None:
        async with self.db.engine.begin() as conn:
            result = await conn.execute(
                text(
                    "UPDATE nodes SET status='deleting', apply_state='decommissioning', apply_error='', "
                    "pending_delete=1, updated_at=:now WHERE id=:id"
                ),
                {"now": now_iso(), "id": node_id},
            )
            if result.rowcount == 0:
                raise NotFoundError("node not found")
            await self._audit(conn, "node.decommission_requested", "node", node_id, {})

    async def force_delete_node(self, node_id: str) -> None:
        async with self.db.engine.begin() as conn:
            result = await conn.execute(text("DELETE FROM nodes WHERE id=:id AND status='deleting'"), {"id": node_id})
            if result.rowcount == 0:
                exists = (await conn.execute(text("SELECT COUNT(*) FROM nodes WHERE id=:id"), {"id": node_id})).scalar_one()
                if not exists:
                    raise NotFoundError("node not found")
                raise ConflictError("node is not pending deletion")
            await self._audit(conn, "node.force_deleted", "node", node_id, {})

    async def finalize_decommission(self, node_id: str, agent_version: str) -> None:
        """Called by the poller once a node's POST /v1/decommission RPC
        succeeds — deletes the row only after that acknowledgement, same
        "acknowledged decommission" guarantee the upstream project had."""
        async with self.db.engine.begin() as conn:
            await self._audit(conn, "node.decommissioned", "node", node_id, {"agent_version": agent_version})
            await conn.execute(text("DELETE FROM nodes WHERE id=:id"), {"id": node_id})

    async def set_node_enabled(self, node_id: str, enabled: bool) -> None:
        status = "connecting" if enabled else "disabled"
        apply_state = "waiting" if enabled else "disabled"
        async with self.db.engine.begin() as conn:
            result = await conn.execute(
                text(
                    "UPDATE nodes SET enabled=:enabled, status=:status, apply_state=:apply_state, apply_error='', "
                    "online_since=NULL, updated_at=:now WHERE id=:id"
                ),
                {"enabled": int(enabled), "status": status, "apply_state": apply_state, "now": now_iso(), "id": node_id},
            )
            if result.rowcount == 0:
                raise NotFoundError("node not found")
            await self._audit(conn, "node.enabled_changed", "node", node_id, {"enabled": enabled})

    async def rotate_node_credential(self, node_id: str) -> str:
        token = new_token()
        async with self.db.engine.begin() as conn:
            result = await conn.execute(
                text("UPDATE nodes SET api_key=:key, status='connecting', apply_error='', updated_at=:now WHERE id=:id"),
                {"key": token, "now": now_iso(), "id": node_id},
            )
            if result.rowcount == 0:
                raise NotFoundError("node not found")
            await self._audit(conn, "node.credential_rotated", "node", node_id, {})
        return token

    async def assign_core(self, node_id: str, core_id: str) -> None:
        async with self.db.engine.begin() as conn:
            revision = (
                await conn.execute(text("SELECT current_revision FROM cores WHERE id=:id"), {"id": core_id})
            ).scalar_one_or_none()
            if revision is None:
                raise NotFoundError("core not found")
            result = await conn.execute(
                text("UPDATE nodes SET core_id=:core_id, desired_revision=:revision, reset_revision=0, updated_at=:now WHERE id=:id"),
                {"core_id": core_id, "revision": revision, "now": now_iso(), "id": node_id},
            )
            if result.rowcount == 0:
                raise NotFoundError("node not found")
            await self._audit(conn, "node.core_assigned", "node", node_id, {"core_id": core_id, "revision": revision})

    async def request_node_update(self, node_id: str, version: str) -> None:
        async with self.db.engine.begin() as conn:
            result = await conn.execute(
                text("UPDATE nodes SET update_target=:v, update_state='requested', update_error='', updated_at=:now WHERE id=:id"),
                {"v": version, "now": now_iso(), "id": node_id},
            )
            if result.rowcount == 0:
                raise NotFoundError("node not found")
            await self._audit(conn, "node.update_requested", "node", node_id, {"version": version})

    async def desired_state_for_node(self, node_id: str) -> Optional[dict]:
        """What the poller should POST to /v1/apply, or None if the node has
        no core assigned yet. Field names match the node agent's unmodified
        Go domain.NodeDesiredState wire struct — see domain.NodeApplyRequest."""
        async with self.db.engine.connect() as conn:
            row = (
                await conn.execute(
                    text(
                        "SELECT n.ingress_address, n.desired_revision, n.reset_revision=n.desired_revision AS reset, "
                        "c.id AS core_id, c.name AS core_name, r.config_json FROM nodes n "
                        "JOIN cores c ON c.id=n.core_id JOIN core_revisions r ON r.core_id=c.id AND r.number=n.desired_revision "
                        "WHERE n.id=:id"
                    ),
                    {"id": node_id},
                )
            ).mappings().first()
        if row is None:
            return None
        return {
            "ingress_address": row["ingress_address"],
            "revision": row["desired_revision"],
            "profile_id": row["core_id"],
            "profile_name": row["core_name"],
            "reset_connections": bool(row["reset"]),
            "config": json.loads(row["config_json"]),
        }

    async def acknowledge_reset(self, node_id: str, applied_revision: int) -> None:
        await self._exec(
            "UPDATE nodes SET reset_revision=0 WHERE id=:id AND reset_revision>0 AND :applied>=reset_revision",
            {"id": node_id, "applied": applied_revision},
        )

    async def record_node_state(
        self,
        node_id: str,
        *,
        agent_version: str,
        apply_state: str,
        applied_revision: int,
        apply_error: str,
        health: list[dict],
        stats: list[dict],
        outbound_stats: list[dict],
        metrics: NodeMetrics,
        diagnostics: Optional[NodeDiagnostics],
        update_state: str,
        update_error: str,
    ) -> None:
        """Persists one successful GET /v1/state poll — the pull-based
        analogue of the old POST .../heartbeat handler."""
        now = datetime.now(timezone.utc)
        async with self.db.engine.begin() as conn:
            row = (
                await conn.execute(
                    text("SELECT status, last_seen_at, online_since, apply_error, update_target FROM nodes WHERE id=:id"),
                    {"id": node_id},
                )
            ).mappings().first()
            if row is None:
                raise NotFoundError("node not found")
            previous_status, previous_seen, previous_online, previous_error = (
                row["status"], parse_iso(row["last_seen_at"]), row["online_since"], row["apply_error"]
            )
            update_target = row["update_target"]

            status = "online" if previous_status not in ("disabled",) else previous_status
            online_since = previous_online
            if not previous_online or previous_seen is None or previous_status in ("offline", "connecting"):
                online_since = now_iso()
            elif previous_seen is not None and (now - previous_seen) > timedelta(seconds=45):
                online_since = now_iso()

            if update_target and update_state == "":
                update_state = "idle"
            diagnostics_json = diagnostics.model_dump_json() if diagnostics else "{}"

            await conn.execute(
                text(
                    "UPDATE nodes SET applied_revision=:applied, agent_version=:version, status=:status, "
                    "apply_state=:apply_state, apply_error=:apply_error, last_seen_at=:now, online_since=:online_since, "
                    "ram_used_percent=:ram, cpu_used_percent=:cpu, load_1=:load1, cpu_cores=:cores, "
                    "network_rx_bps=:rx, network_tx_bps=:tx, active_ips=:active_ips, metrics_collected_at=:now, "
                    "diagnostics_json=:diag, update_state=:update_state, update_error=:update_error, updated_at=:now "
                    "WHERE id=:id"
                ),
                {
                    "applied": applied_revision, "version": agent_version, "status": status, "apply_state": apply_state,
                    "apply_error": apply_error, "now": now_iso(), "online_since": online_since,
                    "ram": metrics.ram_used_percent, "cpu": metrics.cpu_used_percent, "load1": metrics.load_1,
                    "cores": metrics.cpu_cores, "rx": metrics.network_rx_bps, "tx": metrics.network_tx_bps,
                    "active_ips": metrics.active_ips, "diag": diagnostics_json, "update_state": update_state,
                    "update_error": update_error, "id": node_id,
                },
            )
            await conn.execute(
                text("UPDATE nodes SET reset_revision=0 WHERE id=:id AND reset_revision>0 AND :applied>=reset_revision"),
                {"id": node_id, "applied": applied_revision},
            )
            if apply_error and apply_error != previous_error:
                await self._audit(conn, "node.apply_failed", "node", node_id, {"error": apply_error, "revision": applied_revision})
            elif not apply_error and previous_error:
                await self._audit(conn, "node.apply_recovered", "node", node_id, {"revision": applied_revision})

            metric_minute = now.replace(second=0, microsecond=0)
            await conn.execute(
                text(
                    "DELETE FROM node_metric_history WHERE node_id=:id AND collected_at=:minute"
                ),
                {"id": node_id, "minute": metric_minute.isoformat()},
            )
            await conn.execute(
                text(
                    "INSERT INTO node_metric_history(node_id,ram_used_percent,cpu_used_percent,load_1,network_rx_bps,network_tx_bps,active_ips,collected_at) "
                    "VALUES(:id,:ram,:cpu,:load1,:rx,:tx,:active_ips,:minute)"
                ),
                {
                    "id": node_id, "ram": metrics.ram_used_percent, "cpu": metrics.cpu_used_percent, "load1": metrics.load_1,
                    "rx": metrics.network_rx_bps, "tx": metrics.network_tx_bps, "active_ips": metrics.active_ips,
                    "minute": metric_minute.isoformat(),
                },
            )
            await conn.execute(
                text("DELETE FROM node_metric_history WHERE collected_at<:cutoff"),
                {"cutoff": (now - METRIC_HISTORY_RETENTION).isoformat()},
            )

            await conn.execute(text("DELETE FROM backend_health WHERE node_id=:id"), {"id": node_id})
            for item in health:
                await conn.execute(
                    text(
                        "INSERT INTO backend_health(node_id,address,state,consecutive_successes,consecutive_failures,latency_millis,checked_at) "
                        "VALUES(:id,:address,:state,:up,:down,:latency,:now)"
                    ),
                    {
                        "id": node_id, "address": item["address"], "state": item.get("state", "unknown"),
                        "up": item.get("consecutive_successes", 0), "down": item.get("consecutive_failures", 0),
                        "latency": item.get("latency_millis", 0), "now": now_iso(),
                    },
                )
            await conn.execute(text("DELETE FROM service_stats WHERE node_id=:id"), {"id": node_id})
            for item in stats:
                await conn.execute(
                    text(
                        "INSERT INTO service_stats(node_id,protocol,listen_address,listen_port,backend_address,backend_port,"
                        "connections,incoming_packets,outgoing_packets,incoming_bytes,outgoing_bytes,collected_at) "
                        "VALUES(:id,:protocol,:laddr,:lport,:baddr,:bport,:conn,:ipkt,:opkt,:ibytes,:obytes,:now)"
                    ),
                    {
                        "id": node_id, "protocol": item["protocol"], "laddr": item["listen_address"], "lport": item["listen_port"],
                        "baddr": item.get("backend_address", ""), "bport": item.get("backend_port", 0),
                        "conn": item.get("connections", 0), "ipkt": item.get("incoming_packets", 0),
                        "opkt": item.get("outgoing_packets", 0), "ibytes": item.get("incoming_bytes", 0),
                        "obytes": item.get("outgoing_bytes", 0), "now": now_iso(),
                    },
                )

            previous_outbound_rows = (
                await conn.execute(text("SELECT outbound_id, bytes_rx, bytes_tx, updated_at FROM outbound_stats WHERE node_id=:id"), {"id": node_id})
            ).mappings().all()
            previous_outbound = {row["outbound_id"]: row for row in previous_outbound_rows}
            await conn.execute(text("DELETE FROM outbound_stats WHERE node_id=:id"), {"id": node_id})
            for item in outbound_stats:
                outbound_id = item["outbound_id"]
                bytes_rx, bytes_tx = item.get("bytes_rx", 0), item.get("bytes_tx", 0)
                rx_bps = tx_bps = 0.0
                previous = previous_outbound.get(outbound_id)
                if previous is not None:
                    elapsed = (now - parse_iso(previous["updated_at"])).total_seconds()
                    if elapsed > 0:
                        rx_bps = max(0.0, (bytes_rx - previous["bytes_rx"]) / elapsed)
                        tx_bps = max(0.0, (bytes_tx - previous["bytes_tx"]) / elapsed)
                await conn.execute(
                    text(
                        "INSERT INTO outbound_stats(node_id,outbound_id,active_connections,active_ips,bytes_rx,bytes_tx,rx_bps,tx_bps,updated_at) "
                        "VALUES(:id,:outbound_id,:conn,:ips,:bytes_rx,:bytes_tx,:rx_bps,:tx_bps,:now)"
                    ),
                    {
                        "id": node_id, "outbound_id": outbound_id, "conn": item.get("active_connections", 0), "ips": item.get("active_ips", 0),
                        "bytes_rx": bytes_rx, "bytes_tx": bytes_tx, "rx_bps": rx_bps, "tx_bps": tx_bps, "now": now_iso(),
                    },
                )

    async def set_node_offline(self, node_id: str) -> None:
        await self._exec(
            "UPDATE nodes SET status='offline', online_since=NULL, updated_at=:now WHERE id=:id AND status NOT IN ('disabled','deleting')",
            {"now": now_iso(), "id": node_id},
        )

    async def list_health(self) -> list[dict]:
        async with self.db.engine.connect() as conn:
            rows = (await conn.execute(text("SELECT * FROM backend_health ORDER BY node_id, address"))).mappings().all()
        return [dict(row) for row in rows]

    async def list_stats(self) -> list[dict]:
        async with self.db.engine.connect() as conn:
            rows = (
                await conn.execute(
                    text("SELECT * FROM service_stats ORDER BY node_id, protocol, listen_address, listen_port, backend_address, backend_port")
                )
            ).mappings().all()
        return [dict(row) for row in rows]

    async def list_outbound_stats(self) -> list[dict]:
        async with self.db.engine.connect() as conn:
            rows = (await conn.execute(text("SELECT * FROM outbound_stats"))).mappings().all()
        return [dict(row) for row in rows]

    async def list_metric_history(self, node_id: str) -> list[NodeMetricPoint]:
        cutoff = (datetime.now(timezone.utc) - METRIC_HISTORY_RETENTION).isoformat()
        query = "SELECT * FROM node_metric_history WHERE collected_at>=:cutoff"
        params: dict = {"cutoff": cutoff}
        if node_id and node_id != "all":
            query += " AND node_id=:node_id"
            params["node_id"] = node_id
        query += " ORDER BY collected_at"
        async with self.db.engine.connect() as conn:
            rows = (await conn.execute(text(query), params)).mappings().all()
        return [
            NodeMetricPoint(
                node_id=row["node_id"], ram_used_percent=row["ram_used_percent"], cpu_used_percent=row["cpu_used_percent"],
                load_1=row["load_1"], network_rx_bps=row["network_rx_bps"], network_tx_bps=row["network_tx_bps"],
                active_ips=row["active_ips"], collected_at=parse_iso(row["collected_at"]),
            )
            for row in rows
        ]

    # -- settings ---------------------------------------------------------

    async def get_system_settings(self, defaults: SystemSettings) -> SystemSettings:
        async with self.db.engine.connect() as conn:
            rows = (
                await conn.execute(text("SELECT key, value FROM system_settings WHERE key IN ('panel_port')"))
            ).all()
        result = defaults.model_copy()
        for key, value in rows:
            try:
                parsed = int(value)
            except ValueError:
                continue
            if key == "panel_port":
                result.panel_port = parsed
        result.db_backend = "postgresql" if self.db.is_postgres else "sqlite"
        return result

    async def update_system_settings(self, settings: SystemSettings) -> None:
        if not (1024 <= settings.panel_port <= 65535):
            raise ConflictError("panel_port must be between 1024 and 65535")
        now = now_iso()
        async with self.db.engine.begin() as conn:
            await conn.execute(
                text(
                    "INSERT INTO system_settings(key,value,updated_at) VALUES('panel_port',:value,:now) "
                    "ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at"
                ),
                {"value": str(settings.panel_port), "now": now},
            )
            await self._audit(conn, "settings.updated", "system", "network", {"panel_port": settings.panel_port})

    # -- audit --------------------------------------------------------------

    async def audit(self, action: str, target_type: str, target_id: str, details: dict) -> None:
        async with self.db.engine.begin() as conn:
            await self._audit(conn, action, target_type, target_id, details)

    async def _audit(self, conn: AsyncConnection, action: str, target_type: str, target_id: str, details: dict) -> None:
        cutoff = (datetime.now(timezone.utc) - AUDIT_RETENTION).isoformat()
        await conn.execute(text("DELETE FROM audit_events WHERE created_at<:cutoff"), {"cutoff": cutoff})
        await conn.execute(
            text("INSERT INTO audit_events(action,target_type,target_id,details_json,created_at) VALUES(:a,:t,:i,:d,:now)"),
            {"a": action, "t": target_type, "i": target_id, "d": json.dumps(details, default=str), "now": now_iso()},
        )

    async def list_audit(self, filter: str, limit: int = 200) -> list[AuditEvent]:
        if not (1 <= limit <= 500):
            limit = 200
        cutoff = (datetime.now(timezone.utc) - AUDIT_RETENTION).isoformat()
        query = "SELECT * FROM audit_events"
        clause = {
            "nodes": " WHERE target_type='node' OR action LIKE 'backend.%'",
            "profiles": " WHERE target_type='core'",
            "errors": " WHERE action LIKE '%failed%' OR action LIKE '%error%'",
        }.get(filter, "")
        query += clause + " ORDER BY id DESC LIMIT :limit"
        async with self.db.engine.begin() as conn:
            await conn.execute(text("DELETE FROM audit_events WHERE created_at<:cutoff"), {"cutoff": cutoff})
            rows = (await conn.execute(text(query), {"limit": limit})).mappings().all()
        return [
            AuditEvent(id=row["id"], action=row["action"], target_type=row["target_type"], target_id=row["target_id"],
                       details=row["details_json"], created_at=parse_iso(row["created_at"]))
            for row in rows
        ]

    async def _exec(self, sql: str, params: dict) -> None:
        async with self.db.engine.begin() as conn:
            await conn.execute(text(sql), params)


def _fingerprint(cert_pem: str) -> str:
    from .security import cert_fingerprint

    try:
        return cert_fingerprint(cert_pem)
    except Exception:
        return sha256_hex(cert_pem)


def _row_to_core(row) -> Core:
    return Core(
        id=row["id"], name=row["name"], description=row["description"], current_revision=row["current_revision"],
        auto_version=bool(row["auto_version"]), version=row["version_label"],
        created_at=parse_iso(row["created_at"]), updated_at=parse_iso(row["updated_at"]),
    )


def _row_to_revision(row) -> CoreRevision:
    return CoreRevision(
        id=row["id"], core_id=row["core_id"], number=row["number"], version=row["version_label"],
        config=CoreConfig.model_validate_json(row["config_json"]), created_at=parse_iso(row["created_at"]),
    )


def _row_to_node(row) -> Node:
    metrics = None
    if row["metrics_collected_at"]:
        metrics = NodeMetrics(
            ram_used_percent=row["ram_used_percent"], cpu_used_percent=row["cpu_used_percent"], load_1=row["load_1"],
            cpu_cores=row["cpu_cores"], network_rx_bps=row["network_rx_bps"], network_tx_bps=row["network_tx_bps"],
            active_ips=row["active_ips"], collected_at=parse_iso(row["metrics_collected_at"]),
        )
    diagnostics = None
    raw_diag = row["diagnostics_json"]
    if raw_diag and raw_diag != "{}":
        try:
            diagnostics = NodeDiagnostics.model_validate_json(raw_diag)
        except Exception:
            diagnostics = None
    return Node(
        id=row["id"], name=row["name"], ingress_address=row["ingress_address"], control_address=row["control_address"],
        control_port=row["control_port"], cert_fingerprint=row["cert_fingerprint"], core_id=row["core_id"] or "",
        desired_revision=row["desired_revision"], applied_revision=row["applied_revision"], agent_version=row["agent_version"],
        status=row["status"], apply_state=row["apply_state"], apply_error=row["apply_error"],
        last_seen_at=parse_iso(row["last_seen_at"]), online_since=parse_iso(row["online_since"]),
        metrics=metrics, diagnostics=diagnostics, update_target=row["update_target"], update_state=row["update_state"],
        update_error=row["update_error"], enabled=bool(row["enabled"]),
        poll_interval_seconds=row["poll_interval_seconds"], timeout_seconds=row["timeout_seconds"],
        created_at=parse_iso(row["created_at"]), updated_at=parse_iso(row["updated_at"]),
    )
