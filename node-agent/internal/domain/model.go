package domain

import (
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"time"
)

const SchemaVersion = 1

type Protocol string

const (
	ProtocolTCP Protocol = "tcp"
	ProtocolUDP Protocol = "udp"
)

type HealthCheck struct {
	Enabled           bool `json:"enabled"`
	IntervalSeconds   int  `json:"interval_seconds"`
	TimeoutMillis     int  `json:"timeout_millis"`
	FailureThreshold  int  `json:"failure_threshold"`
	RecoveryThreshold int  `json:"recovery_threshold"`
}

func DefaultHealthCheck() HealthCheck {
	return HealthCheck{
		Enabled:           true,
		IntervalSeconds:   10,
		TimeoutMillis:     1000,
		FailureThreshold:  3,
		RecoveryThreshold: 2,
	}
}

// Inbound is a listening socket on the node. Unlike the old Listener, it no
// longer owns a static backend pool directly — which outbounds receive its
// traffic, and under what SNI/path rules, is decided by the Bindings that
// reference it. TCP and UDP are independently toggleable: TCP traffic is
// routed by the internal/proxy package (SNI/HTTP-aware); UDP has no such
// signal to route by, so it is still forwarded via IPVS as one flat pool
// (see reconciler.go's compileServices) — unless a Binding on this inbound
// carries real match rules, in which case UDP is not forwarded at all (UDP
// carries no SNI/Host to evaluate those rules against).
//
// Whether TCP traffic here is routed by raw passthrough (SNI-only) or as
// HTTP (SNI + URI path) is not decided here — see BindingMode: the same
// socket only ever runs one of the two engines, and a Binding is where an
// operator actually reasons about routing.
type Inbound struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Enabled       bool   `json:"enabled"`
	ListenAddress string `json:"listen_address"`
	ListenPort    uint16 `json:"listen_port"`
	TCP           bool   `json:"tcp"`
	UDP           bool   `json:"udp"`
}

type BindingMode string

const (
	BindingModeTCP  BindingMode = "tcp"
	BindingModeHTTP BindingMode = "http"
)

// Outbound is a backend server, monitored independently (each outbound
// carries its own HealthCheck now, not one shared per core).
type Outbound struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Address     string      `json:"address"`
	Port        uint16      `json:"port"`
	Enabled     bool        `json:"enabled"`
	HealthCheck HealthCheck `json:"health_check"`
}

type MatchField string

const (
	MatchFieldSNI  MatchField = "sni"
	MatchFieldPath MatchField = "path"
)

type MatchOperator string

const (
	MatchOpEquals        MatchOperator = "equals"
	MatchOpNotEquals     MatchOperator = "not_equals"
	MatchOpContains      MatchOperator = "contains"
	MatchOpNotContains   MatchOperator = "not_contains"
	MatchOpStartsWith    MatchOperator = "starts_with"
	MatchOpNotStartsWith MatchOperator = "not_starts_with"
)

type MatchCondition struct {
	Field    MatchField    `json:"field"`
	Operator MatchOperator `json:"operator"`
	Value    string        `json:"value"`
}

// MatchGroup's conditions are AND'd; a Binding's groups are OR'd — a plain
// disjunctive-normal-form rule set.
type MatchGroup struct {
	Conditions []MatchCondition `json:"conditions"`
}

type SelectionStrategy string

const (
	SelectionPing   SelectionStrategy = "ping"
	SelectionLeast  SelectionStrategy = "least"
	SelectionManual SelectionStrategy = "manual"
)

type BindingAction string

const (
	BindingActionForward BindingAction = "forward"
	BindingActionDrop    BindingAction = "drop"
)

type BindingTarget struct {
	OutboundID    string `json:"outbound_id"`
	WeightPercent int    `json:"weight_percent"`
}

