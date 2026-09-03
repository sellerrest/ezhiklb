"""Domain models and validation for the panel.

The version-compare semantics (``compare_versions``) and the node-facing
wire types below are still a faithful port of the upstream Go project's
``internal/domain/version.go`` / ``model.go``. The core-config shape
(``Inbound``/``Outbound``/``Binding``) is NOT a port — it replaces the
original flat Listener/Backend (IPVS weighted-pool) model with an
inbound/outbound/binding graph that supports SNI- and HTTP-path-based
routing rules.

IMPORTANT — enforcement gap: the node agent (``node-agent/internal/...``,
Go) still only knows the old Listener/Backend shape and drives plain IPVS,
which has no visibility into TLS SNI or HTTP Host/Path — it cannot express
"route this SNI to pool A, that SNI to pool B" through one IPVS service.
Cores saved through this new shape validate and persist correctly, but
nothing on the node enforces binding match rules yet: that requires a new
L7-aware proxy component in the node agent (SNI-sniffing TCP proxy + HTTP
reverse proxy), which has not been built. Simple bindings — one inbound,
one outbound, no match groups — are the only shape close enough to plain
forwarding to reason about once that proxy exists.
"""

from __future__ import annotations

import ipaddress
from datetime import datetime, timezone
from enum import Enum
from typing import Optional

from pydantic import BaseModel, Field

SCHEMA_VERSION = 1


class CoreValidationError(ValueError):
    """Raised by validate_core_config; str(err) is a "; "-joined, sorted list
    of problems, matching the Go ProfileConfig.Validate() error text."""


class Protocol(str, Enum):
    TCP = "tcp"
    UDP = "udp"


class HealthCheck(BaseModel):
    enabled: bool = True
    interval_seconds: int = 10
    timeout_millis: int = 1000
    failure_threshold: int = 3
    recovery_threshold: int = 2


def default_health_check() -> HealthCheck:
    return HealthCheck()


class Inbound(BaseModel):
    """A listening socket on the node: host+port to accept connections on,
    and which L4 protocols to actually listen with (tcp/udp, independently
    toggleable). Whether TCP traffic on it is routed by raw passthrough
    (SNI-only matching) or as HTTP (SNI + URI path matching) is no longer
    decided here — it's a per-Binding choice (see BindingMode), since the
    same socket only ever runs one or the other and a Binding is where an
    operator actually reasons about routing."""

    id: str = ""
    name: str = ""
    enabled: bool = True
    listen_address: str = "0.0.0.0"
    listen_port: int = 0
    tcp: bool = True
    udp: bool = False


class BindingMode(str, Enum):
    TCP = "tcp"
    HTTP = "http"


class Outbound(BaseModel):
    """A backend server: where traffic bound to it is actually forwarded,
    plus its own liveness check (moved here from the old core-wide
    HealthCheck — each backend server is monitored independently)."""

    id: str = ""
    name: str = ""
    address: str = ""
    port: int = 0
    enabled: bool = True
    health_check: HealthCheck = Field(default_factory=default_health_check)


class MatchField(str, Enum):
    SNI = "sni"
    PATH = "path"


class MatchOperator(str, Enum):
    EQUALS = "equals"
    NOT_EQUALS = "not_equals"
    CONTAINS = "contains"
    NOT_CONTAINS = "not_contains"
    STARTS_WITH = "starts_with"
    NOT_STARTS_WITH = "not_starts_with"


class MatchCondition(BaseModel):
    field: MatchField = MatchField.SNI
    operator: MatchOperator = MatchOperator.EQUALS
    value: str = ""


class MatchGroup(BaseModel):
    """Conditions within a group are AND'd; groups on a binding are OR'd —
    a plain disjunctive-normal-form rule set, same shape the reference
    panel's own rule builder produces."""

    conditions: list[MatchCondition] = Field(default_factory=list)


