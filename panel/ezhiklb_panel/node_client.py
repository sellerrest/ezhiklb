"""HTTP client for the panel-dials-out-to-node control protocol (point 2 of
the fork). Every call presents the node's pinned API key as a Bearer token
and verifies the node's TLS certificate against the exact PEM pinned at
enrollment (see security.build_pinned_ssl_context) — no public CA involved.
"""

from __future__ import annotations

from typing import Any, Optional

import httpx

from .security import build_pinned_ssl_context


class NodeUnreachableError(Exception):
    pass


class NodeRejectedError(Exception):
    """The node answered but refused the request (e.g. invalid core config,
    or an apply that failed) — distinct from being unreachable at all, since
    the caller (poller.py) treats these very differently: unreachable means
    "retry later, mark offline"; rejected means "the node is alive and the
    apply itself failed", the same distinction the old design made between a
    transport error and an apply_error in the heartbeat body."""

    def __init__(self, status_code: int, message: str):
        super().__init__(message)
        self.status_code = status_code


class NodeClient:
    def __init__(self, control_address: str, control_port: int, api_key: str, cert_pem: str):
        self.base_url = f"https://{control_address}:{control_port}"
        self.api_key = api_key
        self.cert_pem = cert_pem

    @classmethod
    def from_dial_info(cls, dial_info: dict) -> "NodeClient":
        return cls(dial_info["control_address"], dial_info["control_port"], dial_info["api_key"], dial_info["cert_pem"])

    def _client(self, timeout: float) -> httpx.AsyncClient:
        ssl_context = build_pinned_ssl_context(self.cert_pem)
        return httpx.AsyncClient(
            base_url=self.base_url, verify=ssl_context, timeout=timeout,
            headers={"Authorization": f"Bearer {self.api_key}"},
        )

    async def _request(self, method: str, path: str, timeout: float, json_body: Optional[dict] = None) -> dict:
        try:
            async with self._client(timeout) as client:
                response = await client.request(method, path, json=json_body)
        except (httpx.ConnectError, httpx.ConnectTimeout, httpx.ReadTimeout, httpx.TransportError, OSError) as exc:
            raise NodeUnreachableError(str(exc)) from exc
        if response.status_code >= 400:
            message = response.text
            try:
                body = response.json()
                message = body.get("error", {}).get("message", message)
            except Exception:
                pass
            raise NodeRejectedError(response.status_code, message)
        if not response.content:
            return {}
        return response.json()

    async def health(self, timeout: float = 3.0) -> dict:
        return await self._request("GET", "/health", timeout)

    async def get_state(self, timeout: float = 4.0) -> dict:
        return await self._request("GET", "/v1/state", timeout)

    async def apply(self, desired_state: dict, timeout: float = 25.0) -> dict:
        return await self._request("POST", "/v1/apply", timeout, json_body=desired_state)

    async def request_update(self, target_version: str, timeout: float = 5.0) -> dict:
        return await self._request("POST", "/v1/update", timeout, json_body={"target_version": target_version})

    async def health_probe(self, timeout: float = 10.0) -> dict:
        return await self._request("POST", "/v1/health-probe", timeout)

    async def decommission(self, timeout: float = 15.0) -> dict:
        return await self._request("POST", "/v1/decommission", timeout)


def make_client(dial_info: dict) -> NodeClient:
    return NodeClient.from_dial_info(dial_info)
