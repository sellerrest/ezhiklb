package agent

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"ezhiklb-node-agent/internal/domain"
)

const (
	ReachabilityUnknown = "unknown"
	ReachabilityUp      = "reachable"
	ReachabilityDown    = "unreachable"
)

// schedulerTick governs how often HealthMonitor looks for outbounds that
// are due a probe — each outbound is only actually pinged once its own
// HealthCheck.IntervalSeconds has elapsed, so this just needs to be short
// relative to the shortest interval anyone configures (1s, matching the
// interval field's own 1-3600s validated range).
const schedulerTick = time.Second

// HealthMonitor checks every enabled Outbound on its own interval/timeout/
// thresholds (each Outbound carries its own HealthCheck now, there is no
// single core-wide policy any more) with a plain TCP dial to its exact
// address:port — not an ICMP ping, deliberately: a host can drop ICMP
// entirely while the actual service on that port is fine, or answer ICMP
// while the service itself is down, so this checks the thing that actually
// matters. It both zeroes/restores its IPVS destination weight (via
// applyOutboundState, for UDP-routed outbounds) and exposes IsReachable/
// Latency so internal/proxy can skip down outbounds and pick the fastest
// one for "ping" bindings — one shared reachability picture for both
// traffic engines.
type HealthMonitor struct {
	reconciler *Reconciler
	logger     *slog.Logger

	mu      sync.RWMutex
	results map[string]domain.BackendHealth // keyed by "address:port" endpoint, not address alone
}

func NewHealthMonitor(reconciler *Reconciler, logger *slog.Logger) *HealthMonitor {
	if logger == nil {
		logger = slog.Default()
	}
	return &HealthMonitor{reconciler: reconciler, logger: logger, results: map[string]domain.BackendHealth{}}
}

// Run ticks until ctx is cancelled, probing each outbound reported by
// outbounds() once its own interval has elapsed.
func (m *HealthMonitor) Run(ctx context.Context, outbounds func() []domain.Outbound) {
	ticker := time.NewTicker(schedulerTick)
	defer ticker.Stop()
	lastChecked := map[string]time.Time{}
	m.logger.Info("health monitor started")
	defer m.logger.Info("health monitor stopped")
	m.checkDue(ctx, outbounds(), lastChecked)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.checkDue(ctx, outbounds(), lastChecked)
		}
	}
}

func (m *HealthMonitor) Results() []domain.BackendHealth {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]domain.BackendHealth, 0, len(m.results))
	for _, item := range m.results {
		result = append(result, item)
	}
	return result
}

func (m *HealthMonitor) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.results = map[string]domain.BackendHealth{}
}

// CheckNow probes every enabled outbound immediately, ignoring each one's
// own interval — used by POST /v1/health-probe for an on-demand check.
func (m *HealthMonitor) CheckNow(ctx context.Context, outbounds []domain.Outbound) {
	for _, outbound := range outbounds {
		if outbound.Enabled && outbound.HealthCheck.Enabled {
			m.checkOne(ctx, outbound)
		}
	}
}

// IsReachable satisfies proxy.HealthSource, keyed by "address:port". An
// endpoint with no result yet (never checked, or checked but still within
// its recovery/failure threshold) is treated as reachable — fail open, so
// a freshly published binding forwards traffic immediately rather than
// waiting on a probe.
func (m *HealthMonitor) IsReachable(endpoint string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result, ok := m.results[endpoint]
	return !ok || result.State != ReachabilityDown
}

// Latency satisfies proxy.HealthSource for the "ping" selection strategy,
// reusing the RTT already measured by the ordinary TCP health-check dial.
func (m *HealthMonitor) Latency(endpoint string) (time.Duration, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result, ok := m.results[endpoint]
	if !ok || result.State != ReachabilityUp {
		return 0, false
	}
	return time.Duration(result.LatencyMillis) * time.Millisecond, true
}

func (m *HealthMonitor) checkDue(ctx context.Context, outbounds []domain.Outbound, lastChecked map[string]time.Time) {
	now := time.Now()
	for _, outbound := range outbounds {
		if !outbound.Enabled || !outbound.HealthCheck.Enabled {
			continue
		}
		interval := time.Duration(max(1, outbound.HealthCheck.IntervalSeconds)) * time.Second
		if due, checked := lastChecked[outbound.ID]; checked && now.Sub(due) < interval {
			continue
		}
		lastChecked[outbound.ID] = now
		m.checkOne(ctx, outbound)
	}
}

// checkOne dials the outbound's exact address:port over TCP — a real
// connect probe (SYN/SYN-ACK/ACK, then closed) of the service that will
// actually receive traffic, not an ICMP ping to the host. No raw socket or
// root privilege is needed for this, unlike ICMP.
func (m *HealthMonitor) checkOne(ctx context.Context, outbound domain.Outbound) {
	config := outbound.HealthCheck
	endpoint := fmt.Sprintf("%s:%d", outbound.Address, outbound.Port)
	started := time.Now()
	dialCtx, cancel := context.WithTimeout(ctx, time.Duration(config.TimeoutMillis)*time.Millisecond)
	conn, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", endpoint)
	cancel()
	if conn != nil {
		_ = conn.Close()
	}

	m.mu.Lock()
	result := m.results[endpoint]
	result.Address = endpoint
	result.CheckedAt = time.Now().UTC()
	previous := result.State
	if err == nil {
		result.ConsecutiveUp++
		result.ConsecutiveDown = 0
		result.LatencyMillis = time.Since(started).Milliseconds()
		if result.State != ReachabilityUp && result.ConsecutiveUp >= config.RecoveryThreshold {
			result.State = ReachabilityUp
		}
	} else {
		result.ConsecutiveDown++
		result.ConsecutiveUp = 0
		result.LatencyMillis = 0
		if result.ConsecutiveDown >= config.FailureThreshold {
			result.State = ReachabilityDown
		}
	}
	m.results[endpoint] = result
	m.mu.Unlock()

	if result.State != previous && result.State != ReachabilityUnknown {
		m.applyOutboundState(ctx, outbound, result.State)
		m.logger.Info("backend reachability changed", "endpoint", endpoint, "state", result.State)
	}
}

// applyOutboundState zeroes/restores this outbound's weight in every IPVS
// destination that currently references it (UDP-routed outbounds only —
// the TCP proxy doesn't need this push, it reads IsReachable live on every
// connection instead).
func (m *HealthMonitor) applyOutboundState(ctx context.Context, outbound domain.Outbound, state string) {
	m.reconciler.mu.Lock()
	defer m.reconciler.mu.Unlock()
	services, err := m.reconciler.loadState()
	if err != nil {
		return
	}
	for _, service := range services.Services {
		for _, destination := range service.Destinations {
			if destination.Address != outbound.Address || destination.Port != outbound.Port {
				continue
			}
			weight := destination.Weight
			if state == ReachabilityDown {
				weight = 0
			}
			weightCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			applyErr := m.reconciler.setDestinationWeight(weightCtx, service, destination, weight)
			cancel()
			if applyErr != nil {
				m.logger.Error("update health weight", "service", serviceKey(service), "backend", destinationKey(destination), "error", applyErr)
			}
		}
	}
}