class SelectionStrategy(str, Enum):
    PING = "ping"
    LEAST = "least"
    MANUAL = "manual"


class BindingAction(str, Enum):
    FORWARD = "forward"
    DROP = "drop"


class BindingTarget(BaseModel):
    outbound_id: str = ""
    weight_percent: int = 100


class Binding(BaseModel):
    """Connects one inbound to one or more outbounds. ``mode`` decides
    whether this binding's rules can match SNI only (tcp, raw passthrough)
    or SNI *and* URI path (http, terminated) — every binding sharing the
    same inbound_id must agree on this, since one listening socket only
    ever runs one of the two engines (see validate_core_config).

    An empty ``groups`` list means "match everything" — at most one binding
    per inbound may be this shape, since a non-default rule placed after it
    could never be reached; it acts as that inbound's *default*, always
    evaluated last regardless of list position. ``action`` decides what the
    default does with traffic nothing more specific matched: FORWARD (the
    default action) sends it to ``targets``, sharing it per
    ``selection_strategy``; DROP resets the connection immediately and
    ``targets`` must be empty. DROP only makes sense on the default — a
    binding with real match conditions always forwards; there'd be no
    reason to write a rule that matches specific traffic just to refuse
    it."""

    id: str = ""
    name: str = ""
    enabled: bool = True
    inbound_id: str = ""
    mode: BindingMode = BindingMode.TCP
    action: BindingAction = BindingAction.FORWARD
    affinity_seconds: int = 0
    selection_strategy: SelectionStrategy = SelectionStrategy.LEAST
    groups: list[MatchGroup] = Field(default_factory=list)
    targets: list[BindingTarget] = Field(default_factory=list)


class CoreConfig(BaseModel):
    schema_version: int = SCHEMA_VERSION
    inbounds: list[Inbound] = Field(default_factory=list)
    outbounds: list[Outbound] = Field(default_factory=list)
    bindings: list[Binding] = Field(default_factory=list)


def default_core_config() -> CoreConfig:
    return CoreConfig()


def _is_ipv4(value: str) -> bool:
    try:
        ipaddress.IPv4Address(value)
        return True
    except ValueError:
        return False


def _validate_health_check(h: HealthCheck, prefix: str, problems: list[str]) -> None:
    if not (1 <= h.interval_seconds <= 3600):
        problems.append(f"{prefix}.interval_seconds must be between 1 and 3600")
    if not (100 <= h.timeout_millis <= 30000):
        problems.append(f"{prefix}.timeout_millis must be between 100 and 30000")
    if not (1 <= h.failure_threshold <= 100):
        problems.append(f"{prefix}.failure_threshold must be between 1 and 100")
    if not (1 <= h.recovery_threshold <= 100):
        problems.append(f"{prefix}.recovery_threshold must be between 1 and 100")


