"""FastAPI routers for the panel's admin API. Mirrors the upstream Go
project's internal/api/server.go endpoint surface, renamed profiles -> cores,
with the node endpoints reworked for one-step enrollment (point 4) and
on-demand actions (health-probe, connectivity check) now direct synchronous
RPCs through the poller instead of the old nonce-polling/heartbeat tricks.
"""

from __future__ import annotations

import asyncio
from typing import Optional

from fastapi import APIRouter, Depends, HTTPException, Request, Response
from pydantic import BaseModel, Field

from .config import PANEL_VERSION, Settings
from .domain import CoreConfig
from .models import SystemSettings
from .node_client import NodeRejectedError, NodeUnreachableError
from .poller import Poller
from .security import hash_password, verify_password
from .store import ConflictError, NotFoundError, Store

router = APIRouter()


# ---------------------------------------------------------------------------
# Dependencies
# ---------------------------------------------------------------------------


def get_store(request: Request) -> Store:
    return request.app.state.store


def get_poller(request: Request) -> Poller:
    return request.app.state.poller


def get_settings(request: Request) -> Settings:
    return request.app.state.settings


async def require_admin(request: Request, store: Store = Depends(get_store)) -> None:
    cookie = request.cookies.get("ezhiklb_session")
    if not cookie or not await store.session_valid(cookie):
        raise HTTPException(status_code=401, detail="Authentication required")


admin = Depends(require_admin)


def _api_error(status_code: int, code: str, message: str) -> HTTPException:
    return HTTPException(status_code=status_code, detail={"error": {"code": code, "message": message}})


# ---------------------------------------------------------------------------
# Auth
# ---------------------------------------------------------------------------


class LoginRequest(BaseModel):
    login: str = ""
    password: str = ""


@router.get("/api/v1/auth/setup-required")
async def setup_required(store: Store = Depends(get_store)):
    """Lets the login page decide, before the admin types anything, whether
    to render a "create your account" or a plain "log in" form."""
    return {"setup_required": await store.admin_count() == 0}


@router.post("/api/v1/auth/login")
async def login(body: LoginRequest, response: Response, settings: Settings = Depends(get_settings), store: Store = Depends(get_store)):
    login_value = body.login.strip()
    if await store.admin_count() == 0:
        # First login ever: whatever login+password is submitted here
        # becomes the first admin account, stored (password hashed) in the
        # database. Further admins are only ever added via the server-side
        # `ezhik-lb admins add` CLI, not through the web UI.
        if len(login_value) < 3:
            raise _api_error(422, "invalid_login", "Логин должен быть не короче 3 символов")
        if len(body.password) < 8:
            raise _api_error(422, "invalid_password", "Пароль должен быть не короче 8 символов")
        await store.create_admin_account(login_value, hash_password(body.password))
    else:
        account = await store.get_admin_by_username(login_value)
        valid = account is not None and verify_password(body.password, account["password_hash"])
        if not valid:
            await asyncio.sleep(0.25)
            raise _api_error(401, "invalid_credentials", "Неверный логин или пароль")
    token = await store.create_session()
    response.set_cookie(
        "ezhiklb_session", token, max_age=12 * 60 * 60, httponly=True,
        secure=settings.secure_cookie, samesite="strict", path="/",
    )
    return {"authenticated": True}


@router.post("/api/v1/auth/logout", dependencies=[admin])
async def logout(request: Request, response: Response, store: Store = Depends(get_store)):
    cookie = request.cookies.get("ezhiklb_session")
    if cookie:
        await store.delete_session(cookie)
    response.delete_cookie("ezhiklb_session", path="/")
    return Response(status_code=204)


# ---------------------------------------------------------------------------
# Status
# ---------------------------------------------------------------------------


@router.get("/api/v1/status", dependencies=[admin])
async def status(store: Store = Depends(get_store)):
    cores = await store.list_cores()
    nodes = await store.list_nodes(offline_after_seconds=20)
    online = sum(1 for n in nodes if n.status == "online")
    listeners = 0
    for core in cores:
        _, revision = await store.get_core(core.id)
        listeners += sum(1 for inbound in revision.config.inbounds if inbound.enabled)
    return {
        "version": PANEL_VERSION, "cores": len(cores), "nodes": len(nodes),
        "online_nodes": online, "listeners": listeners,
    }


# ---------------------------------------------------------------------------
# Cores (renamed from "profiles")
# ---------------------------------------------------------------------------


