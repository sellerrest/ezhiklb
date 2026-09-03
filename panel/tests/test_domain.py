"""Tests for the inbound/outbound/binding core-config validation, plus the
version-compare port of internal/domain/version_test.go.
"""

import pytest

from ezhiklb_panel.domain import (
    Binding,
    BindingAction,
    BindingMode,
    BindingTarget,
    CoreConfig,
    CoreValidationError,
    Inbound,
    MatchCondition,
    MatchField,
    MatchGroup,
    Outbound,
    SelectionStrategy,
    compare_versions,
    default_core_config,
    validate_core_config,
)


def test_default_core_is_valid():
    validate_core_config(default_core_config())


def test_simple_tcp_passthrough_is_valid():
    config = default_core_config()
    config.inbounds = [Inbound(id="in1", name="Web", listen_address="0.0.0.0", listen_port=8002, tcp=True, udp=False)]
    config.outbounds = [Outbound(id="out1", name="Server 1", address="192.0.2.10", port=8080)]
    config.bindings = [Binding(id="b1", name="Web binding", inbound_id="in1", mode=BindingMode.TCP, targets=[BindingTarget(outbound_id="out1")])]
    validate_core_config(config)


def test_dual_protocol_inbound_is_valid():
    config = default_core_config()
    config.inbounds = [Inbound(id="in1", name="VPN", listen_address="0.0.0.0", listen_port=8002, tcp=True, udp=True)]
    config.outbounds = [Outbound(id="out1", name="Server 1", address="192.0.2.10", port=8080)]
    config.bindings = [Binding(id="b1", inbound_id="in1", targets=[BindingTarget(outbound_id="out1")])]
    validate_core_config(config)


def test_duplicate_host_port_inbound_is_rejected():
    config = default_core_config()
    a = Inbound(id="a", name="A", listen_address="0.0.0.0", listen_port=8002, tcp=True)
    b = Inbound(id="b", name="B", listen_address="0.0.0.0", listen_port=8002, tcp=True)
    config.inbounds = [a, b]
    with pytest.raises(CoreValidationError):
        validate_core_config(config)


def test_wildcard_and_specific_address_conflict_is_rejected():
    config = default_core_config()
    first = Inbound(id="a", name="Wildcard", listen_address="0.0.0.0", listen_port=8002, tcp=True)
    second = Inbound(id="b", name="Specific", listen_address="192.0.2.20", listen_port=8002, tcp=True)
    config.inbounds = [first, second]
    with pytest.raises(CoreValidationError):
        validate_core_config(config)


def test_binding_referencing_unknown_outbound_is_rejected():
    config = default_core_config()
    config.inbounds = [Inbound(id="in1", name="Web", listen_port=8002, tcp=True)]
    config.bindings = [Binding(id="b1", inbound_id="in1", targets=[BindingTarget(outbound_id="missing")])]
    with pytest.raises(CoreValidationError):
        validate_core_config(config)


def test_path_match_on_tcp_mode_binding_is_rejected():
    config = default_core_config()
    config.inbounds = [Inbound(id="in1", name="Web", listen_port=8002, tcp=True)]
    config.outbounds = [Outbound(id="out1", name="Server 1", address="192.0.2.10", port=8080)]
    config.bindings = [
        Binding(
            id="b1",
            inbound_id="in1",
            mode=BindingMode.TCP,
            groups=[MatchGroup(conditions=[MatchCondition(field=MatchField.PATH, value="/api")])],
            targets=[BindingTarget(outbound_id="out1")],
        )
    ]
    with pytest.raises(CoreValidationError):
        validate_core_config(config)


def test_path_match_on_http_mode_binding_is_valid():
    config = default_core_config()
    config.inbounds = [Inbound(id="in1", name="Web", listen_port=8002, tcp=True)]
    config.outbounds = [Outbound(id="out1", name="Server 1", address="192.0.2.10", port=8080)]
    config.bindings = [
        Binding(
            id="b1",
            inbound_id="in1",
            mode=BindingMode.HTTP,
            groups=[MatchGroup(conditions=[MatchCondition(field=MatchField.PATH, value="/api")])],
            targets=[BindingTarget(outbound_id="out1")],
        )
    ]
    validate_core_config(config)


