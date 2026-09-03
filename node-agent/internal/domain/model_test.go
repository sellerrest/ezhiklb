package domain

import (
	"testing"
)

func TestDefaultProfileIsValid(t *testing.T) {
	if err := DefaultProfileConfig().Validate(); err != nil {
		t.Fatalf("default profile is invalid: %v", err)
	}
}

func TestSimpleTCPPassthroughIsValid(t *testing.T) {
	config := DefaultProfileConfig()
	config.Inbounds = []Inbound{{ID: "in1", Name: "Web", Enabled: true, ListenAddress: "0.0.0.0", ListenPort: 8002, TCP: true}}
	config.Outbounds = []Outbound{{ID: "out1", Name: "Server 1", Address: "192.0.2.10", Port: 8080, Enabled: true, HealthCheck: DefaultHealthCheck()}}
	config.Bindings = []Binding{{ID: "b1", InboundID: "in1", Mode: BindingModeTCP, SelectionStrategy: SelectionLeast, Targets: []BindingTarget{{OutboundID: "out1", WeightPercent: 100}}}}
	if err := config.Validate(); err != nil {
		t.Fatalf("simple passthrough config is invalid: %v", err)
	}
}

func TestDualProtocolInboundIsValid(t *testing.T) {
	config := DefaultProfileConfig()
	config.Inbounds = []Inbound{{ID: "in1", Name: "VPN", Enabled: true, ListenAddress: "0.0.0.0", ListenPort: 8002, TCP: true, UDP: true}}
	config.Outbounds = []Outbound{{ID: "out1", Name: "Server 1", Address: "192.0.2.10", Port: 8080, Enabled: true, HealthCheck: DefaultHealthCheck()}}
	config.Bindings = []Binding{{ID: "b1", InboundID: "in1", SelectionStrategy: SelectionLeast, Targets: []BindingTarget{{OutboundID: "out1", WeightPercent: 100}}}}
	if err := config.Validate(); err != nil {
		t.Fatalf("dual protocol inbound config is invalid: %v", err)
	}
}

func TestDuplicateHostPortInboundIsRejected(t *testing.T) {
	config := DefaultProfileConfig()
	a := Inbound{ID: "a", Name: "A", Enabled: true, ListenAddress: "0.0.0.0", ListenPort: 8002, TCP: true}
	b := Inbound{ID: "b", Name: "B", Enabled: true, ListenAddress: "0.0.0.0", ListenPort: 8002, TCP: true}
	config.Inbounds = []Inbound{a, b}
	if err := config.Validate(); err == nil {
		t.Fatal("expected a duplicate host:port validation error")
	}
}

func TestWildcardAndSpecificAddressConflictIsRejected(t *testing.T) {
	config := DefaultProfileConfig()
	first := Inbound{ID: "a", Name: "Wildcard", Enabled: true, ListenAddress: "0.0.0.0", ListenPort: 8002, TCP: true}
	second := Inbound{ID: "b", Name: "Specific", Enabled: true, ListenAddress: "192.0.2.20", ListenPort: 8002, TCP: true}
	config.Inbounds = []Inbound{first, second}
	if err := config.Validate(); err == nil {
		t.Fatal("expected wildcard address conflict")
	}
}

func TestPathMatchOnTCPModeBindingIsRejected(t *testing.T) {
	config := DefaultProfileConfig()
	config.Inbounds = []Inbound{{ID: "in1", Name: "Web", Enabled: true, ListenPort: 8002, TCP: true}}
	config.Outbounds = []Outbound{{ID: "out1", Name: "Server 1", Address: "192.0.2.10", Port: 8080, Enabled: true, HealthCheck: DefaultHealthCheck()}}
	config.Bindings = []Binding{{
		ID: "b1", InboundID: "in1", Mode: BindingModeTCP,
		Groups:  []MatchGroup{{Conditions: []MatchCondition{{Field: MatchFieldPath, Operator: MatchOpEquals, Value: "/api"}}}},
		Targets: []BindingTarget{{OutboundID: "out1", WeightPercent: 100}},
	}}
	if err := config.Validate(); err == nil {
		t.Fatal("expected path-match-on-tcp-mode-binding rejection")
	}
}

func TestPathMatchOnHTTPModeBindingIsValid(t *testing.T) {
	config := DefaultProfileConfig()
	config.Inbounds = []Inbound{{ID: "in1", Name: "Web", Enabled: true, ListenPort: 8002, TCP: true}}
	config.Outbounds = []Outbound{{ID: "out1", Name: "Server 1", Address: "192.0.2.10", Port: 8080, Enabled: true, HealthCheck: DefaultHealthCheck()}}
	config.Bindings = []Binding{{
		ID: "b1", InboundID: "in1", Mode: BindingModeHTTP,
		Groups:  []MatchGroup{{Conditions: []MatchCondition{{Field: MatchFieldPath, Operator: MatchOpEquals, Value: "/api"}}}},
		Targets: []BindingTarget{{OutboundID: "out1", WeightPercent: 100}},
	}}
	if err := config.Validate(); err != nil {
		t.Fatalf("path match on an http-mode binding should be valid: %v", err)
	}
}