class CorePayload(BaseModel):
    name: str = ""
    description: str = ""
    auto_version: Optional[bool] = None
    version: str = ""
    reset_connections: bool = False
    config: CoreConfig = Field(default_factory=CoreConfig)

    def versioning(self) -> tuple[bool, str]:
        return (True if self.auto_version is None else self.auto_version), self.version.strip()


@router.get("/api/v1/cores", dependencies=[admin])
async def list_cores(store: Store = Depends(get_store)):
    return await store.list_cores()


@router.post("/api/v1/cores", dependencies=[admin], status_code=201)
async def create_core(payload: CorePayload, store: Store = Depends(get_store)):
    if not payload.name.strip():
        raise _api_error(422, "validation_failed", "Core name is required")
    auto_version, version = payload.versioning()
    try:
        core, revision = await store.create_core(payload.name.strip(), payload.description.strip(), payload.config, auto_version, version)
    except (ConflictError, ValueError) as exc:
        raise _api_error(422, "validation_failed", str(exc)) from exc
    return {"core": core, "revision": revision}


@router.get("/api/v1/cores/{core_id}", dependencies=[admin])
async def get_core(core_id: str, store: Store = Depends(get_store)):
    try:
        core, revision = await store.get_core(core_id)
    except NotFoundError as exc:
        raise _api_error(404, "not_found", "Core not found") from exc
    return {"core": core, "revision": revision}


@router.put("/api/v1/cores/{core_id}", dependencies=[admin])
async def publish_core(core_id: str, payload: CorePayload, store: Store = Depends(get_store)):
    if not payload.name.strip():
        raise _api_error(422, "validation_failed", "Core name is required")
    auto_version, version = payload.versioning()
    try:
        core, revision = await store.publish_core_revision(
            core_id, payload.name.strip(), payload.description.strip(), payload.config, auto_version, version, payload.reset_connections
        )
    except NotFoundError as exc:
        raise _api_error(404, "not_found", "Core not found") from exc
    except (ConflictError, ValueError) as exc:
        raise _api_error(422, "validation_failed", str(exc)) from exc
    return {"core": core, "revision": revision}


@router.delete("/api/v1/cores/{core_id}", dependencies=[admin], status_code=204)
async def delete_core(core_id: str, store: Store = Depends(get_store)):
    try:
        await store.delete_core(core_id)
    except NotFoundError as exc:
        raise _api_error(404, "not_found", "Core not found") from exc
    except ConflictError as exc:
        raise _api_error(409, "core_in_use", str(exc)) from exc


class CloneRequest(BaseModel):
    name: str = ""


@router.post("/api/v1/cores/{core_id}/clone", dependencies=[admin], status_code=201)
async def clone_core(core_id: str, body: CloneRequest, store: Store = Depends(get_store)):
    try:
        core, revision = await store.clone_core(core_id, body.name.strip())
    except NotFoundError as exc:
        raise _api_error(404, "not_found", "Core not found") from exc
    except (ConflictError, ValueError) as exc:
        raise _api_error(422, "validation_failed", str(exc)) from exc
    return {"core": core, "revision": revision}


# ---------------------------------------------------------------------------
# Nodes — one-step enrollment (point 4)
# ---------------------------------------------------------------------------


async def _nodes_with_outbound_summary(store: Store, settings: Settings):
    """Attaches outbound_alive/outbound_total to each node — how many of its
    core's used, enabled outbounds are currently reachable — for the node
    row's "Активные исходящие A/T" reading, shared by Статистика and Узлы."""
    nodes = await store.list_nodes(offline_after_seconds=settings.node_offline_after_seconds)
    if not nodes:
        return nodes
    configs: dict[str, CoreConfig] = {}
    for core_id in {node.core_id for node in nodes if node.core_id}:
        try:
            _, revision = await store.get_core(core_id)
            configs[core_id] = revision.config
        except NotFoundError:
            continue
    health_rows = await store.list_health()
    health_by_node_endpoint = {(row["node_id"], row["address"]): row["state"] for row in health_rows}

    for node in nodes:
        config = configs.get(node.core_id)
        if config is None:
            continue
        used_outbound_ids = {target.outbound_id for binding in config.bindings if binding.enabled for target in binding.targets}
        outbounds_by_id = {o.id: o for o in config.outbounds}
        total = alive = 0
        for outbound_id in used_outbound_ids:
            outbound = outbounds_by_id.get(outbound_id)
            if outbound is None or not outbound.enabled:
                continue
            total += 1
            endpoint = f"{outbound.address}:{outbound.port}"
            if health_by_node_endpoint.get((node.id, endpoint)) == "reachable":
                alive += 1
        node.outbound_alive = alive
        node.outbound_total = total
    return nodes


