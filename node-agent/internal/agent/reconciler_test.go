package agent

import (
	"context"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"ezhiklb-node-agent/internal/domain"
)

type recordedCall struct {
	name  string
	args  []string
	input string
}

type fakeRunner struct{ calls []recordedCall }

func (f *fakeRunner) Run(_ context.Context, name string, args []string, input string) (string, error) {
	f.calls = append(f.calls, recordedCall{name: name, args: append([]string(nil), args...), input: input})
	if name == "ip" && strings.Join(args, " ") == "-4 route get 192.0.2.10" {
		return "192.0.2.10 via 198.51.100.1 dev eth0 src 198.51.100.10", nil
	}
	if name == "ip" && strings.Join(args, " ") == "-4 route get 1.1.1.1" {
		return "1.1.1.1 via 198.51.100.1 dev eth0 src 198.51.100.10", nil
	}
	if name == "ip" && strings.Join(args, " ") == "-o -4 addr show" {
		return "2: eth0 inet 198.51.100.10/24 scope global eth0", nil
	}
	return "", nil
}

func newTestReconciler(runner Runner, path string) *Reconciler {
	return NewReconciler(context.Background(), runner, path, nil)
}

// compileServices only ever handles UDP now — TCP is routed entirely by
// internal/proxy (see reconciler_test's proxy-focused coverage in
// internal/proxy, and TestReconcileAppliesL7ProxyForTCPInbounds below).
func TestCompileServicesIsUDPOnly(t *testing.T) {
	config := domain.DefaultProfileConfig()
	config.Inbounds = []domain.Inbound{{ID: "in1", Name: "Dual", Enabled: true, ListenPort: 8002, TCP: true, UDP: true}}
	config.Outbounds = []domain.Outbound{{ID: "out1", Name: "Backend", Address: "192.0.2.10", Port: 9000, Enabled: true, HealthCheck: domain.DefaultHealthCheck()}}
	config.Bindings = []domain.Binding{{ID: "b1", Enabled: true, InboundID: "in1", SelectionStrategy: domain.SelectionManual, Targets: []domain.BindingTarget{{OutboundID: "out1", WeightPercent: 100}}}}

	services := compileServices(config, "198.51.100.10")
	if len(services) != 1 {
		t.Fatalf("services = %d, want 1 (UDP only)", len(services))
	}
	if services[0].Protocol != domain.ProtocolUDP {
		t.Fatalf("protocol = %s, want udp", services[0].Protocol)
	}
	if services[0].Destinations[0].Port != 9000 {
		t.Fatalf("backend port = %d, want 9000", services[0].Destinations[0].Port)
	}
}

func TestCompileServicesSkipsTCPOnlyInbound(t *testing.T) {
	config := domain.DefaultProfileConfig()
	config.Inbounds = []domain.Inbound{{ID: "in1", Name: "TCP only", Enabled: true, ListenPort: 8002, TCP: true, UDP: false}}
	services := compileServices(config, "198.51.100.10")
	if len(services) != 0 {
		t.Fatalf("services = %d, want 0 (TCP-only inbound has nothing for IPVS to do)", len(services))
	}
}

func TestApplyIPVSNeverClearsGlobalTable(t *testing.T) {
	runner := &fakeRunner{}
	r := newTestReconciler(runner, t.TempDir()+"/state.json")
	service := Service{Protocol: domain.ProtocolUDP, Address: "198.51.100.10", Port: 8002, Scheduler: "wrr", Destinations: []Destination{{ID: "backend", Address: "192.0.2.10", Port: 9000, Weight: 2}}}
	if err := r.applyIPVS(context.Background(), nil, []Service{service}); err != nil {
		t.Fatal(err)
	}
	for _, call := range runner.calls {
		if call.name == "ipvsadm" && len(call.args) == 1 && call.args[0] == "-C" {
			t.Fatal("reconciler attempted to clear the global IPVS table")
		}
	}
}