// Binding connects one Inbound to one or more Outbounds. Mode decides
// whether this binding's rules can match SNI only (tcp, raw passthrough) or
// SNI *and* URI path (http, terminated) — every binding sharing the same
// InboundID must agree on this (see Validate), since one listening socket
// only ever runs one of the two engines.
//
// An empty Groups list matches everything — at most one binding per inbound
// may be this shape, since a non-default rule placed after it could never
// be reached; it acts as that inbound's *default*, always evaluated last
// regardless of list position (see proxy.Manager.Apply). Action decides
// what the default does with traffic nothing more specific matched:
// forward (the default action) sends it to Targets, sharing it per
// SelectionStrategy; drop resets the connection immediately and Targets
// must be empty. Drop only makes sense on the default — a binding with
// real match conditions always forwards; there'd be no reason to write a
// rule that matches specific traffic just to refuse it.
type Binding struct {
	ID                string            `json:"id"`
	Name              string            `json:"name"`
	Enabled           bool              `json:"enabled"`
	InboundID         string            `json:"inbound_id"`
	Mode              BindingMode       `json:"mode"`
	Action            BindingAction     `json:"action"`
	AffinitySecs      int               `json:"affinity_seconds"`
	SelectionStrategy SelectionStrategy `json:"selection_strategy"`
	Groups            []MatchGroup      `json:"groups"`
	Targets           []BindingTarget   `json:"targets"`
}

type ProfileConfig struct {
	SchemaVersion int        `json:"schema_version"`
	Inbounds      []Inbound  `json:"inbounds"`
	Outbounds     []Outbound `json:"outbounds"`
	Bindings      []Binding  `json:"bindings"`
}