def validate_core_config(config: CoreConfig) -> None:
    """Validates the inbound/outbound/binding graph: id uniqueness, address
    shape, no two inbounds sharing the same listen host+port (accounting for
    0.0.0.0 wildcard conflicts), outbound health-check bounds, and that every
    binding references real inbounds/outbounds with well-formed match rules
    and selection weights. Raises CoreValidationError with every problem
    found (sorted, "; "-joined) instead of stopping at the first one."""
    problems: list[str] = []

    if config.schema_version != SCHEMA_VERSION:
        problems.append(f"schema_version must be {SCHEMA_VERSION}")

    inbound_ids: set[str] = set()
    inbounds_by_id: dict[str, Inbound] = {}
    service_keys: dict[str, str] = {}

    for i, inbound in enumerate(config.inbounds):
        prefix = f"inbounds[{i}]"
        inbound_id = inbound.id.strip()
        if not inbound_id:
            problems.append(f"{prefix}.id is required")
        elif inbound.id in inbound_ids:
            problems.append(f"{prefix}.id is duplicated")
        inbound_ids.add(inbound.id)
        inbounds_by_id[inbound.id] = inbound

        if not inbound.name.strip():
            problems.append(f"{prefix}.name is required")
        if inbound.listen_port == 0:
            problems.append(f"{prefix}.listen_port is required")
        if inbound.listen_address and inbound.listen_address != "0.0.0.0":
            if not _is_ipv4(inbound.listen_address):
                problems.append(f"{prefix}.listen_address must be an IPv4 address")
        if not inbound.tcp and not inbound.udp:
            problems.append(f"{prefix} must listen on tcp, udp, or both")

        key = f"{inbound.listen_address}:{inbound.listen_port}"
        owner = service_keys.get(key)
        if owner is not None:
            problems.append(f"{prefix} conflicts with inbound {owner} on the same host and port")

        wildcard_key = f"0.0.0.0:{inbound.listen_port}"
        if inbound.listen_address != "0.0.0.0":
            owner = service_keys.get(wildcard_key)
            if owner is not None:
                problems.append(f"{prefix} conflicts with wildcard inbound {owner}")
        else:
            suffix = f":{inbound.listen_port}"
            for existing_key, existing_owner in service_keys.items():
                if existing_key != key and existing_key.endswith(suffix):
                    problems.append(f"{prefix} conflicts with inbound {existing_owner}")

        service_keys[key] = inbound.id

    outbound_ids: set[str] = set()
    for i, outbound in enumerate(config.outbounds):
        prefix = f"outbounds[{i}]"
        outbound_id = outbound.id.strip()
        if not outbound_id:
            problems.append(f"{prefix}.id is required")
        elif outbound.id in outbound_ids:
            problems.append(f"{prefix}.id is duplicated")
        outbound_ids.add(outbound.id)

        if not outbound.name.strip():
            problems.append(f"{prefix}.name is required")
        if not _is_ipv4(outbound.address):
            problems.append(f"{prefix}.address must be an IPv4 address")
        if outbound.port == 0:
            problems.append(f"{prefix}.port is required")
        _validate_health_check(outbound.health_check, f"{prefix}.health_check", problems)

    binding_ids: set[str] = set()
    mode_by_inbound: dict[str, tuple[int, BindingMode]] = {}
    default_binding_by_inbound: dict[str, int] = {}
    for i, binding in enumerate(config.bindings):
        prefix = f"bindings[{i}]"
        binding_id = binding.id.strip()
        if not binding_id:
            problems.append(f"{prefix}.id is required")
        elif binding.id in binding_ids:
            problems.append(f"{prefix}.id is duplicated")
        binding_ids.add(binding.id)

        inbound = inbounds_by_id.get(binding.inbound_id)
        if inbound is None:
            problems.append(f"{prefix}.inbound_id must reference an existing inbound")
        if not (0 <= binding.affinity_seconds <= 86400):
            problems.append(f"{prefix}.affinity_seconds must be between 0 and 86400")

        if binding.inbound_id:
            # One listening socket only ever runs one engine (raw TCP
            # passthrough or terminated HTTP) — every binding attached to it
            # must agree on which, since mode now lives on the binding, not
            # the inbound it points at.
            first = mode_by_inbound.get(binding.inbound_id)
            if first is None:
                mode_by_inbound[binding.inbound_id] = (i, binding.mode)
            elif first[1] != binding.mode:
                problems.append(f"{prefix}.mode conflicts with bindings[{first[0]}].mode — all bindings for inbound {binding.inbound_id} must share one mode")

        if not binding.groups:
            # An empty group list matches everything — this binding is
            # inbound_id's *default*, always evaluated last regardless of
            # where it sits in the list. Only one makes sense per inbound:
            # a second one could never be reached.
            if binding.inbound_id:
                first_default = default_binding_by_inbound.get(binding.inbound_id)
                if first_default is None:
                    default_binding_by_inbound[binding.inbound_id] = i
                else:
                    problems.append(f"{prefix} is a second default (empty-rule) binding for inbound {binding.inbound_id} — bindings[{first_default}] is already its default")

        for g, group in enumerate(binding.groups):
            if not group.conditions:
                problems.append(f"{prefix}.groups[{g}] must have at least one condition")
            for c, condition in enumerate(group.conditions):
                cond_prefix = f"{prefix}.groups[{g}].conditions[{c}]"
                if not condition.value.strip():
                    problems.append(f"{cond_prefix}.value is required")
                if condition.field == MatchField.PATH and binding.mode != BindingMode.HTTP:
                    problems.append(f"{cond_prefix} matches path but binding {prefix} is not in http mode")

        if binding.action == BindingAction.DROP:
            if binding.groups:
                problems.append(f"{prefix}.action is drop but this binding has match conditions — drop only makes sense on the default (empty-rule) binding, for traffic that matched nothing else")
            if binding.targets:
                problems.append(f"{prefix}.targets must be empty when action is drop")
        else:
            if not binding.targets:
                problems.append(f"{prefix}.targets must have at least one outbound")
        target_ids: set[str] = set()
        weight_total = 0
        for t, target in enumerate(binding.targets):
            target_prefix = f"{prefix}.targets[{t}]"
            if target.outbound_id not in outbound_ids:
                problems.append(f"{target_prefix}.outbound_id must reference an existing outbound")
            if target.outbound_id in target_ids:
                problems.append(f"{target_prefix} duplicates another target in this binding")
            target_ids.add(target.outbound_id)
            if binding.selection_strategy == SelectionStrategy.MANUAL:
                if not (1 <= target.weight_percent <= 100):
                    problems.append(f"{target_prefix}.weight_percent must be between 1 and 100")
                weight_total += target.weight_percent
        if binding.selection_strategy == SelectionStrategy.MANUAL and binding.targets and weight_total != 100:
            problems.append(f"{prefix}.targets weight_percent values must add up to 100")

    if problems:
        problems.sort()
        raise CoreValidationError("; ".join(problems))


