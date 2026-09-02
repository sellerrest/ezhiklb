package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"ezhiklb-node-agent/internal/domain"
	"ezhiklb-node-agent/internal/proxy"
)

type Runner interface {
	Run(context.Context, string, []string, string) (string, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args []string, input string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if input != "" {
		cmd.Stdin = strings.NewReader(input)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return stdout.String(), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

type Service struct {
	Protocol     domain.Protocol `json:"protocol"`
	Address      string          `json:"address"`
	Port         uint16          `json:"port"`
	Scheduler    string          `json:"scheduler"`
	AffinitySecs int             `json:"affinity_seconds"`
	Destinations []Destination   `json:"destinations"`
}

type Destination struct {
	ID      string `json:"id"`
	Address string `json:"address"`
	Port    uint16 `json:"port"`
	Weight  int    `json:"weight"`
}

type AppliedState struct {
	Revision int64                `json:"revision"`
	Services []Service            `json:"services"`
	Config   domain.ProfileConfig `json:"config"`
}

type Reconciler struct {
	runner       Runner
	statePath    string
	logger       *slog.Logger
	proxyManager *proxy.Manager
	mu           sync.Mutex
}

// NewReconciler's ctx bounds the lifetime of every TCP/HTTP proxy listener
// the reconciler starts (internal/proxy.Manager) — it must outlive any
// single Reconcile/Restore call, since a listener started by one Reconcile
// call keeps serving traffic long after that call returns.
func NewReconciler(ctx context.Context, runner Runner, statePath string, logger *slog.Logger) *Reconciler {
	if runner == nil {
		runner = ExecRunner{}
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Reconciler{runner: runner, statePath: statePath, logger: logger, proxyManager: proxy.NewManager(ctx, logger)}
}

// AttachHealthMonitor lets the L7 proxy skip unreachable outbounds and pick
// the lowest-latency one for "ping" bindings, reusing the same TCP
// health-check results IPVS destinations already rely on. Call this once,
// right after both the Reconciler and the HealthMonitor exist and before
// the first Reconcile/Restore — routers started before this call keep
// whatever health source was set at the time they were created.
func (r *Reconciler) AttachHealthMonitor(monitor *HealthMonitor) {
	r.proxyManager.SetHealth(monitor)
}

func (r *Reconciler) Services() []Service {
	r.mu.Lock()
	defer r.mu.Unlock()
	state, err := r.loadState()
	if err != nil {
		return nil
	}
	return append([]Service(nil), state.Services...)
}

func (r *Reconciler) Reconcile(ctx context.Context, desired domain.NodeDesiredState) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := desired.Config.Validate(); err != nil {
		return fmt.Errorf("invalid desired revision: %w", err)
	}
	vip, err := r.resolveIngress(ctx, desired.IngressAddress)
	if err != nil {
		return err
	}
	services := compileServices(desired.Config, vip)
	if err := r.validateLocalAddresses(ctx, services); err != nil {
		return err
	}
	if err := r.validatePortAvailability(ctx, services); err != nil {
		return err
	}
	old, err := r.loadState()
	if err != nil {
		return err
	}
	if err := r.configureKernel(ctx); err != nil {
		return err
	}
	if err := r.warmRoutes(ctx, services); err != nil {
		r.logger.Warn("route warm-up was incomplete", "error", err)
	}
	transitionServices := unionServices(old.Services, services)
	if err := r.applyFirewall(ctx, transitionServices); err != nil {
		return fmt.Errorf("apply firewall: %w", err)
	}
	if desired.ResetConnections {
		r.logger.Warn("resetting EzhikLB connection state for published profile", "revision", desired.Revision)
		if err := r.resetConnectionState(ctx, old.Services, services); err != nil {
			_ = r.applyFirewall(ctx, old.Services)
			return fmt.Errorf("reset IPVS connection state: %w", err)
		}
	} else if err := r.applyIPVS(ctx, old.Services, services); err != nil {
		rollbackErr := r.applyIPVS(ctx, services, old.Services)
		_ = r.applyFirewall(ctx, old.Services)
		if rollbackErr != nil {
			return fmt.Errorf("apply IPVS: %v; rollback also failed: %w", err, rollbackErr)
		}
		return fmt.Errorf("apply IPVS: %w (previous state restored)", err)
	}
	if err := r.applyFirewall(ctx, services); err != nil {
		return fmt.Errorf("finalize firewall: %w", err)
	}

	// The TCP/HTTP proxy (internal/proxy) enforces the SNI/path Binding
	// rules IPVS cannot express; it owns its own listeners independently of
	// ipvsadm/iptables, so a failure here rolls back only the proxy's own
	// routing tables, not the UDP/IPVS state already committed above.
	if err := r.proxyManager.Apply(desired.Config); err != nil {
		if rollbackErr := r.proxyManager.Apply(old.Config); rollbackErr != nil {
			return fmt.Errorf("apply l7 proxy: %v; rollback also failed: %w", err, rollbackErr)
		}
		return fmt.Errorf("apply l7 proxy: %w (previous routing restored)", err)
	}

	return r.saveState(AppliedState{Revision: desired.Revision, Services: services, Config: desired.Config})
}

// resetConnectionState deliberately interrupts only flows owned by EzhikLB.
// It never clears the host-wide IPVS or conntrack tables.
func (r *Reconciler) resetConnectionState(ctx context.Context, oldServices, desiredServices []Service) error {
	for _, service := range oldServices {
		for _, destination := range service.Destinations {
			if err := r.setDestinationWeight(ctx, service, destination, 0); err != nil {
				r.logger.Warn("quiesce destination before reset", "service", virtualAddress(service), "backend", realAddress(destination), "error", err)
			}
		}
	}
	if err := r.applyIPVS(ctx, oldServices, nil); err != nil {
		restoreErr := r.applyIPVS(ctx, oldServices, oldServices)
		if restoreErr != nil {
			return fmt.Errorf("remove managed services: %v; restore also failed: %w", err, restoreErr)
		}
		return fmt.Errorf("remove managed services: %w (previous state restored)", err)
	}
	r.purgeConntrack(ctx, unionServices(oldServices, desiredServices))
	if err := r.applyIPVS(ctx, nil, desiredServices); err != nil {
		for _, service := range desiredServices {
			_ = r.deleteService(ctx, service)
		}
		restoreErr := r.applyIPVS(ctx, oldServices, oldServices)
		if restoreErr != nil {
			return fmt.Errorf("recreate managed services: %v; restore also failed: %w", err, restoreErr)
		}
		return fmt.Errorf("recreate managed services: %w (previous state restored)", err)
	}
	return nil
}

func (r *Reconciler) purgeConntrack(ctx context.Context, services []Service) {
	seen := map[string]bool{}
	for _, service := range services {
		key := serviceKey(service)
		if seen[key] {
			continue
		}
		seen[key] = true
		args := []string{"-D", "-f", "ipv4", "-p", string(service.Protocol), "-d", service.Address, "--dport", strconv.Itoa(int(service.Port))}
		if _, err := r.runner.Run(ctx, "conntrack", args, ""); err != nil {
			// conntrack exits non-zero when no matching entries exist. The IPVS
			// service reset is authoritative, so an empty or unavailable table
			// must not make the whole profile publication fail.
			r.logger.Warn("scoped conntrack cleanup was incomplete", "service", virtualAddress(service), "protocol", service.Protocol, "error", err)
		}
	}
}

func (r *Reconciler) validatePortAvailability(ctx context.Context, services []Service) error {
	output, err := r.runner.Run(ctx, "ss", []string{"-H", "-lntup"}, "")
	if err != nil {
		return fmt.Errorf("inspect occupied ports: %w", err)
	}
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		protocol := strings.ToLower(fields[0])
		endpoint := fields[4]
		for _, service := range services {
			if protocol == string(service.Protocol) && strings.HasSuffix(endpoint, ":"+strconv.Itoa(int(service.Port))) {
				return fmt.Errorf("port conflict: %s %s:%d is occupied by %s", strings.ToUpper(protocol), service.Address, service.Port, strings.TrimSpace(line))
			}
		}
	}
	return nil
}

// Restore rebuilds the last committed data-plane state without contacting the
// panel. This keeps forwarding available after a VPS reboot during a control
// plane outage.
func (r *Reconciler) Restore(ctx context.Context) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	state, err := r.loadState()
	if err != nil {
		return 0, err
	}
	if len(state.Services) == 0 {
		return state.Revision, nil
	}
	if err := r.validateLocalAddresses(ctx, state.Services); err != nil {
		return 0, err
	}
	if err := r.configureKernel(ctx); err != nil {
		return 0, err
	}
	if err := r.applyFirewall(ctx, state.Services); err != nil {
		return 0, fmt.Errorf("restore firewall: %w", err)
	}
	for _, service := range state.Services {
		if err := r.ensureService(ctx, service, true); err != nil {
			return 0, fmt.Errorf("restore IPVS service: %w", err)
		}
		for _, destination := range service.Destinations {
			if err := r.ensureDestination(ctx, service, destination, true); err != nil {
				return 0, fmt.Errorf("restore IPVS destination: %w", err)
			}
		}
	}
	if err := r.proxyManager.Apply(state.Config); err != nil {
		return 0, fmt.Errorf("restore l7 proxy: %w", err)
	}
	return state.Revision, nil
}

// RestoredConfig lets the caller recover per-outbound health-check settings
// after a restart, before the panel has reconnected — SeedRestoredState/
// StartHealthMonitor use this instead of a global HealthCheck field, since
// health-check settings now live per-Outbound.
func (r *Reconciler) RestoredConfig() domain.ProfileConfig {
	r.mu.Lock()
	defer r.mu.Unlock()
	state, err := r.loadState()
	if err != nil {
		return domain.DefaultProfileConfig()
	}
	return state.Config
}

// Outbounds returns the outbounds from the last successfully applied
// config — the live source HealthMonitor polls, so its per-outbound
// interval/timeout/thresholds always reflect the most recent publish.
func (r *Reconciler) Outbounds() []domain.Outbound {
	r.mu.Lock()
	defer r.mu.Unlock()
	state, err := r.loadState()
	if err != nil {
		return nil
	}
	return append([]domain.Outbound(nil), state.Config.Outbounds...)
}

// OutboundStat is the wire shape GET /v1/state reports per outbound —
// what the panel's "Исходящие" status page needs (live connection/IP
// counts from the L7 proxy; the reachability itself comes from the
// "health" field, keyed the same "address:port" way).
type OutboundStat struct {
	OutboundID        string `json:"outbound_id"`
	ActiveConnections int64  `json:"active_connections"`
	ActiveIPs         int    `json:"active_ips"`
	BytesRx           int64  `json:"bytes_rx"`
	BytesTx           int64  `json:"bytes_tx"`
}

// ProxyStats reports live per-outbound connection/IP counts and cumulative
// byte counters from the L7 proxy (TCP/HTTP-routed outbounds only — UDP/
// IPVS destinations have no per-outbound counters to report here).
func (r *Reconciler) ProxyStats() []OutboundStat {
	stats := r.proxyManager.Stats()
	result := make([]OutboundStat, 0, len(stats))
	for outboundID, stat := range stats {
		result = append(result, OutboundStat{
			OutboundID: outboundID, ActiveConnections: stat.ActiveConnections, ActiveIPs: stat.ActiveIPs,
			BytesRx: stat.BytesRx, BytesTx: stat.BytesTx,
		})
	}
	return result
}

// Decommission removes only services and firewall rules managed by EzhikLB.
// Unrelated IPVS services and host firewall rules remain untouched.
func (r *Reconciler) Decommission(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	old, err := r.loadState()
	if err != nil {
		return err
	}
	if err := r.applyIPVS(ctx, old.Services, nil); err != nil {
		return fmt.Errorf("remove IPVS services: %w", err)
	}
	if err := r.removeFirewall(ctx); err != nil {
		return fmt.Errorf("remove EzhikLB firewall rules: %w", err)
	}
	r.proxyManager.Stop()
	return r.saveState(AppliedState{})
}

func (r *Reconciler) removeFirewall(ctx context.Context) error {
	for _, rule := range []struct{ table, parent, child string }{{"filter", "FORWARD", "EZHIKLB-FORWARD"}, {"nat", "POSTROUTING", "EZHIKLB-SNAT"}} {
		prefix := []string{"-w", "5"}
		if rule.table != "filter" {
			prefix = append(prefix, "-t", rule.table)
		}
		check := append(append([]string{}, prefix...), "-C", rule.parent, "-j", rule.child)
		if _, err := r.runner.Run(ctx, "iptables", check, ""); err == nil {
			remove := append(append([]string{}, prefix...), "-D", rule.parent, "-j", rule.child)
			if _, err := r.runner.Run(ctx, "iptables", remove, ""); err != nil {
				return err
			}
		}
		flush := append(append([]string{}, prefix...), "-F", rule.child)
		_, _ = r.runner.Run(ctx, "iptables", flush, "")
		deleteChain := append(append([]string{}, prefix...), "-X", rule.child)
		_, _ = r.runner.Run(ctx, "iptables", deleteChain, "")
	}
	return nil
}

func (r *Reconciler) validateLocalAddresses(ctx context.Context, services []Service) error {
	output, err := r.runner.Run(ctx, "ip", []string{"-o", "-4", "addr", "show"}, "")
	if err != nil {
		return err
	}
	local := map[string]bool{}
	for _, field := range strings.Fields(output) {
		if prefix, parseErr := netip.ParsePrefix(field); parseErr == nil && prefix.Addr().Is4() {
			local[prefix.Addr().String()] = true
		}
	}
	for _, service := range services {
		if !local[service.Address] {
			return fmt.Errorf("listen address %s is not assigned to this node", service.Address)
		}
	}
	return nil
}

func unionServices(first, second []Service) []Service {
	services := map[string]Service{}
	for _, source := range append(append([]Service(nil), first...), second...) {
		key := serviceKey(source)
		target, exists := services[key]
		if !exists {
			target = source
			target.Destinations = nil
		}
		destinations := map[string]Destination{}
		for _, destination := range target.Destinations {
			destinations[destinationKey(destination)] = destination
		}
		for _, destination := range source.Destinations {
			destinations[destinationKey(destination)] = destination
		}
		target.Destinations = target.Destinations[:0]
		for _, destination := range destinations {
			target.Destinations = append(target.Destinations, destination)
		}
		sort.Slice(target.Destinations, func(i, j int) bool {
			return destinationKey(target.Destinations[i]) < destinationKey(target.Destinations[j])
		})
		services[key] = target
	}
	result := make([]Service, 0, len(services))
	for _, service := range services {
		result = append(result, service)
	}
	sort.Slice(result, func(i, j int) bool { return serviceKey(result[i]) < serviceKey(result[j]) })
	return result
}

// compileServices builds the IPVS-facing service list. Only UDP is handled
// here now: UDP carries no SNI/Host signal to route by, so it stays on
// IPVS as one flat weighted pool per inbound, exactly as before. TCP is
// handled entirely by internal/proxy (Reconcile calls proxyManager.Apply
// separately), which is the only engine that can actually see SNI/path.
func compileServices(config domain.ProfileConfig, vip string) []Service {
	outboundByID := map[string]domain.Outbound{}
	for _, outbound := range config.Outbounds {
		outboundByID[outbound.ID] = outbound
	}
	bindingsByInbound := map[string][]domain.Binding{}
	for _, binding := range config.Bindings {
		if binding.Enabled {
			bindingsByInbound[binding.InboundID] = append(bindingsByInbound[binding.InboundID], binding)
		}
	}

	var result []Service
	for _, inbound := range config.Inbounds {
		if !inbound.Enabled || !inbound.UDP {
			continue
		}
		address := vip
		if inbound.ListenAddress != "" && inbound.ListenAddress != "0.0.0.0" {
			address = inbound.ListenAddress
		}
		service := Service{Protocol: domain.ProtocolUDP, Address: address, Port: inbound.ListenPort, Scheduler: "wrr"}

		bindings := bindingsByInbound[inbound.ID]
		if len(bindings) > 0 {
			// UDP can't carry SNI/Host, so every binding on this inbound
			// feeds the same flat pool; only the first governs scheduler and
			// affinity. A UDP inbound with several differently-configured
			// bindings isn't a coherent config to begin with — none of their
			// match rules can ever apply to UDP traffic.
			primary := bindings[0]
			service.Scheduler = ipvsScheduler(primary.SelectionStrategy)
			service.AffinitySecs = primary.AffinitySecs
			seen := map[string]bool{}
			for _, binding := range bindings {
				for _, target := range binding.Targets {
					outbound, ok := outboundByID[target.OutboundID]
					if !ok || !outbound.Enabled || seen[outbound.ID] {
						continue
					}
					seen[outbound.ID] = true
					weight := 100
					if primary.SelectionStrategy == domain.SelectionManual && target.WeightPercent >= 1 {
						weight = target.WeightPercent
					}
					service.Destinations = append(service.Destinations, Destination{ID: outbound.ID, Address: outbound.Address, Port: outbound.Port, Weight: weight})
				}
			}
		}
		sort.Slice(service.Destinations, func(i, j int) bool {
			return destinationKey(service.Destinations[i]) < destinationKey(service.Destinations[j])
		})
		result = append(result, service)
	}
	sort.Slice(result, func(i, j int) bool { return serviceKey(result[i]) < serviceKey(result[j]) })
	return result
}

// ipvsScheduler maps a Binding's SelectionStrategy onto the closest native
// ipvsadm scheduler, since UDP forwarding stays on IPVS.
func ipvsScheduler(strategy domain.SelectionStrategy) string {
	switch strategy {
	case domain.SelectionLeast:
		return "lc" // least-connection: a real IPVS scheduler, not an approximation
	case domain.SelectionManual:
		return "wrr"
	case domain.SelectionPing:
		// IPVS has no live-RTT scheduler. "sed" (shortest expected delay,
		// weight/(active+1)) is the closest built-in analogue; true
		// ping-based steering is only implemented for TCP (internal/proxy),
		// which reuses the same health-check latency IPVS can't see.
		return "sed"
	default:
		return "wrr"
	}
}

func (r *Reconciler) applyIPVS(ctx context.Context, oldServices, desiredServices []Service) error {
	oldMap, newMap := map[string]Service{}, map[string]Service{}
	for _, service := range oldServices {
		oldMap[serviceKey(service)] = service
	}
	for _, service := range desiredServices {
		newMap[serviceKey(service)] = service
	}
	for key, service := range newMap {
		old, existed := oldMap[key]
		if err := r.ensureService(ctx, service, existed); err != nil {
			return err
		}
		oldDestinations := map[string]Destination{}
		if existed {
			for _, destination := range old.Destinations {
				oldDestinations[destinationKey(destination)] = destination
			}
		}
		newDestinations := map[string]Destination{}
		for _, destination := range service.Destinations {
			newDestinations[destinationKey(destination)] = destination
			if err := r.ensureDestination(ctx, service, destination, oldDestinations[destinationKey(destination)].Address != ""); err != nil {
				return err
			}
		}
		for destinationKey, destination := range oldDestinations {
			if _, keep := newDestinations[destinationKey]; keep {
				continue
			}
			_ = r.setDestinationWeight(ctx, service, destination, 0)
			if err := r.deleteDestination(ctx, service, destination); err != nil {
				return err
			}
		}
	}
	for key, service := range oldMap {
		if _, keep := newMap[key]; keep {
			continue
		}
		if err := r.deleteService(ctx, service); err != nil {
			return err
		}
	}
	return nil
}

func (r *Reconciler) ensureService(ctx context.Context, service Service, existed bool) error {
	action := "-A"
	if existed {
		action = "-E"
	}
	args := []string{action, protocolFlag(service.Protocol), virtualAddress(service), "-s", service.Scheduler}
	if service.AffinitySecs > 0 {
		args = append(args, "-p", strconv.Itoa(service.AffinitySecs))
	}
	_, err := r.runner.Run(ctx, "ipvsadm", args, "")
	if err != nil && existed {
		args[0] = "-A"
		_, err = r.runner.Run(ctx, "ipvsadm", args, "")
	}
	return err
}

func (r *Reconciler) ensureDestination(ctx context.Context, service Service, destination Destination, existed bool) error {
	action := "-a"
	if existed {
		action = "-e"
	}
	args := []string{action, protocolFlag(service.Protocol), virtualAddress(service), "-r", realAddress(destination), "-m", "-w", strconv.Itoa(destination.Weight)}
	_, err := r.runner.Run(ctx, "ipvsadm", args, "")
	if err != nil && existed {
		args[0] = "-a"
		_, err = r.runner.Run(ctx, "ipvsadm", args, "")
	}
	return err
}

func (r *Reconciler) deleteDestination(ctx context.Context, service Service, destination Destination) error {
	_, err := r.runner.Run(ctx, "ipvsadm", []string{"-d", protocolFlag(service.Protocol), virtualAddress(service), "-r", realAddress(destination)}, "")
	return err
}

func (r *Reconciler) deleteService(ctx context.Context, service Service) error {
	_, err := r.runner.Run(ctx, "ipvsadm", []string{"-D", protocolFlag(service.Protocol), virtualAddress(service)}, "")
	return err
}

func (r *Reconciler) setDestinationWeight(ctx context.Context, service Service, destination Destination, weight int) error {
	_, err := r.runner.Run(ctx, "ipvsadm", []string{"-e", protocolFlag(service.Protocol), virtualAddress(service), "-r", realAddress(destination), "-m", "-w", strconv.Itoa(weight)}, "")
	return err
}

// Fixing the alpha.5 UDP idle-resume bug (docs/ROADMAP.md) turned out to
// need only ONE of these two timeouts extended, not both — extending both
// (as the first attempt at this fix did) let IPVS's own per-flow cache
// balloon to a full day of dead entries, which both the kernel and this
// agent's own MetricsCollector (scanning /proc/net/ip_vs_conn every
// heartbeat) then had to churn through, driving sustained high CPU on busy
// nodes. The actual mechanism: EZHIKLB-FORWARD's return-path rule only
// ACCEPTs a backend's reply once conntrack classifies the flow as
// ESTABLISHED/RELATED — that classification depends solely on netfilter's
// conntrack timeout, not on IPVS's own ip_vs_conn entry. A resumed flow is
// re-routed to the correct backend by the listener's own Affinity
// (persistence template, `-p <seconds>`) regardless of whether the old
// ip_vs_conn entry still exists, so that table doesn't need to survive the
// client's idle period — only the conntrack NAT mapping does.
const (
	// udpConntrackTimeoutSeconds: what the firewall's ESTABLISHED check
	// actually depends on. Kept at the longest Affinity preset a listener
	// can choose (24h) so it never undercuts whatever Affinity is set.
	udpConntrackTimeoutSeconds = 86400
	// udpIPVSTimeoutSeconds: IPVS's own connection cache. Short on purpose —
	// bounds /proc/net/ip_vs_conn's size; Affinity handles routing
	// continuity independently of this value.
	udpIPVSTimeoutSeconds = 300
	// nfConntrackMaxEntries gives conntrack room for entries that now live
	// up to 24h instead of ~3 minutes, so concurrent flows don't fill the
	// table and start getting dropped.
	nfConntrackMaxEntries = 2000000
)

func (r *Reconciler) configureKernel(ctx context.Context) error {
	values := map[string]string{
		"net.ipv4.ip_forward":                           "1",
		"net.ipv4.vs.conntrack":                         "1",
		"net.ipv4.vs.snat_reroute":                      "1",
		"net.ipv4.vs.expire_nodest_conn":                "1",
		"net.ipv4.vs.expire_quiescent_template":         "1",
		"net.netfilter.nf_conntrack_udp_timeout":        "60",
		"net.netfilter.nf_conntrack_udp_timeout_stream": strconv.Itoa(udpConntrackTimeoutSeconds),
		"net.netfilter.nf_conntrack_max":                strconv.Itoa(nfConntrackMaxEntries),
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		// Read before write: in the --docker deployment, --network host
		// makes /proc/sys/net/* reflect the *host's* real values (since the
		// container shares the host's network namespace), but the container
		// runtime still mounts /proc/sys read-only by default regardless of
		// capabilities — writing fails with "permission denied" even though
		// the value is already correct, because scripts/install-node.sh
		// pre-sets every one of these on the host before the container ever
		// starts. Only attempt the write when the value actually needs to
		// change, so an already-correct host doesn't require write access.
		procPath := "/proc/sys/" + strings.ReplaceAll(key, ".", "/")
		if current, err := os.ReadFile(procPath); err == nil && strings.TrimSpace(string(current)) == values[key] {
			continue
		}
		if _, err := r.runner.Run(ctx, "sysctl", []string{"-w", key + "=" + values[key]}, ""); err != nil {
			return err
		}
	}
	// IPVS's own UDP connection timeout is independent of conntrack and
	// deliberately kept short — see the block comment above.
	if _, err := r.runner.Run(ctx, "ipvsadm", []string{"--set", "900", "120", strconv.Itoa(udpIPVSTimeoutSeconds)}, ""); err != nil {
		return fmt.Errorf("set IPVS UDP connection timeout: %w", err)
	}
	return nil
}

func (r *Reconciler) warmRoutes(ctx context.Context, services []Service) error {
	seen := map[string]bool{}
	var failures []string
	for _, service := range services {
		for _, destination := range service.Destinations {
			if seen[destination.Address] {
				continue
			}
			seen[destination.Address] = true
			if _, err := r.runner.Run(ctx, "ip", []string{"-4", "route", "get", destination.Address}, ""); err != nil {
				failures = append(failures, err.Error())
				continue
			}
			pingCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
			_, err := r.runner.Run(pingCtx, "ping", []string{"-n", "-c", "1", "-W", "1", destination.Address}, "")
			cancel()
			if err != nil {
				failures = append(failures, destination.Address+": "+err.Error())
			}
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}

func (r *Reconciler) applyFirewall(ctx context.Context, services []Service) error {
	filter := []string{"*filter", ":EZHIKLB-FORWARD - [0:0]", "-F EZHIKLB-FORWARD"}
	nat := []string{"*nat", ":EZHIKLB-SNAT - [0:0]", "-F EZHIKLB-SNAT"}
	for _, service := range services {
		for _, destination := range service.Destinations {
			proto := string(service.Protocol)
			filter = append(filter,
				fmt.Sprintf("-A EZHIKLB-FORWARD -p %s -d %s --dport %d -j ACCEPT", proto, destination.Address, destination.Port),
				fmt.Sprintf("-A EZHIKLB-FORWARD -p %s -s %s --sport %d -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT", proto, destination.Address, destination.Port))
			source, err := r.routeSource(ctx, destination.Address)
			if err != nil {
				return err
			}
			nat = append(nat, fmt.Sprintf("-A EZHIKLB-SNAT -p %s -d %s --dport %d -m ipvs --ipvs --vproto %s --vaddr %s --vport %d --vdir ORIGINAL -j SNAT --to-source %s", proto, destination.Address, destination.Port, proto, service.Address, service.Port, source))
		}
	}
	filter = append(filter, "COMMIT", "")
	nat = append(nat, "COMMIT", "")
	if _, err := r.runner.Run(ctx, "iptables-restore", []string{"--noflush"}, strings.Join(filter, "\n")); err != nil {
		return err
	}
	if _, err := r.runner.Run(ctx, "iptables-restore", []string{"--noflush"}, strings.Join(nat, "\n")); err != nil {
		return err
	}
	if err := r.ensureJump(ctx, "filter", "FORWARD", "EZHIKLB-FORWARD"); err != nil {
		return err
	}
	return r.ensureJump(ctx, "nat", "POSTROUTING", "EZHIKLB-SNAT")
}

func (r *Reconciler) ensureJump(ctx context.Context, table, parent, child string) error {
	args := []string{"-w", "5"}
	if table != "filter" {
		args = append(args, "-t", table)
	}
	check := append(append([]string{}, args...), "-C", parent, "-j", child)
	if _, err := r.runner.Run(ctx, "iptables", check, ""); err == nil {
		return nil
	}
	insert := append(append([]string{}, args...), "-I", parent, "1", "-j", child)
	_, err := r.runner.Run(ctx, "iptables", insert, "")
	return err
}

func (r *Reconciler) routeSource(ctx context.Context, address string) (string, error) {
	output, err := r.runner.Run(ctx, "ip", []string{"-4", "route", "get", address}, "")
	if err != nil {
		return "", err
	}
	fields := strings.Fields(output)
	for i, field := range fields {
		if field == "src" && i+1 < len(fields) {
			if addr, parseErr := netip.ParseAddr(fields[i+1]); parseErr == nil && addr.Is4() {
				return fields[i+1], nil
			}
		}
	}
	return "", fmt.Errorf("route to %s has no IPv4 source: %s", address, strings.TrimSpace(output))
}

func (r *Reconciler) resolveIngress(ctx context.Context, configured string) (string, error) {
	if configured != "" && configured != "0.0.0.0" {
		addr, err := netip.ParseAddr(configured)
		if err != nil || !addr.Is4() {
			return "", fmt.Errorf("invalid ingress address %q", configured)
		}
		return configured, nil
	}
	return r.routeSource(ctx, "1.1.1.1")
}

func (r *Reconciler) loadState() (AppliedState, error) {
	var state AppliedState
	data, err := os.ReadFile(r.statePath)
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return state, fmt.Errorf("parse agent state: %w", err)
	}
	return state, nil
}

func (r *Reconciler) saveState(state AppliedState) error {
	if err := os.MkdirAll(filepath.Dir(r.statePath), 0750); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmp := r.statePath + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0640); err != nil {
		return err
	}
	return os.Rename(tmp, r.statePath)
}

func serviceKey(service Service) string {
	return fmt.Sprintf("%s/%s:%d", service.Protocol, service.Address, service.Port)
}
func destinationKey(destination Destination) string {
	return fmt.Sprintf("%s:%d", destination.Address, destination.Port)
}
func protocolFlag(protocol domain.Protocol) string {
	if protocol == domain.ProtocolTCP {
		return "-t"
	}
	return "-u"
}
func virtualAddress(service Service) string {
	return fmt.Sprintf("%s:%d", service.Address, service.Port)
}
func realAddress(destination Destination) string {
	return fmt.Sprintf("%s:%d", destination.Address, destination.Port)
}
