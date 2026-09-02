"""Serves the built React frontend, with an SPA fallback to index.html and
long-cache headers for hashed /assets/ files — port of server.go's static()."""

from __future__ import annotations

import os

from starlette.applications import Starlette
from starlette.requests import Request
from starlette.responses import FileResponse, PlainTextResponse, Response
from starlette.routing import Route


def make_static_app(web_dir: str) -> Starlette:
    async def handler(request: Request) -> Response:
        if not web_dir:
            return PlainTextResponse("not found", status_code=404)
        path = request.path_params.get("path", "")
        clean = os.path.normpath(os.path.join(web_dir, path.lstrip("/"))) if path else os.path.join(web_dir, "index.html")
        if not clean.startswith(os.path.normpath(web_dir)) or not os.path.isfile(clean):
            clean = os.path.join(web_dir, "index.html")
        headers = {}
        if os.path.basename(clean) == "index.html":
            headers["Cache-Control"] = "no-cache"
        elif os.path.join(web_dir, "assets") in clean:
            headers["Cache-Control"] = "public, max-age=31536000, immutable"
        if not os.path.isfile(clean):
            return PlainTextResponse("not found", status_code=404)
        return FileResponse(clean, headers=headers)

    return Starlette(routes=[Route("/{path:path}", handler, methods=["GET"])])