# ---------------------------------------------------------------------------
# Version comparison — port of internal/domain/version.go CompareVersions.
# ---------------------------------------------------------------------------


def _parse_version(value: str) -> tuple[list[int], list[str]]:
    trimmed = value.strip()
    if trimmed.startswith("v"):
        trimmed = trimmed[1:]
    parts = trimmed.split("-", 1)
    core_parts = parts[0].split(".")
    core = [0, 0, 0]
    for index in range(3):
        if index < len(core_parts):
            try:
                core[index] = int(core_parts[index])
            except ValueError:
                core[index] = 0
    if len(parts) == 1:
        return core, []
    return core, parts[1].split(".")


def compare_versions(left: str, right: str) -> int:
    """Returns -1, 0 or 1. Faithful port of Go's CompareVersions."""
    l_core, l_pre = _parse_version(left)
    r_core, r_pre = _parse_version(right)
    for index in range(3):
        if l_core[index] < r_core[index]:
            return -1
        if l_core[index] > r_core[index]:
            return 1
    if len(l_pre) == 0 and len(r_pre) > 0:
        return 1
    if len(r_pre) == 0 and len(l_pre) > 0:
        return -1
    depth = max(len(l_pre), len(r_pre))
    for index in range(depth):
        if index >= len(l_pre):
            return -1
        if index >= len(r_pre):
            return 1
        l_seg, r_seg = l_pre[index], r_pre[index]
        l_ok = r_ok = True
        try:
            li = int(l_seg)
        except ValueError:
            l_ok, li = False, 0
        try:
            ri = int(r_seg)
        except ValueError:
            r_ok, ri = False, 0
        if l_ok and r_ok:
            if li < ri:
                return -1
            if li > ri:
                return 1
            continue
        if l_ok:
            return -1
        if r_ok:
            return 1
        if l_seg < r_seg:
            return -1
        if l_seg > r_seg:
            return 1
    return 0


