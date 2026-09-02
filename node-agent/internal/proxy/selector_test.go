package proxy

import (
	"testing"
	"time"

	"ezhiklb-node-agent/internal/domain"
)

type fakeHealth struct {
	down    map[string]bool
	latency map[string]time.Duration
}

func (f fakeHealth) IsReachable(address string) bool { return !f.down[address] }
func (f fakeHealth) Latency(address string) (time.Duration, bool) {
	d, ok := f.latency[address]
	return d, ok
}

func TestPickTargetSingleTarget(t *testing.T) {
	targets := []ResolvedTarget{{OutboundID: "a", Address: "10.0.0.1", Port: 80}}
	got, ok := pickTarget(domain.SelectionLeast, targets, fakeHealth{}, newConnCounters())
	if !ok || got.OutboundID != "a" {
		t.Fatalf("got %+v, ok=%v", got, ok)
	}
}

func TestPickTargetSkipsUnreachable(t *testing.T) {
	targets := []ResolvedTarget{
		{OutboundID: "down", Address: "10.0.0.1", Port: 80},
		{OutboundID: "up", Address: "10.0.0.2", Port: 80},
	}
	health := fakeHealth{down: map[string]bool{"10.0.0.1:80": true}}
	for i := 0; i < 20; i++ {
		got, ok := pickTarget(domain.SelectionLeast, targets, health, newConnCounters())
		if !ok || got.OutboundID != "up" {
			t.Fatalf("expected the reachable target every time, got %+v", got)
		}
	}
}

func TestPickTargetFailsOpenWhenAllUnreachable(t *testing.T) {
	targets := []ResolvedTarget{{OutboundID: "a", Address: "10.0.0.1", Port: 80}}
	health := fakeHealth{down: map[string]bool{"10.0.0.1:80": true}}
	got, ok := pickTarget(domain.SelectionLeast, targets, health, newConnCounters())
	if !ok || got.OutboundID != "a" {
		t.Fatal("should fail open and still return the only target rather than drop the connection")
	}
}

func TestPickTargetManualRespectsWeights(t *testing.T) {
	targets := []ResolvedTarget{
		{OutboundID: "small", Address: "10.0.0.1", Port: 80, WeightPercent: 10},
		{OutboundID: "big", Address: "10.0.0.2", Port: 80, WeightPercent: 90},
	}
	counts := map[string]int{}
	for i := 0; i < 2000; i++ {
		got, ok := pickTarget(domain.SelectionManual, targets, fakeHealth{}, newConnCounters())
		if !ok {
			t.Fatal("expected a target")
		}
		counts[got.OutboundID]++
	}
	// With 2000 draws at a 90/10 split, expect roughly 1800/200 — assert a
	// generous band so this isn't flaky, but tight enough to catch the
	// weights being ignored or inverted.
	if counts["big"] < 1600 || counts["big"] > 2000 {
		t.Fatalf("big got %d/2000, want roughly 1800", counts["big"])
	}
	if counts["small"] < 20 {
		t.Fatalf("small got %d/2000, want roughly 200 (some traffic, not zero)", counts["small"])
	}
}

func TestPickTargetLeastConnections(t *testing.T) {
	targets := []ResolvedTarget{
		{OutboundID: "busy", Address: "10.0.0.1", Port: 80},
		{OutboundID: "idle", Address: "10.0.0.2", Port: 80},
	}
	conns := newConnCounters()
	conns.inc("busy")
	conns.inc("busy")
	conns.inc("idle")
	got, ok := pickTarget(domain.SelectionLeast, targets, fakeHealth{}, conns)
	if !ok || got.OutboundID != "idle" {
		t.Fatalf("expected the target with fewer active connections, got %+v", got)
	}
}

func TestPickTargetPingPrefersLowestLatency(t *testing.T) {
	targets := []ResolvedTarget{
		{OutboundID: "slow", Address: "10.0.0.1", Port: 80},
		{OutboundID: "fast", Address: "10.0.0.2", Port: 80},
	}
	health := fakeHealth{latency: map[string]time.Duration{
		"10.0.0.1:80": 200 * time.Millisecond,
		"10.0.0.2:80": 20 * time.Millisecond,
	}}
	got, ok := pickTarget(domain.SelectionPing, targets, health, newConnCounters())
	if !ok || got.OutboundID != "fast" {
		t.Fatalf("expected the lowest-latency target, got %+v", got)
	}
}

func TestConnCountersIndependentPerOutbound(t *testing.T) {
	c := newConnCounters()
	c.inc("a")
	c.inc("a")
	c.inc("b")
	c.dec("a")
	if got := c.value("a"); got != 1 {
		t.Fatalf("a = %d, want 1", got)
	}
	if got := c.value("b"); got != 1 {
		t.Fatalf("b = %d, want 1", got)
	}
}
