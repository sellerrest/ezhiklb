"""Background reconciliation loop — the panel-side half of the protocol
inversion (point 2 of the fork).

Where the upstream Go project's agent polled an inbound panel port every 5s
and pushed a heartbeat every 15s, here the *panel* dials out to every
enabled node on a timer: pull its current state, persist it, and push
whatever the node doesn't have yet (a new core revision, a pending update, a
pending decommission). A node that can't be reached is simply left alone —
its `last_seen_at` stops advancing, and Store.list_nodes()'s own staleness
check (mirroring the upstream ListNodes()) is what turns that into an
"offline" status wherever nodes are read, exactly like before. No separate
in-memory failure counter is needed, so a poller restart can't desync one.
"""

from __future__ import annotations

import asyncio
import logging
from datetime import datetime, timezone
from typing import Callable, Optional

from .config import Settings
from .domain import NodeDiagnostics, NodeMetrics
from .node_client import NodeClient, NodeRejectedError, NodeUnreachableError, make_client
from .store import Store

logger = logging.getLogger("ezhiklb_panel.poller")

# An update RPC in one of these stages is already in flight on the node; the
# poller must not re-request it every tick just because update_target is
# still set and agent_version hasn't caught up yet.
_UPDATE_IN_PROGRESS_STATES = {"requested", "downloading", "verifying", "installing", "restarting"}

# The scheduler tick — short relative to any node's own poll interval, so a
# per-node override (set in its "Расширенные настройки") takes effect within
# a second of becoming due, the same pattern the Go agent's HealthMonitor
# uses for per-outbound intervals.
_SCHEDULER_TICK_SECONDS = 1.0