# ---------------------------------------------------------------------------
# Node-facing wire types: what the panel POSTs to a node's local control API,
# and what a node reports back. These replace the old heartbeat/desired-state
# JSON shapes with the same fields, split across the new RPC-style endpoints
# (POST /v1/apply, GET /v1/state, POST /v1/update, POST /v1/health-probe,
# POST /v1/decommission) instead of one shared poll body.
# ---------------------------------------------------------------------------


class NodeApplyRequest(BaseModel):
    """Body of POST /v1/apply — everything the node needs to reconcile its
    local IPVS/iptables state to one core revision.

    Field names deliberately match the node agent's still-Go, unmodified
    ``domain.NodeDesiredState`` wire struct (``profile_id``/``profile_name``)
    even though the panel calls the same entity a "core" in its own DB/API/UI
    — the Профили->Ядра rename is a panel-side UX change, not a wire-protocol
    change, so the proven Go reconciler needed zero source edits. ``node_id``,
    ``health_probe``, ``decommission`` and ``update_version`` are handled by
    their own dedicated endpoints now and are omitted here; the node's Go
    JSON decoder simply zero-fills any struct field absent from the body."""

    ingress_address: str = ""
    revision: int = 0
    profile_id: str = ""
    profile_name: str = ""
    reset_connections: bool = False
    config: CoreConfig = Field(default_factory=default_core_config)


class NodeUpdateRequest(BaseModel):
    target_version: str


class BackendHealth(BaseModel):
    address: str
    state: str = "unknown"
    consecutive_successes: int = Field(0, alias="consecutive_successes")
    consecutive_failures: int = 0
    latency_millis: int = 0
    checked_at: datetime = Field(default_factory=lambda: datetime.now(timezone.utc))

    model_config = {"populate_by_name": True}


class ServiceStat(BaseModel):
    protocol: Protocol
    listen_address: str
    listen_port: int
    backend_address: str = ""
    backend_port: int = 0
    connections: int = 0
    incoming_packets: int = 0
    outgoing_packets: int = 0
    incoming_bytes: int = 0
    outgoing_bytes: int = 0
    collected_at: datetime = Field(default_factory=lambda: datetime.now(timezone.utc))


class NodeMetrics(BaseModel):
    ram_used_percent: float = 0
    cpu_used_percent: float = 0
    load_1: float = 0
    cpu_cores: int = 0
    network_rx_bps: int = 0
    network_tx_bps: int = 0
    active_ips: int = 0
    collected_at: datetime = Field(default_factory=lambda: datetime.now(timezone.utc))


class NodeDiagnostics(BaseModel):
    ipvs_available: bool = False
    firewall_ready: bool = False
    service_count: int = 0
    destination_count: int = 0
    error: str = ""
    checked_at: datetime = Field(default_factory=lambda: datetime.now(timezone.utc))


class OutboundConnStat(BaseModel):
    """Live per-outbound connection/IP count reported by the node's L7
    proxy (internal/proxy in the Go agent) — the "Исходящие" status page's
    online count. ``health`` (keyed the same "address:port" way) is what
    says whether the outbound is currently reachable at all."""

    outbound_id: str
    active_connections: int = 0
    active_ips: int = 0


class NodeStateReport(BaseModel):
    """Body returned by GET /v1/state — what used to be the heartbeat POST
    body, now pulled by the panel instead of pushed by the node."""

    agent_version: str = ""
    applied_revision: int = 0
    apply_state: str = "waiting"
    apply_error: str = ""
    health: list[BackendHealth] = Field(default_factory=list)
    stats: list[ServiceStat] = Field(default_factory=list)
    outbound_stats: list[OutboundConnStat] = Field(default_factory=list)
    metrics: NodeMetrics = Field(default_factory=NodeMetrics)
    diagnostics: Optional[NodeDiagnostics] = None
    update_state: str = "idle"
    update_error: str = ""
