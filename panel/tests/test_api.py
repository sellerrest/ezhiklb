"""End-to-end tests against the real FastAPI app (ASGI transport, no real
network) + a temp SQLite DB — covers login (first-run account creation and
ordinary login), core CRUD/publish, and node create/list, mirroring what
docs/TESTING.md's acceptance checklist asks a human to click through by
hand."""

from __future__ import annotations

import uuid

import httpx
import pytest

from ezhiklb_panel.config import Settings
from ezhiklb_panel.db import Database
from ezhiklb_panel.main import create_app
from ezhiklb_panel.poller import Poller
from ezhiklb_panel.store import Store

ADMIN_LOGIN = "admin"
ADMIN_PASSWORD = "correct horse battery staple"


def _settings(tmp_path) -> Settings:
    db_path = tmp_path / f"{uuid.uuid4().hex}.db"
    return Settings(
        database_url=f"sqlite+aiosqlite:///{db_path}", secure_cookie=False, web_dir="",
        host="127.0.0.1", port=8080, poll_interval_seconds=5, node_offline_after_seconds=20,
        node_state_timeout_seconds=4.0, node_apply_timeout_seconds=25.0,
    )


@pytest.fixture
async def client(tmp_path):
    settings = _settings(tmp_path)
    db = Database(settings.database_url)
    store = Store(db)
    poller = Poller(store, settings)
    app = create_app(settings, store, poller)
    transport = httpx.ASGITransport(app=app)
    # httpx's ASGITransport doesn't send lifespan startup/shutdown events on
    # its own, so drive the app's lifespan (db.migrate + bootstrap + poller
    # start/stop) directly — the same context manager a real server would use.
    async with app.router.lifespan_context(app):
        async with httpx.AsyncClient(transport=transport, base_url="http://panel") as c:
            yield c


async def _login(client: httpx.AsyncClient) -> None:
    """First call against a fresh DB creates the admin account; later calls
    in the same test just need to succeed, which either path satisfies."""
    response = await client.post("/api/v1/auth/login", json={"login": ADMIN_LOGIN, "password": ADMIN_PASSWORD})
    assert response.status_code == 200, response.text


async def test_first_login_creates_admin_account_then_requires_it(client):
    response = await client.get("/api/v1/auth/setup-required")
    assert response.json() == {"setup_required": True}

    response = await client.post("/api/v1/auth/login", json={"login": ADMIN_LOGIN, "password": "short"})
    assert response.status_code == 422  # password too short, no account created yet

    response = await client.post("/api/v1/auth/login", json={"login": ADMIN_LOGIN, "password": ADMIN_PASSWORD})
    assert response.status_code == 200
    assert response.json() == {"authenticated": True}

    response = await client.get("/api/v1/auth/setup-required")
    assert response.json() == {"setup_required": False}

    response = await client.post("/api/v1/auth/login", json={"login": ADMIN_LOGIN, "password": "wrong password"})
    assert response.status_code == 401

    response = await client.post("/api/v1/auth/login", json={"login": ADMIN_LOGIN, "password": ADMIN_PASSWORD})
    assert response.status_code == 200
    assert response.json() == {"authenticated": True}


async def test_unauthenticated_requests_are_rejected(client):
    response = await client.get("/api/v1/cores")
    assert response.status_code == 401


async def test_status_reports_bootstrap_defaults(client):
    await _login(client)
    response = await client.get("/api/v1/status")
    assert response.status_code == 200
    body = response.json()
    assert body["cores"] == 1
    assert body["nodes"] == 0


async def test_core_crud_and_publish(client):
    await _login(client)

    response = await client.get("/api/v1/cores")
    cores = response.json()
    assert len(cores) == 1
    core_id = cores[0]["id"]

    config = {
        "schema_version": 1,
        "inbounds": [{
            "id": "in1", "name": "VPN", "enabled": True, "listen_address": "0.0.0.0", "listen_port": 8002,
            "mode": "tcp", "tcp": True, "udp": True,
        }],
        "outbounds": [{
            "id": "out1", "name": "Server 1", "address": "192.0.2.10", "port": 9000, "enabled": True,
            "health_check": {"enabled": True, "interval_seconds": 10, "timeout_millis": 1000, "failure_threshold": 3, "recovery_threshold": 2},
        }],
        "bindings": [{
            "id": "b1", "name": "VPN binding", "enabled": True, "inbound_id": "in1", "affinity_seconds": 0,
            "selection_strategy": "least", "groups": [], "targets": [{"outbound_id": "out1", "weight_percent": 100}],
        }],
    }
    response = await client.put(f"/api/v1/cores/{core_id}", json={"name": "Default", "description": "", "config": config})
    assert response.status_code == 200, response.text
    body = response.json()
    assert body["core"]["version"] == "v2"
    assert len(body["revision"]["config"]["inbounds"]) == 1


