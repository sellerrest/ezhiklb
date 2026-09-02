"""Panel application factory and process entrypoint."""

from __future__ import annotations

import logging
import os
import signal
import sys
import time
from contextlib import asynccontextmanager

import uvicorn
from fastapi import FastAPI, Request
from starlette.middleware.base import BaseHTTPMiddleware

from . import api
from .config import Settings, load_settings
from .db import Database
from .poller import Poller
from .store import Store
from .web import make_static_app

logger = logging.getLogger("ezhiklb_panel")


class SecurityHeadersMiddleware(BaseHTTPMiddleware):
    async def dispatch(self, request: Request, call_next):
        response = await call_next(request)
        response.headers["X-Content-Type-Options"] = "nosniff"
        response.headers["X-Frame-Options"] = "DENY"
        response.headers["Referrer-Policy"] = "no-referrer"
        response.headers["Content-Security-Policy"] = (
            "default-src 'self'; style-src 'self' 'unsafe-inline'; font-src 'self'; "
            "img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'"
        )
        return response


class RequestLogMiddleware(BaseHTTPMiddleware):
    async def dispatch(self, request: Request, call_next):
        started = time.monotonic()
        response = await call_next(request)
        logger.info("%s %s -> %s (%.1fms)", request.method, request.url.path, response.status_code, (time.monotonic() - started) * 1000)
        return response


def create_app(settings: Settings, store: Store, poller: Poller, restart=None) -> FastAPI:
    @asynccontextmanager
    async def lifespan(app: FastAPI):
        await store.db.migrate()
        await store.bootstrap()
        poller.start()
        yield
        await poller.stop()
        await store.db.close()

    app = FastAPI(title="EzhikLB Panel", lifespan=lifespan)
    app.state.store = store
    app.state.poller = poller
    app.state.settings = settings
    app.state.restart = restart

    app.add_middleware(SecurityHeadersMiddleware)
    app.add_middleware(RequestLogMiddleware)
    app.include_router(api.router)
    app.mount("/", make_static_app(settings.web_dir))
    return app


def run() -> None:
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s: %(message)s")
    settings = load_settings()

    db = Database(settings.database_url)
    store = Store(db)
    poller = Poller(store, settings)

    # Settings changes (currently: panel_port) can't be applied to an
    # already-bound uvicorn server in place, so — same approach the Go
    # version used — a save schedules a graceful self-SIGTERM and exits with
    # a distinct code; scripts/install-panel.sh's systemd unit uses
    # Restart=on-failure, so systemd brings the process straight back up
    # with the newly saved port. The browser-side redirect-after-save logic
    # (SettingsPage in the frontend) already expects this exact pattern.
    restarting = {"flag": False}

    def restart() -> None:
        restarting["flag"] = True
        os.kill(os.getpid(), signal.SIGTERM)

    app = create_app(settings, store, poller, restart=restart)
    uvicorn.run(app, host=settings.host, port=settings.port, log_config=None)
    sys.exit(75 if restarting["flag"] else 0)


if __name__ == "__main__":
    run()