func TestBindingsOnSameInboundMustShareOneMode(t *testing.T) {
	config := DefaultProfileConfig()
	config.Inbounds = []Inbound{{ID: "in1", Name: "Web", Enabled: true, ListenPort: 8002, TCP: true}}
	config.Outbounds = []Outbound{{ID: "out1", Name: "Server 1", Address: "192.0.2.10", Port: 8080, Enabled: true, HealthCheck: DefaultHealthCheck()}}
	config.Bindings = []Binding{
		{ID: "b1", InboundID: "in1", Mode: BindingModeTCP, Targets: []BindingTarget{{OutboundID: "out1", WeightPercent: 100}}},
		{ID: "b2", InboundID: "in1", Mode: BindingModeHTTP, Targets: []BindingTarget{{OutboundID: "out1", WeightPercent: 100}}},
	}
	if err := config.Validate(); err == nil {
		t.Fatal("expected a mode-conflict validation error")
	}
}

func TestSecondDefaultBindingOnSameInboundIsRejected(t *testing.T) {
	config := DefaultProfileConfig()
	config.Inbounds = []Inbound{{ID: "in1", Name: "Web", Enabled: true, ListenPort: 8002, TCP: true}}
	config.Outbounds = []Outbound{{ID: "out1", Name: "Server 1", Address: "192.0.2.10", Port: 8080, Enabled: true, HealthCheck: DefaultHealthCheck()}}
	config.Bindings = []Binding{
		{ID: "b1", InboundID: "in1", Targets: []BindingTarget{{OutboundID: "out1", WeightPercent: 100}}},
		{ID: "b2", InboundID: "in1", Targets: []BindingTarget{{OutboundID: "out1", WeightPercent: 100}}},
	}
	if err := config.Validate(); err == nil {
		t.Fatal("expected a second-default-binding validation error")
	}
}

func TestDropActionForbidsTargets(t *testing.T) {
	config := DefaultProfileConfig()
	config.Inbounds = []Inbound{{ID: "in1", Name: "Web", Enabled: true, ListenPort: 8002, TCP: true}}
	config.Outbounds = []Outbound{{ID: "out1", Name: "Server 1", Address: "192.0.2.10", Port: 8080, Enabled: true, HealthCheck: DefaultHealthCheck()}}
	config.Bindings = []Binding{{ID: "b1", InboundID: "in1", Action: BindingActionDrop, Targets: []BindingTarget{{OutboundID: "out1", WeightPercent: 100}}}}
	if err := config.Validate(); err == nil {
		t.Fatal("expected a drop-action-with-targets validation error")
	}
}

func TestDropActionWithNoTargetsIsValid(t *testing.T) {
	config := DefaultProfileConfig()
	config.Inbounds = []Inbound{{ID: "in1", Name: "Web", Enabled: true, ListenPort: 8002, TCP: true}}
	config.Bindings = []Binding{{ID: "b1", InboundID: "in1", Action: BindingActionDrop}}
	if err := config.Validate(); err != nil {
		t.Fatalf("drop action with no targets should be valid: %v", err)
	}
}

func TestManualWeightsMustSumTo100(t *testing.T) {
	config := DefaultProfileConfig()
	config.Inbounds = []Inbound{{ID: "in1", Name: "Web", Enabled: true, ListenPort: 8002, TCP: true}}
	config.Outbounds = []Outbound{
		{ID: "out1", Name: "Server 1", Address: "192.0.2.10", Port: 8080, Enabled: true, HealthCheck: DefaultHealthCheck()},
		{ID: "out2", Name: "Server 2", Address: "192.0.2.11", Port: 8080, Enabled: true, HealthCheck: DefaultHealthCheck()},
	}
	config.Bindings = []Binding{{
		ID: "b1", InboundID: "in1", SelectionStrategy: SelectionManual,
		Targets: []BindingTarget{{OutboundID: "out1", WeightPercent: 20}, {OutboundID: "out2", WeightPercent: 70}},
	}}
	if err := config.Validate(); err == nil {
		t.Fatal("expected manual weight sum rejection")
	}
}

// TestParseLegacyFile (migrating configs from the pre-ezhiklb "Ezhik UDP"
// tool) is intentionally not ported: this fork has no in-place upgrade path
// from any earlier tool, so internal/domain/legacy.go was not carried over.
