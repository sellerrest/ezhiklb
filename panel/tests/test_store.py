"""Port of the interesting scenarios from internal/store/store_test.go,
adapted for the renamed Core entity and the dropped node_probe_requests /
agent-version-gated-reset mechanics (both were artifacts of the old
poll-based protocol and don't apply to the new direct-RPC model)."""

from __future__ import annotations

import uuid

import pytest

from ezhiklb_panel.db import Database
from ezhiklb_panel.domain import default_core_config
from ezhiklb_panel.models import SystemSettings
from ezhiklb_panel.store import ConflictError, NotFoundError, Store, resolve_version


@pytest.fixture
async def store(tmp_path):
    db_path = tmp_path / f"{uuid.uuid4().hex}.db"
    db = Database(f"sqlite+aiosqlite:///{db_path}")
    await db.migrate()
    s = Store(db)
    await s.bootstrap()
    yield s
    await db.close()


async def test_admin_account_and_session_lifecycle(store):
    assert await store.admin_count() == 0
    assert await store.get_admin_by_username("admin") is None

    await store.create_admin_account("admin", "salt$hash")
    account = await store.get_admin_by_username("admin")
    assert account["username"] == "admin"
    assert account["password_hash"] == "salt$hash"
    assert await store.admin_count() == 1

    token = await store.create_session()
    assert await store.session_valid(token) is True
    assert await store.session_valid("not-a-real-token") is False

    await store.delete_session(token)
    assert await store.session_valid(token) is False


async def test_multiple_admins_add_passwd_remove(store):
    await store.create_admin_account("alice", "hash-a")
    await store.create_admin_account("bob", "hash-b")
    assert await store.admin_count() == 2

    admins = await store.list_admins()
    assert {a["username"] for a in admins} == {"alice", "bob"}
    assert "password_hash" not in admins[0]  # list_admins never leaks hashes

    with pytest.raises(ConflictError):
        await store.create_admin_account("alice", "hash-dup")

    await store.update_admin_password("bob", "new-hash")
    assert (await store.get_admin_by_username("bob"))["password_hash"] == "new-hash"
    with pytest.raises(NotFoundError):
        await store.update_admin_password("carol", "hash-c")

    await store.delete_admin("bob")
    assert await store.admin_count() == 1
    with pytest.raises(ConflictError):
        await store.delete_admin("alice")  # can't remove the last admin
    with pytest.raises(NotFoundError):
        await store.delete_admin("bob")  # already gone


def test_resolve_version_automatic():
    assert resolve_version(True, "ignored", 3) == "v3"


def test_resolve_version_manual():
    assert resolve_version(False, "release-1.2", 4) == "release-1.2"


def test_resolve_version_rejects_underscore():
    with pytest.raises(ConflictError):
        resolve_version(False, "release_1", 4)


def test_resolve_version_rejects_cyrillic():
    with pytest.raises(ConflictError):
        resolve_version(False, "версия-1", 4)


async def test_bootstrap_creates_default_core(store):
    cores = await store.list_cores()
    assert len(cores) == 1
    assert cores[0].version == "v1"
    assert cores[0].auto_version is True

    # Bootstrap is idempotent — re-running it must not create a second core.
    await store.bootstrap()
    cores = await store.list_cores()
    assert len(cores) == 1


async def test_core_versions_and_publish_conflicts(store):
    cores = await store.list_cores()
    core = cores[0]
    config = default_core_config()

    core, revision = await store.publish_core_revision(core.id, core.name, core.description, config, True, "", False)
    assert core.version == "v2"
    assert revision.version == "v2"

    with pytest.raises(ConflictError):
        await store.publish_core_revision(core.id, core.name, core.description, config, False, "v2", False)

    core, _ = await store.publish_core_revision(core.id, core.name, core.description, config, False, "vpn-2026.08", False)
    assert core.version == "vpn-2026.08"
    assert core.auto_version is False


async def test_clone_core(store):
    cores = await store.list_cores()
    core = cores[0]
    cloned, _ = await store.clone_core(core.id, "")
    assert cloned.name.startswith(core.name)
    assert cloned.id != core.id


async def test_delete_core_blocked_while_assigned(store):
    cores = await store.list_cores()
    core = cores[0]
    await store.create_node("node-1", "203.0.113.10", "203.0.113.10", 62050, "api-key-value", _fake_cert(), core.id)
    with pytest.raises(ConflictError):
        await store.delete_core(core.id)


