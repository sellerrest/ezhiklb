package agent

import (
	"os"
	"path/filepath"
	"testing"
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