func TestResetConnectionStateIsScopedToManagedServices(t *testing.T) {
	runner := &fakeRunner{}
	r := newTestReconciler(runner, filepath.Join(t.TempDir(), "state.json"))
	service := Service{Protocol: domain.ProtocolUDP, Address: "198.51.100.10", Port: 8002, Scheduler: "wrr", AffinitySecs: 10800, Destinations: []Destination{{ID: "backend", Address: "192.0.2.10", Port: 9000, Weight: 2}}}
	if err := r.resetConnectionState(context.Background(), []Service{service}, []Service{service}); err != nil {
		t.Fatal(err)
	}
	var deletedService, purgedConntrack, recreatedService bool
	for _, call := range runner.calls {
		joined := strings.Join(call.args, " ")
		if call.name == "ipvsadm" && joined == "-C" {
			t.Fatal("reset attempted to clear the global IPVS table")
		}
		if call.name == "conntrack" && joined == "-F" {
			t.Fatal("reset attempted to clear the global conntrack table")
		}
		if call.name == "ipvsadm" && joined == "-D -u 198.51.100.10:8002" {
			deletedService = true
		}
		if call.name == "conntrack" && joined == "-D -f ipv4 -p udp -d 198.51.100.10 --dport 8002" {
			purgedConntrack = true
		}
		if call.name == "ipvsadm" && strings.HasPrefix(joined, "-A -u 198.51.100.10:8002") {
			recreatedService = true
		}
	}
	if !deletedService || !purgedConntrack || !recreatedService {
		t.Fatalf("scoped reset was incomplete: %#v", runner.calls)
	}
}

func TestRestoreRebuildsSavedDataPlane(t *testing.T) {
	runner := &fakeRunner{}
	r := newTestReconciler(runner, filepath.Join(t.TempDir(), "state.json"))
	service := Service{Protocol: domain.ProtocolUDP, Address: "198.51.100.10", Port: 8002, Scheduler: "wrr", AffinitySecs: 10800, Destinations: []Destination{{ID: "backend", Address: "192.0.2.10", Port: 9000, Weight: 2}}}
	if err := r.saveState(AppliedState{Revision: 7, Services: []Service{service}, Config: domain.DefaultProfileConfig()}); err != nil {
		t.Fatal(err)
	}
	revision, err := r.Restore(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if revision != 7 {
		t.Fatalf("revision = %d, want 7", revision)
	}
	var serviceRestored, destinationRestored bool
	for _, call := range runner.calls {
		joined := strings.Join(call.args, " ")
		if call.name == "ipvsadm" && strings.Contains(joined, "-E -u 198.51.100.10:8002") {
			serviceRestored = true
		}
		if call.name == "ipvsadm" && strings.Contains(joined, "-e -u 198.51.100.10:8002 -r 192.0.2.10:9000") {
			destinationRestored = true
		}
	}
	if !serviceRestored || !destinationRestored {
		t.Fatalf("saved IPVS state was not restored: %#v", runner.calls)
	}
}

// TestReconcileAppliesL7ProxyForTCPInbounds confirms Reconcile starts a real
// TCP listener for a TCP-mode inbound (via internal/proxy) alongside its
// IPVS/UDP handling, and persists it into AppliedState.Config so Restore()
// can bring it back after a reboot.
func TestReconcileAppliesL7ProxyForTCPInbounds(t *testing.T) {
	runner := &fakeRunner{}
	r := newTestReconciler(runner, filepath.Join(t.TempDir(), "state.json"))

	// Ask the OS for a free ephemeral port up front so the test doesn't
	// depend on any fixed port being free on the machine running it —
	// domain.Inbound.ListenPort can't be 0 (Validate rejects that).
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	freePort := uint16(probe.Addr().(*net.TCPAddr).Port)
	probe.Close()

	desired := domain.NodeDesiredState{
		IngressAddress: "198.51.100.10",
		Revision:       1,
		Config: domain.ProfileConfig{
			SchemaVersion: domain.SchemaVersion,
			Inbounds:      []domain.Inbound{{ID: "in1", Name: "Web", Enabled: true, ListenAddress: "127.0.0.1", ListenPort: freePort, Mode: domain.InboundModeTCP, TCP: true}},
			Outbounds:     []domain.Outbound{{ID: "out1", Name: "Backend", Address: "192.0.2.10", Port: 9000, Enabled: true, HealthCheck: domain.DefaultHealthCheck()}},
			Bindings:      []domain.Binding{{ID: "b1", Enabled: true, InboundID: "in1", SelectionStrategy: domain.SelectionLeast, Targets: []domain.BindingTarget{{OutboundID: "out1", WeightPercent: 100}}}},
		},
	}
	if err := r.Reconcile(context.Background(), desired); err != nil {
		t.Fatalf("reconcile with a TCP inbound failed: %v", err)
	}
	state, err := r.loadState()
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Config.Inbounds) != 1 || state.Config.Inbounds[0].ID != "in1" {
		t.Fatalf("applied config did not persist the TCP inbound: %#v", state.Config)
	}
}
