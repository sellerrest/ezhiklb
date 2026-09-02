package proxy

import (
	"math/rand/v2"
	"sync"
	"sync/atomic"

	"ezhiklb-node-agent/internal/domain"
)

// connCounters tracks in-flight connections per outbound ID for the "least"
// selection strategy — a real least-connections count, not a round-robin
// approximation, since the proxy owns each connection's full lifetime — and
// separately, per outbound, how many distinct client IPs currently have at
// least one open connection, for the "Исходящие" status page's "online, N
// IP" reading.
type connCounters struct {
	mu      sync.Mutex
	counts  map[string]*int64
	ips     map[string]map[string]int // outboundID -> clientIP -> refcount
	bytesRx map[string]*int64         // outboundID -> cumulative bytes client->outbound
	bytesTx map[string]*int64         // outboundID -> cumulative bytes outbound->client
}

func newConnCounters() *connCounters {
	return &connCounters{counts: map[string]*int64{}, ips: map[string]map[string]int{}, bytesRx: map[string]*int64{}, bytesTx: map[string]*int64{}}
}

func (c *connCounters) counter(outboundID string) *int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing, ok := c.counts[outboundID]; ok {
		return existing
	}
	created := new(int64)
	c.counts[outboundID] = created
	return created
}

func (c *connCounters) inc(outboundID string) { atomic.AddInt64(c.counter(outboundID), 1) }
func (c *connCounters) dec(outboundID string) { atomic.AddInt64(c.counter(outboundID), -1) }

// incIP/decIP are called once per accepted connection, alongside inc/dec,
// with the client's IP (no port) — a client with several concurrent
// connections to the same outbound still counts as one IP.
func (c *connCounters) incIP(outboundID, clientIP string) {
	if clientIP == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	byIP, ok := c.ips[outboundID]
	if !ok {
		byIP = map[string]int{}
		c.ips[outboundID] = byIP
	}
	byIP[clientIP]++
}

func (c *connCounters) decIP(outboundID, clientIP string) {
	if clientIP == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	byIP, ok := c.ips[outboundID]
	if !ok {
		return
	}
	byIP[clientIP]--
	if byIP[clientIP] <= 0 {
		delete(byIP, clientIP)
	}
}

func (c *connCounters) ipCount(outboundID string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.ips[outboundID])
}

// allIPs unions every distinct client IP currently counted across all
// outbounds this counter set tracks — a client connected to two outbounds
// through the same router counts once, not twice. Feeds the node-wide
// active-IPs metric (see Manager.ActiveClientIPs).
func (c *connCounters) allIPs() map[string]struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make(map[string]struct{})
	for _, byIP := range c.ips {
		for ip := range byIP {
			result[ip] = struct{}{}
		}
	}
	return result
}

func (c *connCounters) bytesCounter(m map[string]*int64, outboundID string) *int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing, ok := m[outboundID]; ok {
		return existing
	}
	created := new(int64)
	m[outboundID] = created
	return created
}

// addRx/addTx accumulate bytes copied client->outbound ("rx", what the
// service receives from clients) and outbound->client ("tx", what it sends
// back) — cumulative counters; the panel derives a Мбит/с rate by diffing
// two consecutive polls, the same way it already does for node-wide network
// metrics.
func (c *connCounters) addRx(outboundID string, n int64) {
	if n > 0 {
		atomic.AddInt64(c.bytesCounter(c.bytesRx, outboundID), n)
	}
}
func (c *connCounters) addTx(outboundID string, n int64) {
	if n > 0 {
		atomic.AddInt64(c.bytesCounter(c.bytesTx, outboundID), n)
	}
}

// snapshot reports, for every outbound this counter set has ever touched,
// the current in-flight connection count, distinct client IP count, and
// cumulative byte counters.
func (c *connCounters) snapshot() map[string]OutboundStat {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make(map[string]OutboundStat, len(c.counts))
	touch := func(outboundID string) OutboundStat { return result[outboundID] }
	for outboundID, count := range c.counts {
		stat := touch(outboundID)
		stat.ActiveConnections = atomic.LoadInt64(count)
		stat.ActiveIPs = len(c.ips[outboundID])
		result[outboundID] = stat
	}
	for outboundID, byIP := range c.ips {
		stat := touch(outboundID)
		stat.ActiveIPs = len(byIP)
		result[outboundID] = stat
	}
	for outboundID, n := range c.bytesRx {
		stat := touch(outboundID)
		stat.BytesRx = atomic.LoadInt64(n)
		result[outboundID] = stat
	}
	for outboundID, n := range c.bytesTx {
		stat := touch(outboundID)
		stat.BytesTx = atomic.LoadInt64(n)
		result[outboundID] = stat
	}
	return result
}
func (c *connCounters) value(outboundID string) int64 {
	return atomic.LoadInt64(c.counter(outboundID))
}

// pickTarget chooses one target from a binding's resolved outbounds. It
// first drops targets the health source has marked unreachable; if that
// leaves nothing (everything looks down), it falls back to the full list
// rather than dropping the connection outright — a client is more likely to
// get a working attempt than a guaranteed failure, and the health monitor
// will recover the picture on its next successful probe.
func pickTarget(strategy domain.SelectionStrategy, targets []ResolvedTarget, health HealthSource, conns *connCounters) (ResolvedTarget, bool) {
	if len(targets) == 0 {
		return ResolvedTarget{}, false
	}
	candidates := make([]ResolvedTarget, 0, len(targets))
	for _, target := range targets {
		if health.IsReachable(target.endpoint()) {
			candidates = append(candidates, target)
		}
	}
	if len(candidates) == 0 {
		candidates = targets
	}
	if len(candidates) == 1 {
		return candidates[0], true
	}

	switch strategy {
	case domain.SelectionManual:
		return pickWeighted(candidates), true
	case domain.SelectionPing:
		return pickLowestLatency(candidates, health), true
	default: // SelectionLeast, and any unrecognized value
		return pickLeastConnections(candidates, conns), true
	}
}

func pickWeighted(candidates []ResolvedTarget) ResolvedTarget {
	total := 0
	for _, target := range candidates {
		if target.WeightPercent > 0 {
			total += target.WeightPercent
		}
	}
	if total <= 0 {
		return candidates[rand.IntN(len(candidates))]
	}
	roll := rand.IntN(total)
	for _, target := range candidates {
		if target.WeightPercent <= 0 {
			continue
		}
		if roll < target.WeightPercent {
			return target
		}
		roll -= target.WeightPercent
	}
	return candidates[len(candidates)-1]
}

// pickLowestLatency falls back to least-connections among targets the
// health monitor has no timing for yet (freshly added outbounds), so a
// binding still balances sensibly before the first health probe completes.
func pickLowestLatency(candidates []ResolvedTarget, health HealthSource) ResolvedTarget {
	best := candidates[0]
	bestLatency, bestKnown := health.Latency(best.endpoint())
	for _, target := range candidates[1:] {
		latency, known := health.Latency(target.endpoint())
		if !known {
			continue
		}
		if !bestKnown || latency < bestLatency {
			best, bestLatency, bestKnown = target, latency, true
		}
	}
	if !bestKnown {
		return candidates[rand.IntN(len(candidates))]
	}
	return best
}

func pickLeastConnections(candidates []ResolvedTarget, conns *connCounters) ResolvedTarget {
	best := candidates[0]
	bestCount := conns.value(best.OutboundID)
	for _, target := range candidates[1:] {
		count := conns.value(target.OutboundID)
		if count < bestCount {
			best, bestCount = target, count
		}
	}
	return best
}
