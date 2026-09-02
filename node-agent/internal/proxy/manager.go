package proxy

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"ezhiklb-node-agent/internal/domain"
)

type runningRouter interface {
	updateBindings([]compiledBinding)
	stats() map[string]OutboundStat
	close() error
}

type running struct {
	router runningRouter
	cancel context.CancelFunc
	addr   string
	mode   domain.InboundMode
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
	for _, binding := range cfg.Bindings {
		targets := make([]ResolvedTarget, 0, len(binding.Targets))
		for _, target := range binding.Targets {
			outbound, ok := outboundByID[target.OutboundID]
			if !ok || !outbound.Enabled {
				continue
			}
			targets = append(targets, ResolvedTarget{OutboundID: outbound.ID, Address: outbound.Address, Port: outbound.Port, WeightPercent: target.WeightPercent})
		}
		if len(targets) == 0 {
			continue
		}
		bindingsByInbound[binding.InboundID] = append(bindingsByInbound[binding.InboundID], compiledBinding{
			enabled: binding.Enabled, groups: binding.Groups, strategy: binding.SelectionStrategy, targets: targets,
		})
	}

	desired := map[string]domain.Inbound{}
	for _, inbound := range cfg.Inbounds {
		if inbound.Enabled && inbound.TCP {
			desired[inbound.ID] = inbound
		}
	}

	for id, current := range m.routers {
		inbound, stillWanted := desired[id]
		if !stillWanted || inbound.Mode != current.mode || addrOf(inbound) != current.addr {
			current.cancel()
			_ = current.router.close()
			delete(m.routers, id)
		}
	}

	var errs []string
	for id, inbound := range desired {
		bindings := bindingsByInbound[id]
		addr := addrOf(inbound)
		current, exists := m.routers[id]
		if !exists {
			routerCtx, cancel := context.WithCancel(m.ctx)
			var router runningRouter
			var startErr error
			if inbound.Mode == domain.InboundModeHTTP {
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
			current = &running{router: router, cancel: cancel, addr: addr, mode: inbound.Mode}
			m.routers[id] = current
			m.logger.Info("l7 proxy listener started", "inbound", inbound.Name, "address", addr, "mode", inbound.Mode)
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