async def test_node_lifecycle(store):
    cores = await store.list_cores()
    core = cores[0]
    node = await store.create_node("node-1", "203.0.113.10", "203.0.113.10", 62050, "api-key-value", _fake_cert(), core.id)
    assert node.status == "connecting"
    assert node.desired_revision == 1

    nodes = await store.list_nodes(offline_after_seconds=20)
    assert len(nodes) == 1
    assert nodes[0].status == "connecting"  # never seen yet

    await store.set_node_enabled(node.id, False)
    node = await store.get_node(node.id)
    assert node.status == "disabled"
    assert node.enabled is False

    await store.set_node_enabled(node.id, True)
    node = await store.get_node(node.id)
    assert node.enabled is True

    await store.delete_node(node.id)
    node = await store.get_node(node.id)
    assert node.status == "deleting"

    with pytest.raises(NotFoundError):
        await store.force_delete_node("does-not-exist")


async def test_force_delete_requires_pending_deletion(store):
    cores = await store.list_cores()
    core = cores[0]
    node = await store.create_node("node-2", "", "203.0.113.11", 62050, "key", _fake_cert(), core.id)
    with pytest.raises(ConflictError):
        await store.force_delete_node(node.id)
    await store.delete_node(node.id)
    await store.force_delete_node(node.id)
    with pytest.raises(NotFoundError):
        await store.get_node(node.id)


async def test_desired_state_matches_assigned_core(store):
    cores = await store.list_cores()
    core = cores[0]
    node = await store.create_node("node-3", "", "203.0.113.12", 62050, "key", _fake_cert(), core.id)
    desired = await store.desired_state_for_node(node.id)
    assert desired is not None
    assert desired["profile_id"] == core.id
    assert desired["revision"] == 1
    assert desired["reset_connections"] is False


async def test_record_node_state_tracks_apply_errors_and_metric_history(store):
    from ezhiklb_panel.domain import NodeMetrics

    cores = await store.list_cores()
    core = cores[0]
    node = await store.create_node("node-4", "", "203.0.113.13", 62050, "key", _fake_cert(), core.id)

    await store.record_node_state(
        node.id, agent_version="1.0.0", apply_state="error", applied_revision=0, apply_error="boom",
        health=[], stats=[], outbound_stats=[], metrics=NodeMetrics(), diagnostics=None, update_state="idle", update_error="",
    )
    node = await store.get_node(node.id)
    assert node.status == "online"
    assert node.apply_error == "boom"

    events = await store.list_audit("errors")
    assert any(e.action == "node.apply_failed" for e in events)

    await store.record_node_state(
        node.id, agent_version="1.0.0", apply_state="applied", applied_revision=1, apply_error="",
        health=[], stats=[], outbound_stats=[], metrics=NodeMetrics(), diagnostics=None, update_state="idle", update_error="",
    )
    node = await store.get_node(node.id)
    assert node.apply_error == ""

    events = await store.list_audit("nodes")
    assert any(e.action == "node.apply_recovered" for e in events)

    history = await store.list_metric_history(node.id)
    assert len(history) >= 1


async def test_record_node_state_persists_outbound_stats(store):
    from ezhiklb_panel.domain import NodeMetrics

    cores = await store.list_cores()
    node = await store.create_node("node-5", "", "203.0.113.14", 62050, "key", _fake_cert(), cores[0].id)

    await store.record_node_state(
        node.id, agent_version="1.0.0", apply_state="applied", applied_revision=1, apply_error="",
        health=[], stats=[], outbound_stats=[{"outbound_id": "out1", "active_connections": 3, "active_ips": 2}],
        metrics=NodeMetrics(), diagnostics=None, update_state="idle", update_error="",
    )
    rows = await store.list_outbound_stats()
    matching = [row for row in rows if row["node_id"] == node.id and row["outbound_id"] == "out1"]
    assert len(matching) == 1
    assert matching[0]["active_connections"] == 3
    assert matching[0]["active_ips"] == 2

    # A second record_node_state call must replace, not accumulate, this
    # node's rows — mirrors backend_health/service_stats' own delete+insert.
    await store.record_node_state(
        node.id, agent_version="1.0.0", apply_state="applied", applied_revision=1, apply_error="",
        health=[], stats=[], outbound_stats=[{"outbound_id": "out1", "active_connections": 0, "active_ips": 0}],
        metrics=NodeMetrics(), diagnostics=None, update_state="idle", update_error="",
    )
    rows = await store.list_outbound_stats()
    matching = [row for row in rows if row["node_id"] == node.id and row["outbound_id"] == "out1"]
    assert len(matching) == 1
    assert matching[0]["active_connections"] == 0


async def test_settings_roundtrip(store):
    defaults = SystemSettings(panel_port=8080)
    settings = await store.get_system_settings(defaults)
    assert settings.panel_port == 8080

    await store.update_system_settings(SystemSettings(panel_port=9090))
    settings = await store.get_system_settings(defaults)
    assert settings.panel_port == 9090

    with pytest.raises(ConflictError):
        await store.update_system_settings(SystemSettings(panel_port=1))


def _fake_cert() -> str:
    return (
        "-----BEGIN CERTIFICATE-----\n"
        "MIIBIjCB1qADAgECAhQtest\n"
        "-----END CERTIFICATE-----\n"
    )