async def test_core_validation_error_returns_422(client):
    await _login(client)
    response = await client.get("/api/v1/cores")
    core_id = response.json()[0]["id"]

    bad_config = {
        "schema_version": 1,
        "inbounds": [{
            "id": "in1", "name": "Bad", "enabled": True, "listen_address": "0.0.0.0", "listen_port": 8002,
            "mode": "tcp", "tcp": True, "udp": False,
        }],
        "outbounds": [],
        "bindings": [{
            "id": "b1", "inbound_id": "in1", "targets": [{"outbound_id": "missing", "weight_percent": 100}],
        }],
    }
    response = await client.put(f"/api/v1/cores/{core_id}", json={"name": "Default", "config": bad_config})
    assert response.status_code == 422


async def test_outbounds_status_reflects_binding_usage_and_node_assignment(client):
    await _login(client)
    core_id = (await client.get("/api/v1/cores")).json()[0]["id"]

    # An outbound with no binding referencing it is "unused" even though
    # the core itself has no nodes yet.
    config = {
        "schema_version": 1,
        "inbounds": [],
        "outbounds": [{
            "id": "out1", "name": "Server 1", "address": "192.0.2.10", "port": 8080, "enabled": True,
            "health_check": {"enabled": True, "interval_seconds": 10, "timeout_millis": 1000, "failure_threshold": 3, "recovery_threshold": 2},
        }],
        "bindings": [],
    }
    response = await client.put(f"/api/v1/cores/{core_id}", json={"name": "Default", "config": config})
    assert response.status_code == 200, response.text

    response = await client.get("/api/v1/outbounds")
    assert response.status_code == 200
    entries = response.json()
    assert len(entries) == 1
    assert entries[0]["outbound_id"] == "out1"
    assert entries[0]["status"] == "unused"

    # Now reference it from a binding — still "unused" because no node has
    # this core applied yet.
    config["inbounds"] = [{"id": "in1", "name": "Web", "enabled": True, "listen_address": "0.0.0.0", "listen_port": 8443, "mode": "tcp", "tcp": True, "udp": False}]
    config["bindings"] = [{"id": "b1", "enabled": True, "inbound_id": "in1", "selection_strategy": "least", "groups": [], "targets": [{"outbound_id": "out1", "weight_percent": 100}]}]
    response = await client.put(f"/api/v1/cores/{core_id}", json={"name": "Default", "config": config})
    assert response.status_code == 200, response.text

    response = await client.get("/api/v1/outbounds")
    assert response.json()[0]["status"] == "unused"

    # A node exists and is assigned to the core, but it has never reported
    # in (status stays "connecting"/"offline"), so it's still "unused".
    await client.post(
        "/api/v1/nodes",
        json={
            "name": "server-1", "ingress_address": "", "control_address": "203.0.113.10",
            "control_port": 62050, "api_key": "a" * 40, "cert_pem": "-----BEGIN CERTIFICATE-----\nMA==\n-----END CERTIFICATE-----\n",
            "core_id": core_id,
        },
    )
    response = await client.get("/api/v1/outbounds")
    assert response.json()[0]["status"] == "unused"


