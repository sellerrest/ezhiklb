"""Tests for the `ezhik-lb admins` CLI implementation — exercises the same
async handlers the script's argparse dispatch calls, with getpass/input
monkeypatched so nothing here needs a real terminal."""

from __future__ import annotations

import uuid

import pytest

from ezhiklb_panel import admin_cli
from ezhiklb_panel.db import Database
from ezhiklb_panel.store import Store


@pytest.fixture
async def store(tmp_path):
    db_path = tmp_path / f"{uuid.uuid4().hex}.db"
    db = Database(f"sqlite+aiosqlite:///{db_path}")
    await db.migrate()
    s = Store(db)
    yield s
    await db.close()


def _stub_password(monkeypatch, value: str) -> None:
    monkeypatch.setattr(admin_cli.getpass, "getpass", lambda *_a, **_kw: value)


async def test_add_list_passwd_remove_flow(store, monkeypatch, capsys):
    _stub_password(monkeypatch, "correct horse battery staple")
    assert await admin_cli._add(store, "alice") == 0
    assert "добавлен" in capsys.readouterr().out

    assert await admin_cli._list(store) == 0
    assert "alice" in capsys.readouterr().out

    _stub_password(monkeypatch, "a-new-password-123")
    assert await admin_cli._passwd(store, "alice") == 0
    account = await store.get_admin_by_username("alice")
    from ezhiklb_panel.security import verify_password

    assert verify_password("a-new-password-123", account["password_hash"])

    monkeypatch.setattr("builtins.input", lambda *_a, **_kw: "yes")
    assert await admin_cli._remove(store, "alice") == 1  # last admin, refused
    monkeypatch.setattr(admin_cli.getpass, "getpass", lambda *_a, **_kw: "second-password-123")
    await admin_cli._add(store, "bob")
    assert await admin_cli._remove(store, "alice") == 0
    assert await store.get_admin_by_username("alice") is None


async def test_add_rejects_short_username(store, monkeypatch, capsys):
    assert await admin_cli._add(store, "ab") == 1
    assert "3 символов" in capsys.readouterr().err


async def test_passwd_unknown_admin(store, capsys):
    assert await admin_cli._passwd(store, "ghost") == 1
    assert "не найден" in capsys.readouterr().err


async def test_remove_declined_confirmation(store, monkeypatch, capsys):
    _stub_password(monkeypatch, "correct horse battery staple")
    await admin_cli._add(store, "alice")
    await admin_cli._add(store, "bob")
    monkeypatch.setattr("builtins.input", lambda *_a, **_kw: "no")
    assert await admin_cli._remove(store, "bob") == 1
    assert await store.get_admin_by_username("bob") is not None
