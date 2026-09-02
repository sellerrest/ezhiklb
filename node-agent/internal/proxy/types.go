// Package proxy is the node-agent's L7 traffic plane: it enforces the
// Binding match rules (SNI for TCP inbounds, SNI+URI-path for HTTP
// inbounds) that IPVS structurally cannot express, since IPVS only ever
// sees destination IP:port. UDP inbounds are not handled here — they still
// go through IPVS as one flat weighted pool (reconciler.go), since UDP
// carries no SNI/Host signal to route by at this layer.
package proxy

import (
	"fmt"
	"time"

	"ezhiklb-node-agent/internal/domain"
)

// ResolvedTarget is an Outbound reachable from one Binding, with its
// manual-strategy weight already resolved.
type ResolvedTarget struct {
	OutboundID    string
	Address       string
	Port          uint16
	WeightPercent int
}

// endpoint is the HealthSource lookup key — address alone is not unique,
// since two outbounds can share a host on different ports.
func (t ResolvedTarget) endpoint() string { return fmt.Sprintf("%s:%d", t.Address, t.Port) }

// OutboundStat is what the panel's node/outbound status views need per
// outbound: how many connections and distinct client IPs are live right
// now, and cumulative bytes copied in each direction since the proxy
// started (the panel derives a Мбит/с rate by diffing consecutive polls).
type OutboundStat struct {
	ActiveConnections int64 `json:"active_connections"`
	ActiveIPs         int   `json:"active_ips"`
	BytesRx           int64 `json:"bytes_rx"`
	BytesTx           int64 `json:"bytes_tx"`
}

// compiledBinding is a Binding with its InboundID already used for grouping
// (by the Manager) and its targets pre-resolved against the config's
// Outbound list, so the hot path never has to re-walk the whole config per
// connection.
type compiledBinding struct {
	enabled  bool
	groups   []domain.MatchGroup
	strategy domain.SelectionStrategy
	targets  []ResolvedTarget
}

// HealthSource lets the proxy skip outbounds the health monitor has already
// marked down, and pick the lowest-latency one for the "ping" strategy — it
// is satisfied by *agent.HealthMonitor without proxy importing agent
// (agent imports proxy, not the other way, to avoid an import cycle). Both
// methods key on the outbound's "address:port" endpoint, not address alone
// — two outbounds can share a host on different ports, and the health
// check itself is now a TCP dial to that exact endpoint (see health.go).
// Unset/never-checked endpoints must be treated as reachable (fail open):
// this is what lets a freshly published binding forward traffic before its
// first health probe has even run.
type HealthSource interface {
	IsReachable(endpoint string) bool
	Latency(endpoint string) (time.Duration, bool)
}

type noopHealthSource struct{}

func (noopHealthSource) IsReachable(string) bool              { return true }
func (noopHealthSource) Latency(string) (time.Duration, bool) { return 0, false }