def _validate_node_overrides(poll_interval_seconds: Optional[int], timeout_seconds: Optional[int]) -> None:
    if poll_interval_seconds is not None and not (1 <= poll_interval_seconds <= 3600):
        raise _api_error(422, "validation_failed", "Интервал опроса должен быть от 1 до 3600 секунд")
    if timeout_seconds is not None and not (1 <= timeout_seconds <= 120):
        raise _api_error(422, "validation_failed", "Таймаут должен быть от 1 до 120 секунд")


class NodeCreateRequest(BaseModel):
    name: str
    ingress_address: str = ""
    control_address: str
    control_port: int
    api_key: str
    cert_pem: str
    core_id: str
    poll_interval_seconds: Optional[int] = None
    timeout_seconds: Optional[int] = None


@router.get("/api/v1/nodes", dependencies=[admin])
async def list_nodes(store: Store = Depends(get_store), settings: Settings = Depends(get_settings)):
    return await _nodes_with_outbound_summary(store, settings)


@router.post("/api/v1/nodes", dependencies=[admin], status_code=201)
async def create_node(payload: NodeCreateRequest, poller: Poller = Depends(get_poller), store: Store = Depends(get_store)):
    name = payload.name.strip()
    control_address = payload.control_address.strip()
    if not name or not control_address or not payload.api_key.strip() or not payload.cert_pem.strip() or not payload.core_id:
        raise _api_error(422, "validation_failed", "Название, адрес, порт, API ключ, сертификат и ядро обязательны")
    if not (1 <= payload.control_port <= 65535):
        raise _api_error(422, "validation_failed", "Порт узла должен быть от 1 до 65535")
    _validate_node_overrides(payload.poll_interval_seconds, payload.timeout_seconds)
    try:
        node = await store.create_node(
            name, payload.ingress_address.strip(), control_address, payload.control_port,
            payload.api_key.strip(), payload.cert_pem.strip(), payload.core_id,
            payload.poll_interval_seconds, payload.timeout_seconds,
        )
    except NotFoundError as exc:
        raise _api_error(404, "not_found", "Core not found") from exc
    except (ConflictError, ValueError) as exc:
        raise _api_error(422, "validation_failed", str(exc)) from exc

    connected = False
    error_message = ""
    try:
        await poller.check_connectivity(node.id)
        connected = True
    except NodeUnreachableError as exc:
        error_message = str(exc)
    except NodeRejectedError as exc:
        error_message = str(exc)
    node = await store.get_node(node.id)
    return {"node": node, "connected": connected, "connect_error": error_message}


class NodeUpdateRequest(BaseModel):
    name: str
    ingress_address: str = ""
    control_address: str
    control_port: int
    api_key: str = ""
    cert_pem: str = ""
    poll_interval_seconds: Optional[int] = None
    timeout_seconds: Optional[int] = None


@router.put("/api/v1/nodes/{node_id}", dependencies=[admin])
async def update_node(node_id: str, payload: NodeUpdateRequest, store: Store = Depends(get_store)):
    if not payload.name.strip() or not payload.control_address.strip():
        raise _api_error(422, "validation_failed", "Название и адрес узла обязательны")
    _validate_node_overrides(payload.poll_interval_seconds, payload.timeout_seconds)
    try:
        await store.update_node(
            node_id, payload.name.strip(), payload.ingress_address.strip(), payload.control_address.strip(),
            payload.control_port, payload.api_key.strip() or None, payload.cert_pem.strip() or None,
            payload.poll_interval_seconds, payload.timeout_seconds,
        )
    except NotFoundError as exc:
        raise _api_error(404, "not_found", "Node not found") from exc
    return Response(status_code=204)


@router.post("/api/v1/nodes/{node_id}/sync", dependencies=[admin])
async def sync_node(node_id: str, poller: Poller = Depends(get_poller), store: Store = Depends(get_store)):
    """"Синхронизировать" — polls and pushes to this one node right now,
    ignoring its own poll-interval countdown."""
    try:
        await poller.sync_now(node_id)
    except NotFoundError as exc:
        raise _api_error(404, "not_found", "Node not found") from exc
    return await store.get_node(node_id)


