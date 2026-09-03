"""Environment-driven configuration for the panel process."""

from __future__ import annotations

import os
from dataclasses import dataclass


def _env(key: str, fallback: str) -> str:
    value = os.environ.get(key)
    return value if value else fallback


def _env_int(key: str, fallback: int) -> int:
    value = os.environ.get(key)
    if not value:
        return fallback
    try:
        return int(value)
    except ValueError:
        return fallback


@dataclass(frozen=True)
class Settings:
    database_url: str
    secure_cookie: bool
    web_dir: str
    host: str
    port: int
    poll_interval_seconds: int
    node_offline_after_seconds: int
    node_state_timeout_seconds: float
    node_apply_timeout_seconds: float


def load_settings() -> Settings:
    return Settings(
        database_url=_env("EZHIKLB_DATABASE_URL", "sqlite+aiosqlite:////var/lib/ezhiklb/ezhiklb.db"),
        secure_cookie=_env("EZHIKLB_SECURE_COOKIE", "1") == "1",
        web_dir=_env("EZHIKLB_WEB_DIR", "/usr/share/ezhiklb/web"),
        host=_env("EZHIKLB_HOST", "127.0.0.1"),
        port=_env_int("EZHIKLB_PORT", 8080),
        poll_interval_seconds=_env_int("EZHIKLB_POLL_INTERVAL_SECONDS", 5),
        node_offline_after_seconds=_env_int("EZHIKLB_NODE_OFFLINE_AFTER_SECONDS", 20),
        node_state_timeout_seconds=float(_env_int("EZHIKLB_NODE_STATE_TIMEOUT_MS", 4000)) / 1000,
        node_apply_timeout_seconds=float(_env_int("EZHIKLB_NODE_APPLY_TIMEOUT_MS", 25000)) / 1000,
    )


PANEL_VERSION = "1.0.13"
