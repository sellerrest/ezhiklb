package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func metricFixture(t *testing.T, value string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture")
	if err := os.WriteFile(path, []byte(value), 0600); err != nil { t.Fatal(err) }
	return path
}

func TestReadCPU(t *testing.T) {
	total, idle, err := readCPU(metricFixture(t, "cpu  100 20 30 400 50 0 0 0\n"))
	if err != nil { t.Fatal(err) }
	if total != 600 || idle != 450 { t.Fatalf("unexpected CPU counters: total=%d idle=%d", total, idle) }
}

func TestReadRAM(t *testing.T) {
	used, err := readRAM(metricFixture(t, "MemTotal: 1000 kB\nMemAvailable: 720 kB\n"))
	if err != nil { t.Fatal(err) }
	if used != 28 { t.Fatalf("unexpected RAM percentage: %v", used) }
}

func TestReadNetworkExcludesLoopback(t *testing.T) {
	path := metricFixture(t, "Inter-| Receive | Transmit\n lo: 100 0 0 0 0 0 0 0 200 0 0 0 0 0 0 0\n eth0: 1000 0 0 0 0 0 0 0 2000 0 0 0 0 0 0 0\n")
	rx, tx, err := readNetwork(path)
	if err != nil { t.Fatal(err) }
	if rx != 1000 || tx != 2000 { t.Fatalf("unexpected network counters: rx=%d tx=%d", rx, tx) }
}

func TestReadActiveIPVSClientsDeduplicates(t *testing.T) {
	path := metricFixture(t, "Pro FromIP FPrt ToIP TPrt DestIP DPrt State Expires\nUDP C0A80101 1234 C0A80102 8000 C0A80103 8000 UDP 30\nTCP C0A80101 1235 C0A80102 8001 C0A80103 8001 ESTABLISHED 90\nUDP C0A80104 1236 C0A80102 8000 C0A80103 8000 UDP 30\n")
	if clients := readActiveIPVSClients(path); len(clients) != 2 { t.Fatalf("expected 2 unique clients, got %d", len(clients)) }
}

type fakeActiveClientIPs map[string]struct{}

func (f fakeActiveClientIPs) ActiveClientIPs() map[string]struct{} { return f }

// A node running only SNI/HTTP-routed Ядра (the L7 proxy, not IPVS) used to
// always report 0 active IPs node-wide — IPVS's own conntrack scan is blind
// to that traffic entirely. updateActiveIPs must fold both sources in.
func TestUpdateActiveIPsFoldsInL7ClientsAlongsideIPVS(t *testing.T) {
	c := NewMetricsCollector(fakeActiveClientIPs{"203.0.113.5": {}})
	now := time.Now()

	got := c.updateActiveIPs(now, true, []string{"198.51.100.9"})
	if got != 2 {
		t.Fatalf("updateActiveIPs = %d, want 2 (one IPVS client + one L7 client)", got)
	}

	// Same L7 client IP as an IPVS one must not be double-counted.
	c2 := NewMetricsCollector(fakeActiveClientIPs{"198.51.100.9": {}})
	got2 := c2.updateActiveIPs(now, true, []string{"198.51.100.9"})
	if got2 != 1 {
		t.Fatalf("updateActiveIPs = %d, want 1 (same IP seen on both sides)", got2)
	}
}

// L7 clients are re-added on every call (cheap, in-memory) even when the
// IPVS scan itself is skipped (rate-limited) — the moment a client actually
// disconnects, ActiveClientIPs() stops reporting it and this evicts it
// within a minute, same as the IPVS side already did.
func TestUpdateActiveIPsEvictsL7ClientAfterItDisconnects(t *testing.T) {
	l7 := fakeActiveClientIPs{"203.0.113.5": {}}
	c := NewMetricsCollector(l7)
	now := time.Now()

	if got := c.updateActiveIPs(now, false, nil); got != 1 {
		t.Fatalf("updateActiveIPs = %d, want 1 while still connected", got)
	}

	delete(l7, "203.0.113.5") // client disconnected
	if got := c.updateActiveIPs(now.Add(90*time.Second), false, nil); got != 0 {
		t.Fatalf("updateActiveIPs = %d, want 0 once past the disconnect + 1-minute window", got)
	}
}