def test_manual_weights_must_sum_to_100():
    config = default_core_config()
    config.inbounds = [Inbound(id="in1", name="Web", listen_port=8002, tcp=True)]
    config.outbounds = [
        Outbound(id="out1", name="Server 1", address="192.0.2.10", port=8080),
        Outbound(id="out2", name="Server 2", address="192.0.2.11", port=8080),
    ]
    config.bindings = [
        Binding(
            id="b1",
            inbound_id="in1",
            selection_strategy=SelectionStrategy.MANUAL,
            targets=[BindingTarget(outbound_id="out1", weight_percent=20), BindingTarget(outbound_id="out2", weight_percent=70)],
        )
    ]
    with pytest.raises(CoreValidationError):
        validate_core_config(config)


def test_bindings_on_the_same_inbound_must_share_one_mode():
    config = default_core_config()
    config.inbounds = [Inbound(id="in1", name="Web", listen_port=8002, tcp=True)]
    config.outbounds = [Outbound(id="out1", name="Server 1", address="192.0.2.10", port=8080)]
    config.bindings = [
        Binding(id="b1", inbound_id="in1", mode=BindingMode.TCP, targets=[BindingTarget(outbound_id="out1")]),
        Binding(id="b2", inbound_id="in1", mode=BindingMode.HTTP, targets=[BindingTarget(outbound_id="out1")]),
    ]
    with pytest.raises(CoreValidationError):
        validate_core_config(config)


def test_second_default_binding_on_the_same_inbound_is_rejected():
    config = default_core_config()
    config.inbounds = [Inbound(id="in1", name="Web", listen_port=8002, tcp=True)]
    config.outbounds = [Outbound(id="out1", name="Server 1", address="192.0.2.10", port=8080)]
    config.bindings = [
        Binding(id="b1", inbound_id="in1", groups=[], targets=[BindingTarget(outbound_id="out1")]),
        Binding(id="b2", inbound_id="in1", groups=[], targets=[BindingTarget(outbound_id="out1")]),
    ]
    with pytest.raises(CoreValidationError):
        validate_core_config(config)


def test_drop_action_forbids_targets():
    config = default_core_config()
    config.inbounds = [Inbound(id="in1", name="Web", listen_port=8002, tcp=True)]
    config.outbounds = [Outbound(id="out1", name="Server 1", address="192.0.2.10", port=8080)]
    config.bindings = [Binding(id="b1", inbound_id="in1", action=BindingAction.DROP, targets=[BindingTarget(outbound_id="out1")])]
    with pytest.raises(CoreValidationError):
        validate_core_config(config)


def test_drop_action_with_no_targets_is_valid():
    config = default_core_config()
    config.inbounds = [Inbound(id="in1", name="Web", listen_port=8002, tcp=True)]
    config.bindings = [Binding(id="b1", inbound_id="in1", action=BindingAction.DROP, targets=[])]
    validate_core_config(config)


def test_drop_action_on_a_non_default_binding_is_rejected():
    """Drop only makes sense on the default (empty-rule) binding — a rule
    with real match conditions should always forward; there'd be no reason
    to write a rule that matches specific traffic just to refuse it."""
    config = default_core_config()
    config.inbounds = [Inbound(id="in1", name="Web", listen_port=8002, tcp=True)]
    config.bindings = [
        Binding(
            id="b1",
            inbound_id="in1",
            action=BindingAction.DROP,
            groups=[MatchGroup(conditions=[MatchCondition(field=MatchField.SNI, value="blocked.example")])],
            targets=[],
        )
    ]
    with pytest.raises(CoreValidationError):
        validate_core_config(config)


@pytest.mark.parametrize(
    "left,right,want",
    [
        ("0.1.0-beta.3.3", "0.1.0-beta.3.2", 1),
        ("0.1.0-beta.3.1", "0.1.0-beta.3.3", -1),
        ("0.1.0", "0.1.0-beta.3.3", 1),
        ("v0.1.0-beta.3.3", "0.1.0-beta.3.3", 0),
    ],
)
def test_compare_versions(left, right, want):
    assert compare_versions(left, right) == want
