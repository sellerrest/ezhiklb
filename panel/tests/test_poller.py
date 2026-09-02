"""Poller tests using a fake NodeClient double instead of a real node —
mirrors the upstream Go project's own fakeRunner-based testing style."""

from __future__ import annotations

import uuid

import pytest

from ezhiklb_panel.config import Settings
from ezhiklb_panel.db import Database
from ezhiklb_panel.node_client import NodeRejectedError, NodeUnreachableError
from ezhiklb_panel.poller import Poller
from ezhiklb_panel.store import Store


class FakeRemoteNode:
    """The mutable state that would actually live on the remote node,
    outliving any single HTTP call/connection — a real Poller opens a fresh
    NodeClient (and fresh httpx connection) on every tick, so the fake must
    keep "remote" state outside the per-call client object too, or a second
    tick would (wrongly) see a freshly-reset node."""

    def __init__(self):
        self.applied: list[dict] = []
        self.get_state_calls = 0
        self.decommission_calls = 0
        self.state: dict = {
            "agent_version": "1.0.0", "applied_revision": 0, "apply_state": "waiting", "apply_error": "",
            "health": [], "stats": [], "metrics": {}, "diagnostics": None, "update_state": "idle", "update_error": "",
        }
        self.unreachable = False
        self.decommission_result = {"decommissioned": True}


class FakeNodeClient:
    def __init__(self, dial_info: dict, remote: FakeRemoteNode):
        self.dial_info = dial_info
        self.remote = remote

    async def get_state(self, timeout: float = 4.0) -> dict:
        self.remote.get_state_calls += 1
        if self.remote.unreachable:
            raise NodeUnreachableError("connection refused")
        return dict(self.remote.state)

    async def apply(self, desired_state: dict, timeout: float = 25.0) -> dict:
        self.remote.applied.append(desired_state)
        self.remote.state["applied_revision"] = desired_state["revision"]
        self.remote.state["apply_state"] = "applied"
        return {"applied_revision": desired_state["revision"], "apply_state": "applied"}

    async def request_update(self, target_version: str, timeout: float = 5.0) -> dict:
        self.remote.state["update_state"] = "requested"
        return {"update_state": "requested"}

    async def health_probe(self, timeout: float = 10.0) -> dict:
        return {"health": []}

    async def decommission(self, timeout: float = 15.0) -> dict:
        if self.remote.unreachable:
            raise NodeUnreachableError("connection refused")
        self.remote.decommission_calls += 1
        return dict(self.remote.decommission_result)


@pytest.fixture
async def store(tmp_path):
    db_path = tmp_path / f"{uuid.uuid4().hex}.db"
    db = Database(f"sqlite+aiosqlite:///{db_path}")
    await db.migrate()
    s = Store(db)
    await s.bootstrap()
    yield s
    await db.close()


def _settings(poll_interval_seconds: int = 0) -> Settings:
    # poll_interval_seconds=0 by default: makes every node "due" on every
    # tick() call, matching these tests' assumption that tick() always
    # polls — the interval-gating behavior itself has its own dedicated test.
    return Settings(
        database_url="", secure_cookie=False, web_dir="", host="127.0.0.1", port=8080,
        poll_interval_seconds=poll_interval_seconds, node_offline_after_seconds=20, node_state_timeout_seconds=4.0, node_apply_timeout_seconds=25.0,
    )


async def make_node(store: Store):
    cores = await store.list_cores()
    core = cores[0]
    return await store.create_node(
        "node-1", "203.0.113.10", "203.0.113.10", 62050, "api-key", "-----BEGIN CERTIFICATE-----\nMA==\n-----END CERTIFICATE-----\n", core.id
    )


async def test_tick_records_state_and_pushes_pending_revision(store):
    node = await make_node(store)
    remote = FakeRemoteNode()

    def factory(dial_info):
        return FakeNodeClient(dial_info, remote)

    poller = Poller(store, _settings(), client_factory=factory)

    # First tick: pulls the node's initial state (nothing applied yet), sees
    # desired(1) != applied(0) and pushes the apply — but the panel only
    # learns the definitive result on the *next* GET /v1/state poll, same
    # eventually-consistent pattern as before.
    await poller.tick()
    refreshed = await store.get_node(node.id)
    assert refreshed.status == "online"
    assert refreshed.applied_revision == 0
    assert len(remote.applied) == 1
    assert remote.applied[0]["revision"] == 1

    # Second tick: the fake node now reports applied_revision=1; nothing left to push.
    await poller.tick()
    refreshed = await store.get_node(node.id)
    assert refreshed.applied_revision == 1
    assert len(remote.applied) == 1


async def test_tick_skips_unreachable_node_without_crashing(store):
    node = await make_node(store)
    remote = FakeRemoteNode()
    remote.unreachable = True

    def factory(dial_info):
        return FakeNodeClient(dial_info, remote)

    poller = Poller(store, _settings(), client_factory=factory)
    await poller.tick()  # must not raise

    refreshed = await store.get_node(node.id)
    assert refreshed.applied_revision == 0  # never recorded, since the node never answered


async def test_decommission_flow_deletes_node_after_ack(store):
    node = await make_node(store)
    await store.delete_node(node.id)
    remote = FakeRemoteNode()

    def factory(dial_info):
        return FakeNodeClient(dial_info, remote)

    poller = Poller(store, _settings(), client_factory=factory)
    await poller.tick()

    with pytest.raises(Exception):
        await store.get_node(node.id)


async def test_per_node_poll_interval_gates_ticks(store):
    from datetime import datetime, timedelta, timezone

    node = await make_node(store)
    await store.update_node(node.id, node.name, node.ingress_address, node.control_address, node.control_port, poll_interval_seconds=3600)
    remote = FakeRemoteNode()

    def factory(dial_info):
        return FakeNodeClient(dial_info, remote)

    # Global default is 0 (always due), but this node overrides to 1 hour —
    # only the node's own interval should govern whether it gets polled.
    poller = Poller(store, _settings(poll_interval_seconds=0), client_factory=factory)
    await poller.tick()
    assert remote.get_state_calls == 1  # first tick always polls (never polled before)

    await poller.tick()
    assert remote.get_state_calls == 1  # second tick, seconds later: not due yet, skipped

    poller._last_polled[node.id] = datetime.now(timezone.utc) - timedelta(hours=2)
    await poller.tick()
    assert remote.get_state_calls == 2  # backdated past its own interval: due again


async def test_sync_now_bypasses_the_interval(store):
    node = await make_node(store)
    await store.update_node(node.id, node.name, node.ingress_address, node.control_address, node.control_port, poll_interval_seconds=3600)
    remote = FakeRemoteNode()

    def factory(dial_info):
        return FakeNodeClient(dial_info, remote)

    poller = Poller(store, _settings(poll_interval_seconds=0), client_factory=factory)
    await poller.tick()
    assert remote.get_state_calls == 1

    await poller.tick()
    assert remote.get_state_calls == 1  # not due yet

    await poller.sync_now(node.id)
    assert remote.get_state_calls == 2  # sync_now ignores the interval entirely


async def test_decommission_stays_pending_while_node_unreachable(store):
    node = await make_node(store)
    await store.delete_node(node.id)

    remote = FakeRemoteNode()
    remote.unreachable = True

    def factory(dial_info):
        return FakeNodeClient(dial_info, remote)

    poller = Poller(store, _settings(), client_factory=factory)
    await poller.tick()

    refreshed = await store.get_node(node.id)
    assert refreshed.status == "deleting"
