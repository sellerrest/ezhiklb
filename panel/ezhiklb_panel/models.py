"""Row-level API/response models for the panel — the Python analogue of the
Go project's Profile/Node/AuditEvent structs in internal/domain/model.go,
renamed Profile -> Core per the panel UX rework. These are plain Pydantic
models used directly as FastAPI response shapes; domain.py holds the
core-config wire/validation types shared with the node agent's protocol.
"""

from __future__ import annotations

from datetime import datetime
from typing import Optional

from pydantic import BaseModel

from .domain import CoreConfig, NodeDiagnostics, NodeMetrics


class Core(BaseModel):
    id: str
    name: str
    description: str = ""
    current_revision: int = 0
    auto_version: bool = True
    version: str = "v1"
    created_at: datetime
    updated_at: datetime


class CoreRevision(BaseModel):
    id: int
    core_id: str
    number: int
    version: str
    config: CoreConfig
    created_at: datetime


class Node(BaseModel):
    id: str
    name: str
    ingress_address: str = ""
    control_address: str = ""
    control_port: int = 0
    cert_fingerprint: str = ""
    core_id: str = ""
    desired_revision: int = 0
    applied_revision: int = 0
    agent_version: str = ""
    status: str = "connecting"  # connecting | online | offline | disabled | deleting
    apply_state: str = "waiting"
    apply_error: str = ""
    last_seen_at: Optional[datetime] = None
    online_since: Optional[datetime] = None
    metrics: Optional[NodeMetrics] = None
    diagnostics: Optional[NodeDiagnostics] = None
    update_target: str = ""
    update_state: str = "idle"
    update_error: str = ""
    enabled: bool = True
    # Per-node overrides — None means "use the panel-wide default from
    # Settings". Surfaced in the node's "Расширенные настройки", not the
    # global Settings page anymore.
    poll_interval_seconds: Optional[int] = None
    timeout_seconds: Optional[int] = None
    # Computed by the API layer (not stored) from this node's core config +
    # health data: how many of its used outbounds are currently reachable.
    outbound_alive: int = 0
    outbound_total: int = 0
    created_at: datetime
    updated_at: datetime


class AuditEvent(BaseModel):
    id: int
    action: str
    target_type: str
    target_id: str
    details: str
    created_at: datetime


class NodeMetricPoint(BaseModel):
    node_id: str
    ram_used_percent: float = 0
    cpu_used_percent: float = 0
    load_1: float = 0
    network_rx_bps: int = 0
    network_tx_bps: int = 0
    active_ips: int = 0
    collected_at: datetime


class SystemSettings(BaseModel):
    panel_port: int = 8080
    db_backend: str = "sqlite"