type Profile struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Description     string    `json:"description"`
	CurrentRevision int64     `json:"current_revision"`
	AutoVersion     bool      `json:"auto_version"`
	Version         string    `json:"version"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type Revision struct {
	ID        int64         `json:"id"`
	ProfileID string        `json:"profile_id"`
	Number    int64         `json:"number"`
	Version   string        `json:"version"`
	Config    ProfileConfig `json:"config"`
	CreatedAt time.Time     `json:"created_at"`
}

type Node struct {
	ID              string           `json:"id"`
	Name            string           `json:"name"`
	IngressAddress  string           `json:"ingress_address"`
	ObservedAddress string           `json:"observed_address"`
	ProfileID       string           `json:"profile_id"`
	DesiredRevision int64            `json:"desired_revision"`
	AppliedRevision int64            `json:"applied_revision"`
	AgentVersion    string           `json:"agent_version"`
	Status          string           `json:"status"`
	ApplyState      string           `json:"apply_state"`
	LastSeenAt      *time.Time       `json:"last_seen_at,omitempty"`
	OnlineSince     *time.Time       `json:"online_since,omitempty"`
	LastError       string           `json:"last_error,omitempty"`
	Metrics         *NodeMetrics     `json:"metrics,omitempty"`
	Diagnostics     *NodeDiagnostics `json:"diagnostics,omitempty"`
	UpdateTarget    string           `json:"update_target,omitempty"`
	UpdateState     string           `json:"update_state,omitempty"`
	UpdateError     string           `json:"update_error,omitempty"`
	CreatedAt       time.Time        `json:"created_at"`
	UpdatedAt       time.Time        `json:"updated_at"`
}

type NodeDiagnostics struct {
	IPVSAvailable    bool      `json:"ipvs_available"`
	FirewallReady    bool      `json:"firewall_ready"`
	ServiceCount     int       `json:"service_count"`
	DestinationCount int       `json:"destination_count"`
	Error            string    `json:"error,omitempty"`
	CheckedAt        time.Time `json:"checked_at"`
}

type NodeMetrics struct {
	RAMUsedPercent float64   `json:"ram_used_percent"`
	CPUUsedPercent float64   `json:"cpu_used_percent"`
	Load1          float64   `json:"load_1"`
	CPUCores       int       `json:"cpu_cores"`
	NetworkRxBPS   uint64    `json:"network_rx_bps"`
	NetworkTxBPS   uint64    `json:"network_tx_bps"`
	ActiveIPs      int       `json:"active_ips"`
	CollectedAt    time.Time `json:"collected_at"`
}

type NodeMetricPoint struct {
	NodeID         string    `json:"node_id"`
	RAMUsedPercent float64   `json:"ram_used_percent"`
	CPUUsedPercent float64   `json:"cpu_used_percent"`
	Load1          float64   `json:"load_1"`
	NetworkRxBPS   uint64    `json:"network_rx_bps"`
	NetworkTxBPS   uint64    `json:"network_tx_bps"`
	ActiveIPs      int       `json:"active_ips"`
	CollectedAt    time.Time `json:"collected_at"`
}

type SystemSettings struct {
	PanelPort       int `json:"panel_port"`
	AgentPort       int `json:"agent_port"`
	LegacyPanelPort int `json:"legacy_panel_port,omitempty"`
	LegacyAgentPort int `json:"legacy_agent_port,omitempty"`
}

type AuditEvent struct {
	ID         int64     `json:"id"`
	Action     string    `json:"action"`
	TargetType string    `json:"target_type"`
	TargetID   string    `json:"target_id"`
	Details    string    `json:"details"`
	CreatedAt  time.Time `json:"created_at"`
}

type NodeDesiredState struct {
	NodeID           string        `json:"node_id"`
	IngressAddress   string        `json:"ingress_address"`
	Revision         int64         `json:"revision"`
	ProfileID        string        `json:"profile_id"`
	ProfileName      string        `json:"profile_name"`
	HealthProbe      int64         `json:"health_probe"`
	ResetConnections bool          `json:"reset_connections,omitempty"`
	Decommission     bool          `json:"decommission"`
	UpdateVersion    string        `json:"update_version,omitempty"`
	Config           ProfileConfig `json:"config"`
}

type BackendHealth struct {
	NodeID          string    `json:"node_id,omitempty"`
	Address         string    `json:"address"`
	State           string    `json:"state"`
	ConsecutiveUp   int       `json:"consecutive_successes"`
	ConsecutiveDown int       `json:"consecutive_failures"`
	LatencyMillis   int64     `json:"latency_millis"`
	CheckedAt       time.Time `json:"checked_at"`
}

type ServiceStat struct {
	NodeID          string    `json:"node_id,omitempty"`
	Protocol        Protocol  `json:"protocol"`
	ListenAddress   string    `json:"listen_address"`
	ListenPort      uint16    `json:"listen_port"`
	BackendAddress  string    `json:"backend_address,omitempty"`
	BackendPort     uint16    `json:"backend_port,omitempty"`
	Connections     uint64    `json:"connections"`
	IncomingPackets uint64    `json:"incoming_packets"`
	OutgoingPackets uint64    `json:"outgoing_packets"`
	IncomingBytes   uint64    `json:"incoming_bytes"`
	OutgoingBytes   uint64    `json:"outgoing_bytes"`
	CollectedAt     time.Time `json:"collected_at"`
}

func validateHealthCheck(h HealthCheck, prefix string, problems *[]string) {
	if h.IntervalSeconds < 1 || h.IntervalSeconds > 3600 {
		*problems = append(*problems, prefix+".interval_seconds must be between 1 and 3600")
	}
	if h.TimeoutMillis < 100 || h.TimeoutMillis > 30000 {
		*problems = append(*problems, prefix+".timeout_millis must be between 100 and 30000")
	}
	if h.FailureThreshold < 1 || h.FailureThreshold > 100 {
		*problems = append(*problems, prefix+".failure_threshold must be between 1 and 100")
	}
	if h.RecoveryThreshold < 1 || h.RecoveryThreshold > 100 {
		*problems = append(*problems, prefix+".recovery_threshold must be between 1 and 100")
	}
}

// Validate checks the inbound/outbound/binding graph: id uniqueness, address
// shape, no two inbounds sharing the same listen host+port, outbound
// health-check bounds, and that every binding references real
// inbounds/outbounds with well-formed match rules and selection weights.
func (c ProfileConfig) Validate() error {
	var problems []string
	if c.SchemaVersion != SchemaVersion {
		problems = append(problems, fmt.Sprintf("schema_version must be %d", SchemaVersion))
	}

	inboundByID := map[string]Inbound{}
	inboundIDs := map[string]bool{}
	serviceKeys := map[string]string{}
	for i, inbound := range c.Inbounds {
		prefix := fmt.Sprintf("inbounds[%d]", i)
		if strings.TrimSpace(inbound.ID) == "" {
			problems = append(problems, prefix+".id is required")
		} else if inboundIDs[inbound.ID] {
			problems = append(problems, prefix+".id is duplicated")
		}
		inboundIDs[inbound.ID] = true
		inboundByID[inbound.ID] = inbound

		if strings.TrimSpace(inbound.Name) == "" {
			problems = append(problems, prefix+".name is required")
		}
		if inbound.ListenPort == 0 {
			problems = append(problems, prefix+".listen_port is required")
		}
		if inbound.ListenAddress != "" && inbound.ListenAddress != "0.0.0.0" {
			if addr, err := netip.ParseAddr(inbound.ListenAddress); err != nil || !addr.Is4() {
				problems = append(problems, prefix+".listen_address must be an IPv4 address")
			}
		}
		if !inbound.TCP && !inbound.UDP {
			problems = append(problems, prefix+" must listen on tcp, udp, or both")
		}

		key := fmt.Sprintf("%s:%d", inbound.ListenAddress, inbound.ListenPort)
		if owner, exists := serviceKeys[key]; exists {
			problems = append(problems, fmt.Sprintf("%s conflicts with inbound %s on the same host and port", prefix, owner))
		}
		wildcardKey := fmt.Sprintf("0.0.0.0:%d", inbound.ListenPort)
		if inbound.ListenAddress != "0.0.0.0" {
			if owner, exists := serviceKeys[wildcardKey]; exists {
				problems = append(problems, fmt.Sprintf("%s conflicts with wildcard inbound %s", prefix, owner))
			}
		} else {
			suffix := fmt.Sprintf(":%d", inbound.ListenPort)
			for existingKey, owner := range serviceKeys {
				if existingKey != key && strings.HasSuffix(existingKey, suffix) {
					problems = append(problems, fmt.Sprintf("%s conflicts with inbound %s", prefix, owner))
				}
			}
		}
		serviceKeys[key] = inbound.ID
	}

	outboundIDs := map[string]bool{}
	for i, outbound := range c.Outbounds {
		prefix := fmt.Sprintf("outbounds[%d]", i)
		if strings.TrimSpace(outbound.ID) == "" {
			problems = append(problems, prefix+".id is required")
		} else if outboundIDs[outbound.ID] {
			problems = append(problems, prefix+".id is duplicated")
		}
		outboundIDs[outbound.ID] = true

		if strings.TrimSpace(outbound.Name) == "" {
			problems = append(problems, prefix+".name is required")
		}
		addr, err := netip.ParseAddr(outbound.Address)
		if err != nil || !addr.Is4() {
			problems = append(problems, prefix+".address must be an IPv4 address")
		}
		if outbound.Port == 0 {
			problems = append(problems, prefix+".port is required")
		}
		validateHealthCheck(outbound.HealthCheck, prefix+".health_check", &problems)
	}

	bindingIDs := map[string]bool{}
	type modeOwner struct {
		index int
		mode  BindingMode
	}
	modeByInbound := map[string]modeOwner{}
	defaultBindingByInbound := map[string]int{}
	for i, binding := range c.Bindings {
		prefix := fmt.Sprintf("bindings[%d]", i)
		if strings.TrimSpace(binding.ID) == "" {
			problems = append(problems, prefix+".id is required")
		} else if bindingIDs[binding.ID] {
			problems = append(problems, prefix+".id is duplicated")
		}
		bindingIDs[binding.ID] = true

		_, inboundExists := inboundByID[binding.InboundID]
		if !inboundExists {
			problems = append(problems, prefix+".inbound_id must reference an existing inbound")
		}
		if binding.AffinitySecs < 0 || binding.AffinitySecs > 86400 {
			problems = append(problems, prefix+".affinity_seconds must be between 0 and 86400")
		}

		if binding.InboundID != "" {
			// One listening socket only ever runs one engine (raw TCP
			// passthrough or terminated HTTP) — every binding attached to
			// it must agree on which, since mode now lives on the binding,
			// not the inbound it points at.
			if first, ok := modeByInbound[binding.InboundID]; !ok {
				modeByInbound[binding.InboundID] = modeOwner{index: i, mode: binding.Mode}
			} else if first.mode != binding.Mode {
				problems = append(problems, fmt.Sprintf("%s.mode conflicts with bindings[%d].mode — all bindings for inbound %s must share one mode", prefix, first.index, binding.InboundID))
			}
		}

		if len(binding.Groups) == 0 {
			// An empty group list matches everything — this binding is
			// InboundID's *default*, always evaluated last regardless of
			// where it sits in the list. Only one makes sense per inbound:
			// a second one could never be reached.
			if binding.InboundID != "" {
				if first, ok := defaultBindingByInbound[binding.InboundID]; !ok {
					defaultBindingByInbound[binding.InboundID] = i
				} else {
					problems = append(problems, fmt.Sprintf("%s is a second default (empty-rule) binding for inbound %s — bindings[%d] is already its default", prefix, binding.InboundID, first))
				}
			}
		}

		for g, group := range binding.Groups {
			if len(group.Conditions) == 0 {
				problems = append(problems, fmt.Sprintf("%s.groups[%d] must have at least one condition", prefix, g))
			}
			for cidx, condition := range group.Conditions {
				condPrefix := fmt.Sprintf("%s.groups[%d].conditions[%d]", prefix, g, cidx)
				if strings.TrimSpace(condition.Value) == "" {
					problems = append(problems, condPrefix+".value is required")
				}
				if condition.Field == MatchFieldPath && binding.Mode != BindingModeHTTP {
					problems = append(problems, fmt.Sprintf("%s matches path but binding %s is not in http mode", condPrefix, prefix))
				}
			}
		}

		if binding.Action == BindingActionDrop {
			if len(binding.Groups) > 0 {
				problems = append(problems, prefix+".action is drop but this binding has match conditions — drop only makes sense on the default (empty-rule) binding, for traffic that matched nothing else")
			}
			if len(binding.Targets) > 0 {
				problems = append(problems, prefix+".targets must be empty when action is drop")
			}
		} else if len(binding.Targets) == 0 {
			problems = append(problems, prefix+".targets must have at least one outbound")
		}
		targetIDs := map[string]bool{}
		weightTotal := 0
		for t, target := range binding.Targets {
			targetPrefix := fmt.Sprintf("%s.targets[%d]", prefix, t)
			if !outboundIDs[target.OutboundID] {
				problems = append(problems, targetPrefix+".outbound_id must reference an existing outbound")
			}
			if targetIDs[target.OutboundID] {
				problems = append(problems, targetPrefix+" duplicates another target in this binding")
			}
			targetIDs[target.OutboundID] = true
			if binding.SelectionStrategy == SelectionManual {
				if target.WeightPercent < 1 || target.WeightPercent > 100 {
					problems = append(problems, targetPrefix+".weight_percent must be between 1 and 100")
				}
				weightTotal += target.WeightPercent
			}
		}
		if binding.SelectionStrategy == SelectionManual && len(binding.Targets) > 0 && weightTotal != 100 {
			problems = append(problems, prefix+".targets weight_percent values must add up to 100")
		}
	}

	if len(problems) > 0 {
		sort.Strings(problems)
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func DefaultProfileConfig() ProfileConfig {
	return ProfileConfig{
		SchemaVersion: SchemaVersion,
		Inbounds:      []Inbound{},
		Outbounds:     []Outbound{},
		Bindings:      []Binding{},
	}
}