@router.get("/api/v1/nodes/{node_id}/breakdown", dependencies=[admin])
async def node_breakdown(node_id: str, store: Store = Depends(get_store)):
    """Per-inbound and per-outbound online/traffic detail for one node —
    what the node detail dialog shows beyond the node-wide totals already on
    the Node object itself."""
    try:
        node = await store.get_node(node_id)
    except NotFoundError as exc:
        raise _api_error(404, "not_found", "Node not found") from exc

    node_totals = {
        "active_ips": node.metrics.active_ips if node.metrics else 0,
        "network_rx_bps": node.metrics.network_rx_bps if node.metrics else 0,
        "network_tx_bps": node.metrics.network_tx_bps if node.metrics else 0,
    }
    empty = {"inbounds": [], "outbounds": [], "node": node_totals}
    if not node.core_id:
        return empty
    try:
        _, revision = await store.get_core(node.core_id)
    except NotFoundError:
        return empty
    config = revision.config

    health_by_endpoint = {row["address"]: row["state"] for row in await store.list_health() if row["node_id"] == node_id}
    stats_by_outbound = {row["outbound_id"]: row for row in await store.list_outbound_stats() if row["node_id"] == node_id}

    outbounds = []
    for outbound in config.outbounds:
        stat = stats_by_outbound.get(outbound.id, {})
        outbounds.append({
            "outbound_id": outbound.id, "name": outbound.name, "address": outbound.address, "port": outbound.port,
            "enabled": outbound.enabled, "reachable": health_by_endpoint.get(f"{outbound.address}:{outbound.port}") == "reachable",
            "online_ips": stat.get("active_ips", 0), "rx_bps": stat.get("rx_bps", 0), "tx_bps": stat.get("tx_bps", 0),
        })
    by_id = {o["outbound_id"]: o for o in outbounds}

    inbounds = []
    for inbound in config.inbounds:
        bound_ids = {
            target.outbound_id
            for binding in config.bindings
            if binding.enabled and binding.inbound_id == inbound.id
            for target in binding.targets
        }
        inbounds.append({
            "inbound_id": inbound.id, "name": inbound.name, "listen_address": inbound.listen_address, "listen_port": inbound.listen_port,
            "online_ips": sum(by_id[oid]["online_ips"] for oid in bound_ids if oid in by_id),
            "rx_bps": sum(by_id[oid]["rx_bps"] for oid in bound_ids if oid in by_id),
            "tx_bps": sum(by_id[oid]["tx_bps"] for oid in bound_ids if oid in by_id),
        })

    return {"inbounds": inbounds, "outbounds": outbounds, "node": node_totals}


@router.delete("/api/v1/nodes/{node_id}", dependencies=[admin], status_code=204)
async def delete_node(node_id: str, store: Store = Depends(get_store)):
    try:
        await store.delete_node(node_id)
    except NotFoundError as exc:
        raise _api_error(404, "not_found", "Node not found") from exc


@router.post("/api/v1/nodes/{node_id}/force-delete", dependencies=[admin], status_code=204)
async def force_delete_node(node_id: str, store: Store = Depends(get_store)):
    try:
        await store.force_delete_node(node_id)
    except NotFoundError as exc:
        raise _api_error(404, "not_found", "Node not found") from exc
    except ConflictError as exc:
        raise _api_error(409, "not_pending_deletion", str(exc)) from exc


class EnabledRequest(BaseModel):
    enabled: bool


@router.put("/api/v1/nodes/{node_id}/enabled", dependencies=[admin], status_code=204)
async def set_node_enabled(node_id: str, body: EnabledRequest, store: Store = Depends(get_store)):
    try:
        await store.set_node_enabled(node_id, body.enabled)
    except NotFoundError as exc:
        raise _api_error(404, "not_found", "Node not found") from exc


@router.post("/api/v1/nodes/{node_id}/health-probe", dependencies=[admin])
async def request_health_probe(node_id: str, poller: Poller = Depends(get_poller)):
    try:
        return await poller.probe_now(node_id)
    except NotFoundError as exc:
        raise _api_error(404, "not_found", "Node not found") from exc
    except NodeUnreachableError as exc:
        raise _api_error(503, "node_unreachable", str(exc)) from exc
    except NodeRejectedError as exc:
        raise _api_error(exc.status_code, "node_rejected", str(exc)) from exc


@router.post("/api/v1/nodes/{node_id}/check", dependencies=[admin])
async def check_node_connectivity(node_id: str, poller: Poller = Depends(get_poller)):
    try:
        state = await poller.check_connectivity(node_id)
    except NotFoundError as exc:
        raise _api_error(404, "not_found", "Node not found") from exc
    except NodeUnreachableError as exc:
        raise _api_error(503, "node_unreachable", str(exc)) from exc
    except NodeRejectedError as exc:
        raise _api_error(exc.status_code, "node_rejected", str(exc)) from exc
    return {"connected": True, "state": state}