class Poller:
    def __init__(
        self, store: Store, settings: Settings, client_factory: Callable[[dict], NodeClient] = make_client
    ):
        self.store = store
        self.settings = settings
        self.client_factory = client_factory
        self._task: Optional[asyncio.Task] = None
        self._stop = asyncio.Event()
        self._last_polled: dict[str, datetime] = {}

    def start(self) -> None:
        if self._task is None:
            self._stop.clear()
            self._task = asyncio.create_task(self._run(), name="ezhiklb-poller")

    async def stop(self) -> None:
        self._stop.set()
        if self._task is not None:
            await self._task
            self._task = None

    async def _run(self) -> None:
        while not self._stop.is_set():
            try:
                await self.tick()
            except Exception:
                logger.exception("poller tick failed")
            try:
                await asyncio.wait_for(self._stop.wait(), timeout=_SCHEDULER_TICK_SECONDS)
            except asyncio.TimeoutError:
                pass

    async def tick(self) -> None:
        nodes = await self.store.list_nodes(offline_after_seconds=self.settings.node_offline_after_seconds)
        pollable = [node for node in nodes if node.status != "disabled" and node.control_address and node.control_port]
        if not pollable:
            return
        now = datetime.now(timezone.utc)
        due = []
        for node in pollable:
            interval = node.poll_interval_seconds or self.settings.poll_interval_seconds
            last = self._last_polled.get(node.id)
            if last is None or (now - last).total_seconds() >= interval:
                due.append(node)
        if not due:
            return
        await asyncio.gather(*(self._poll_node(node.id, node.status, node.timeout_seconds) for node in due), return_exceptions=True)

    async def sync_now(self, node_id: str) -> None:
        """Used by the "Синхронизировать" node action — polls and pushes to
        this one node immediately, ignoring its own interval countdown."""
        node = await self.store.get_node(node_id)
        await self._poll_node(node_id, node.status, node.timeout_seconds)

    async def _poll_node(self, node_id: str, status: str, timeout_override: Optional[int] = None) -> None:
        self._last_polled[node_id] = datetime.now(timezone.utc)
        dial_info = await self.store.node_dial_info(node_id)
        if not dial_info.get("api_key") or not dial_info.get("cert_pem"):
            return
        client = self.client_factory(dial_info)

        if status == "deleting":
            await self._poll_decommission(node_id, client)
            return

        state_timeout = timeout_override or self.settings.node_state_timeout_seconds
        try:
            state = await client.get_state(timeout=state_timeout)
        except NodeUnreachableError:
            return
        except NodeRejectedError as exc:
            logger.warning("node %s rejected /v1/state: %s", node_id, exc)
            return

        await self._record_state(node_id, state)
        await self._push_if_needed(node_id, state, client, timeout_override)

    async def _record_state(self, node_id: str, state: dict) -> None:
        raw_diagnostics = state.get("diagnostics")
        diagnostics = NodeDiagnostics.model_validate(raw_diagnostics) if raw_diagnostics else None
        metrics_raw = state.get("metrics") or {}
        metrics = NodeMetrics.model_validate(metrics_raw) if metrics_raw else NodeMetrics()
        await self.store.record_node_state(
            node_id,
            agent_version=state.get("agent_version", ""),
            apply_state=state.get("apply_state", "waiting"),
            applied_revision=state.get("applied_revision", 0),
            apply_error=state.get("apply_error", ""),
            health=state.get("health") or [],
            stats=state.get("stats") or [],
            outbound_stats=state.get("outbound_stats") or [],
            metrics=metrics,
            diagnostics=diagnostics,
            update_state=state.get("update_state", "idle"),
            update_error=state.get("update_error", ""),
        )

    async def _push_if_needed(self, node_id: str, state: dict, client: NodeClient, timeout_override: Optional[int] = None) -> None:
        node = await self.store.get_node(node_id)

        if node.update_target and state.get("update_state") not in _UPDATE_IN_PROGRESS_STATES:
            if node.update_target != state.get("agent_version"):
                try:
                    await client.request_update(node.update_target, timeout=5.0)
                except (NodeUnreachableError, NodeRejectedError) as exc:
                    logger.warning("update request for node %s failed: %s", node_id, exc)
            return

        if node.desired_revision and node.desired_revision != node.applied_revision:
            desired = await self.store.desired_state_for_node(node_id)
            if desired is None:
                return
            apply_timeout = timeout_override or self.settings.node_apply_timeout_seconds
            try:
                await client.apply(desired, timeout=apply_timeout)
            except NodeUnreachableError as exc:
                logger.warning("node %s unreachable while applying revision %s: %s", node_id, desired["revision"], exc)
            except NodeRejectedError as exc:
                logger.warning("node %s rejected revision %s: %s", node_id, desired["revision"], exc)

    async def _poll_decommission(self, node_id: str, client: NodeClient) -> None:
        try:
            result = await client.decommission(timeout=15.0)
        except NodeUnreachableError:
            return  # stays "deleting"; the panel's force-delete action is the escape hatch
        except NodeRejectedError as exc:
            logger.warning("decommission rejected by node %s: %s", node_id, exc)
            return
        if result.get("decommissioned"):
            node = await self.store.get_node(node_id)
            await self.store.finalize_decommission(node_id, node.agent_version)

    async def probe_now(self, node_id: str) -> dict:
        """Used by the API's "Проверить сейчас"/health-probe action — a
        direct, synchronous RPC instead of the old nonce-polling trick."""
        dial_info = await self.store.node_dial_info(node_id)
        client = self.client_factory(dial_info)
        return await client.health_probe(timeout=10.0)

    async def check_connectivity(self, node_id: str) -> dict:
        """Used by the "Проверить статус"/"Переподключить" actions — an
        immediate reachability probe against freshly entered/edited
        connection details, before the regular poll loop would get to it."""
        dial_info = await self.store.node_dial_info(node_id)
        client = self.client_factory(dial_info)
        node = await self.store.get_node(node_id)
        timeout = node.timeout_seconds or self.settings.node_state_timeout_seconds
        state = await client.get_state(timeout=timeout)
        await self._record_state(node_id, state)
        return state
