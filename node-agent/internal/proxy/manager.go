package proxy

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"

	"ezhiklb-node-agent/internal/domain"
)

type runningRouter interface {
	updateBindings([]compiledBinding)
	stats() map[string]OutboundStat
	clientIPs() map[string]struct{}
	close() error
}

type running struct {
	router runningRouter
	cancel context.CancelFunc
	addr   string
	mode   domain.BindingMode
}

// Manager owns every TCP/HTTP listener the proxy currently runs, one per
// enabled TCP-listening Inbound, and reconciles them against a
// domain.ProfileConfig the same way reconciler.go reconciles IPVS: diff
// against what is already running, stop what is no longer wanted, start
// what is new, and always refresh the routing table of everything that
// stays — so publishing a core never has to tear down unaffected listeners.
type Manager struct {
	logger *slog.Logger
	ctx    context.Context

	mu      sync.Mutex
	health  HealthSource
	routers map[string]*running // keyed by inbound ID
}

// NewManager's ctx bounds every listener's lifetime — cancelling it (e.g. on
// process shutdown) stops all of them. SetHealth should be called once,
// before the first Apply, so already-running routers don't keep pointing at
// a stale (or the no-op) health source.
func NewManager(ctx context.Context, logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	return &Manager{logger: logger, ctx: ctx, health: noopHealthSource{}, routers: map[string]*running{}}
}

func (m *Manager) SetHealth(h HealthSource) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if h == nil {
		h = noopHealthSource{}
	}
	m.health = h
}

// Apply reconciles running listeners against cfg. Only inbounds with
// TCP == true and Enabled == true are handled here; UDP forwarding for the
// same or other inbounds is the reconciler's IPVS path, applied separately.
func (m *Manager) Apply(cfg domain.ProfileConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	outboundByID := map[string]domain.Outbound{}
	for _, outbound := range cfg.Outbounds {
		outboundByID[outbound.ID] = outbound
	}

	bindingsByInbound := map[string][]compiledBinding{}
	modeByInbound := map[string]domain.BindingMode{}
	for _, binding := range cfg.Bindings {
		targets := make([]ResolvedTarget, 0, len(binding.Targets))
		for _, target := range binding.Targets {
			outbound, ok := outboundByID[target.OutboundID]
			if !ok || !outbound.Enabled {
				continue
			}
			targets = append(targets, ResolvedTarget{OutboundID: outbound.ID, Address: outbound.Address, Port: outbound.Port, WeightPercent: target.WeightPercent})
		}
		// A drop binding legitimately has no targets — that's not a reason
		// to skip it the way an under-resolved forward binding would be.
		if binding.Action != domain.BindingActionDrop && len(targets) == 0 {
			continue
		}
		if _, ok := modeByInbound[binding.InboundID]; !ok {
			modeByInbound[binding.InboundID] = binding.Mode
		}
		bindingsByInbound[binding.InboundID] = append(bindingsByInbound[binding.InboundID], compiledBinding{
			enabled: binding.Enabled, groups: binding.Groups, strategy: binding.SelectionStrategy, targets: targets, action: binding.Action,
		})
	}
	// Guarantee the (at most one) default — empty-groups — binding for each
	// inbound is always evaluated last, regardless of where it happened to
	// sit in cfg.Bindings: domain.ProfileConfig.Validate enforces there's
	// only one, but nothing stops it from being saved first in the list,
	// which would silently make every rule after it unreachable.
	for id, bindings := range bindingsByInbound {
		sort.SliceStable(bindings, func(i, j int) bool { return len(bindings[i].groups) > 0 && len(bindings[j].groups) == 0 })
		bindingsByInbound[id] = bindings
	}

	desired := map[string]domain.Inbound{}
	for _, inbound := range cfg.Inbounds {
		if inbound.Enabled && inbound.TCP {
			desired[inbound.ID] = inbound
		}
	}

	// Mode is no longer stored on Inbound — it's derived from whatever its
	// Bindings declare (domain.ProfileConfig.Validate guarantees they all
	// agree). An inbound with no bindings yet has nothing to derive a mode
	// from; TCP passthrough is the sensible default until a Binding for it
	// actually exists.
	modeOf := func(id string) domain.BindingMode {
		if mode, ok := modeByInbound[id]; ok {
			return mode
		}
		return domain.BindingModeTCP
	}

	for id, current := range m.routers {
		inbound, stillWanted := desired[id]
		if !stillWanted || modeOf(id) != current.mode || addrOf(inbound) != current.addr {
			current.cancel()
			_ = current.router.close()
			delete(m.routers, id)
		}
	}

	var errs []string
	for id, inbound := range desired {
		bindings := bindingsByInbound[id]
		mode := modeOf(id)
		addr := addrOf(inbound)
		current, exists := m.routers[id]
		if !exists {
			routerCtx, cancel := context.WithCancel(m.ctx)
			var router runningRouter
			var startErr error
			if mode == domain.BindingModeHTTP {
				httpR := newHTTPRouter(m.logger, m.health)
				startErr = httpR.start(routerCtx, addr)
				router = httpR
			} else {
				tcpR := newTCPRouter(m.logger, m.health)
				startErr = tcpR.start(routerCtx, addr)
				router = tcpR
			}
			if startErr != nil {
				cancel()
				errs = append(errs, fmt.Sprintf("inbound %s: %v", inbound.Name, startErr))
				continue
			}
			current = &running{router: router, cancel: cancel, addr: addr, mode: mode}
			m.routers[id] = current
			m.logger.Info("l7 proxy listener started", "inbound", inbound.Name, "address", addr, "mode", mode)
		}
		current.router.updateBindings(bindings)
	}

	if len(errs) > 0 {
		return fmt.Errorf("l7 proxy: %s", strings.Join(errs, "; "))
	}
	return nil
}

// Stats sums live connection/IP counts per outbound ID across every running
// router — the same outbound can be a target of bindings on more than one
// inbound, each with its own router and its own counters.
func (m *Manager) Stats() map[string]OutboundStat {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := map[string]OutboundStat{}
	for _, current := range m.routers {
		for outboundID, stat := range current.router.stats() {
			existing := result[outboundID]
			existing.ActiveConnections += stat.ActiveConnections
			existing.ActiveIPs += stat.ActiveIPs
			existing.BytesRx += stat.BytesRx
			existing.BytesTx += stat.BytesTx
			result[outboundID] = existing
		}
	}
	return result
}

// ActiveClientIPs unions the distinct client IPs with at least one live
// connection through any L7 (SNI/HTTP-routed) listener right now. IPVS never
// sees this traffic — it's a separate data plane — so the node-wide
// "active IPs" metric (MetricsCollector, scanning /proc/net/ip_vs_conn)
// folds this in too, or a node running only SNI/HTTP-routed Ядра would
// always read 0 online no matter how many real clients are connected.
func (m *Manager) ActiveClientIPs() map[string]struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make(map[string]struct{})
	for _, current := range m.routers {
		for ip := range current.router.clientIPs() {
			result[ip] = struct{}{}
		}
	}
	return result
}

// Stop tears down every listener the Manager owns — used on decommission.
func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, current := range m.routers {
		current.cancel()
		_ = current.router.close()
		delete(m.routers, id)
	}
}

func addrOf(inbound domain.Inbound) string {
	address := inbound.ListenAddress
	if address == "" {
		address = "0.0.0.0"
	}
	return fmt.Sprintf("%s:%d", address, inbound.ListenPort)
}