async def test_node_outbound_summary_and_breakdown(client):
    await _login(client)
    core_id = (await client.get("/api/v1/cores")).json()[0]["id"]

    config = {
        "schema_version": 1,
        "inbounds": [{"id": "in1", "name": "Web", "enabled": True, "listen_address": "0.0.0.0", "listen_port": 8443, "mode": "tcp", "tcp": True, "udp": False}],
        "outbounds": [{
            "id": "out1", "name": "Server 1", "address": "192.0.2.20", "port": 9000, "enabled": True,
            "health_check": {"enabled": True, "interval_seconds": 10, "timeout_millis": 1000, "failure_threshold": 3, "recovery_threshold": 2},
        }],
        "bindings": [{"id": "b1", "enabled": True, "inbound_id": "in1", "selection_strategy": "least", "groups": [], "targets": [{"outbound_id": "out1", "weight_percent": 100}]}],
    }
    response = await client.put(f"/api/v1/cores/{core_id}", json={"name": "Default", "config": config})
    assert response.status_code == 200, response.text

    response = await client.post(
        "/api/v1/nodes",
        json={
            "name": "server-1", "control_address": "203.0.113.20", "control_port": 62050,
            "api_key": "a" * 40, "cert_pem": "-----BEGIN CERTIFICATE-----\nMA==\n-----END CERTIFICATE-----\n", "core_id": core_id,
        },
    )
    assert response.status_code == 201, response.text
    node_id = response.json()["node"]["id"]

    response = await client.get("/api/v1/nodes")
    assert response.status_code == 200
    node = next(n for n in response.json() if n["id"] == node_id)
    assert node["outbound_total"] == 1
    assert node["outbound_alive"] == 0  # never polled yet, no health data

    response = await client.get(f"/api/v1/nodes/{node_id}/breakdown")
    assert response.status_code == 200
    body = response.json()
    assert len(body["inbounds"]) == 1
    assert body["inbounds"][0]["inbound_id"] == "in1"
    assert len(body["outbounds"]) == 1
    assert body["outbounds"][0]["outbound_id"] == "out1"
    assert body["outbounds"][0]["reachable"] is False
    assert body["node"] == {"active_ips": 0, "network_rx_bps": 0, "network_tx_bps": 0}

    response = await client.get("/api/v1/nodes/does-not-exist/breakdown")
    assert response.status_code == 404


async def test_node_poll_interval_and_timeout_overrides_are_validated(client):
    await _login(client)
    core_id = (await client.get("/api/v1/cores")).json()[0]["id"]

    response = await client.post(
        "/api/v1/nodes",
        json={
            "name": "server-2", "control_address": "203.0.113.21", "control_port": 62050,
            "api_key": "a" * 40, "cert_pem": "-----BEGIN CERTIFICATE-----\nMA==\n-----END CERTIFICATE-----\n", "core_id": core_id,
            "poll_interval_seconds": 9999,
        },
    )
    assert response.status_code == 422

    response = await client.post(
        "/api/v1/nodes",
        json={
            "name": "server-2", "control_address": "203.0.113.21", "control_port": 62050,
            "api_key": "a" * 40, "cert_pem": "-----BEGIN CERTIFICATE-----\nMA==\n-----END CERTIFICATE-----\n", "core_id": core_id,
            "poll_interval_seconds": 30, "timeout_seconds": 8,
        },
    )
    assert response.status_code == 201, response.text
    node = response.json()["node"]
    assert node["poll_interval_seconds"] == 30
    assert node["timeout_seconds"] == 8


async def test_create_node_one_step_enrollment(client):
    await _login(client)
    core_id = (await client.get("/api/v1/cores")).json()[0]["id"]

    response = await client.post(
        "/api/v1/nodes",
        json={
            "name": "server-1", "ingress_address": "203.0.113.10", "control_address": "203.0.113.10",
            "control_port": 62050, "api_key": "a" * 40, "cert_pem": "-----BEGIN CERTIFICATE-----\nMA==\n-----END CERTIFICATE-----\n",
            "core_id": core_id,
        },
    )
    assert response.status_code == 201, response.text
    body = response.json()
    assert body["node"]["name"] == "server-1"
    assert body["connected"] is False  # nothing actually listening at that address in this test

    response = await client.get("/api/v1/nodes")
    assert len(response.json()) == 1


async def test_create_node_requires_all_enrollment_fields(client):
    await _login(client)
    core_id = (await client.get("/api/v1/cores")).json()[0]["id"]
    response = await client.post(
        "/api/v1/nodes",
        json={"name": "server-2", "control_address": "", "control_port": 62050, "api_key": "", "cert_pem": "", "core_id": core_id},
    )
    assert response.status_code == 422


async def test_settings_roundtrip(client):
    await _login(client)
    response = await client.get("/api/v1/settings")
    assert response.status_code == 200
    assert response.json()["panel_port"] == 8080

    response = await client.put("/api/v1/settings", json={"panel_port": 9090, "poll_interval_seconds": 5, "db_backend": "sqlite"})
    assert response.status_code == 202