@router.post("/api/v1/nodes/{node_id}/update", dependencies=[admin])
async def request_node_update(node_id: str, store: Store = Depends(get_store)):
    try:
        await store.request_node_update(node_id, PANEL_VERSION)
    except NotFoundError as exc:
        raise _api_error(404, "not_found", "Node not found") from exc
    return {"version": PANEL_VERSION}


class AssignCoreRequest(BaseModel):
    core_id: str


@router.put("/api/v1/nodes/{node_id}/profile", dependencies=[admin], status_code=204)
async def assign_core(node_id: str, body: AssignCoreRequest, store: Store = Depends(get_store)):
    try:
        await store.assign_core(node_id, body.core_id)
    except NotFoundError as exc:
        raise _api_error(404, "not_found", "Node or core not found") from exc


# ---------------------------------------------------------------------------
# Health / stats / metrics / events
# ---------------------------------------------------------------------------


@router.get("/api/v1/health", dependencies=[admin])
async def list_health(store: Store = Depends(get_store)):
    return await store.list_health()


@router.get("/api/v1/stats", dependencies=[admin])
async def list_stats(store: Store = Depends(get_store)):
    return await store.list_stats()


@router.get("/api/v1/outbounds", dependencies=[admin])
async def list_outbounds(store: Store = Depends(get_store)):
    """Every outbound across every core, with a live status: "alive" (used
    by an enabled binding, applied on at least one online node, and that
    node's health check says it's reachable — plus how many client IPs are
    currently proxied to it right now), "dead" (used and applied, but not
    currently reachable), or "unused" (disabled, not referenced by any
    enabled binding, or its core isn't applied on any online node)."""
    cores = await store.list_cores()
    nodes = await store.list_nodes(offline_after_seconds=20)
    health_rows = await store.list_health()
    stat_rows = await store.list_outbound_stats()

    health_by_node_endpoint = {(row["node_id"], row["address"]): row["state"] for row in health_rows}
    ips_by_node_outbound = {(row["node_id"], row["outbound_id"]): row["active_ips"] for row in stat_rows}

    result = []
    for core in cores:
        _, revision = await store.get_core(core.id)
        config = revision.config
        used_outbound_ids = {target.outbound_id for binding in config.bindings if binding.enabled for target in binding.targets}
        applying_nodes = [node for node in nodes if node.core_id == core.id and node.status == "online"]

        for outbound in config.outbounds:
            entry = {
                "core_id": core.id, "core_name": core.name,
                "outbound_id": outbound.id, "name": outbound.name,
                "address": outbound.address, "port": outbound.port,
                "enabled": outbound.enabled,
            }
            if not outbound.enabled or outbound.id not in used_outbound_ids or not applying_nodes:
                result.append({**entry, "status": "unused", "online_ips": 0})
                continue
            endpoint = f"{outbound.address}:{outbound.port}"
            reachable = any(health_by_node_endpoint.get((node.id, endpoint)) == "reachable" for node in applying_nodes)
            if not reachable:
                result.append({**entry, "status": "dead", "online_ips": 0})
                continue
            online_ips = sum(ips_by_node_outbound.get((node.id, outbound.id), 0) for node in applying_nodes)
            result.append({**entry, "status": "alive", "online_ips": online_ips})
    return result


@router.get("/api/v1/metrics/history", dependencies=[admin])
async def metric_history(node_id: str = "all", store: Store = Depends(get_store)):
    return await store.list_metric_history(node_id)


@router.get("/api/v1/events", dependencies=[admin])
async def list_events(filter: str = "all", store: Store = Depends(get_store)):
    return await store.list_audit(filter, 200)


# ---------------------------------------------------------------------------
# Settings
# ---------------------------------------------------------------------------


@router.get("/api/v1/settings", dependencies=[admin])
async def get_settings_api(store: Store = Depends(get_store), settings: Settings = Depends(get_settings)):
    return await store.get_system_settings(SystemSettings(panel_port=settings.port))


@router.put("/api/v1/settings", dependencies=[admin], status_code=202)
async def update_settings_api(payload: SystemSettings, request: Request, store: Store = Depends(get_store)):
    try:
        await store.update_system_settings(payload)
    except ConflictError as exc:
        raise _api_error(422, "validation_failed", str(exc)) from exc
    restart = getattr(request.app.state, "restart", None)
    if restart is not None:
        asyncio.get_running_loop().call_later(0.35, restart)
    return {"settings": payload, "restarting": True}


@router.get("/healthz")
async def healthz():
    return {"status": "ok"}
