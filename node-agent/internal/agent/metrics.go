package agent

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"ezhiklb-node-agent/internal/domain"
)

type metricSample struct {
	at              time.Time
	cpuTotal        uint64
	cpuIdle         uint64
	rxBytes, txBytes uint64
}

// activeIPsScanInterval rate-limits how often /proc/net/ip_vs_conn gets
// scanned. That file's size tracks how many IPVS connection-table entries
// currently exist, which can be very large on a busy node (see
// udpIPVSTimeoutSeconds in reconciler.go for why it's now bounded); reading
// it on every 15s heartbeat was needlessly expensive when the 1-minute
// active-IP window it feeds doesn't need finer resolution than this.
const activeIPsScanInterval = 30 * time.Second

// MetricsCollector keeps only a one-minute in-memory window. Nothing is
// written to disk and the panel receives one aggregate with each heartbeat.
type MetricsCollector struct {
	mu                sync.Mutex
	samples           []metricSample
	seenIPs           map[string]time.Time
	lastActiveIPsScan time.Time
	now               func() time.Time
}

func NewMetricsCollector() *MetricsCollector {
	return &MetricsCollector{seenIPs: make(map[string]time.Time), now: time.Now}
}

func (c *MetricsCollector) Collect() (domain.NodeMetrics, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now().UTC()
	cpuTotal, cpuIdle, err := readCPU("/proc/stat")
	if err != nil { return domain.NodeMetrics{}, err }
	rx, tx, err := readNetwork("/proc/net/dev")
	if err != nil { return domain.NodeMetrics{}, err }
	ram, err := readRAM("/proc/meminfo")
	if err != nil { return domain.NodeMetrics{}, err }
	load := readLoad("/proc/loadavg")
	if now.Sub(c.lastActiveIPsScan) >= activeIPsScanInterval {
		for _, address := range readActiveIPVSClients("/proc/net/ip_vs_conn") { c.seenIPs[address] = now }
		c.lastActiveIPsScan = now
	}
	cutoff := now.Add(-time.Minute)
	for address, seen := range c.seenIPs { if seen.Before(cutoff) { delete(c.seenIPs, address) } }

	current := metricSample{at: now, cpuTotal: cpuTotal, cpuIdle: cpuIdle, rxBytes: rx, txBytes: tx}
	c.samples = append(c.samples, current)
	baseIndex := 0
	for baseIndex+1 < len(c.samples) && !c.samples[baseIndex+1].at.After(cutoff) { baseIndex++ }
	if baseIndex > 0 { c.samples = append([]metricSample(nil), c.samples[baseIndex:]...) }
	base := c.samples[0]
	var cpuPercent float64
	var rxBPS, txBPS uint64
	if elapsed := now.Sub(base.at).Seconds(); elapsed > 0 {
		totalDelta, idleDelta := cpuTotal-base.cpuTotal, cpuIdle-base.cpuIdle
		if totalDelta > 0 && totalDelta >= idleDelta { cpuPercent = float64(totalDelta-idleDelta) * 100 / float64(totalDelta) }
		if rx >= base.rxBytes { rxBPS = uint64(float64(rx-base.rxBytes) / elapsed) }
		if tx >= base.txBytes { txBPS = uint64(float64(tx-base.txBytes) / elapsed) }
	}
	return domain.NodeMetrics{RAMUsedPercent: ram, CPUUsedPercent: cpuPercent, Load1: load, CPUCores: runtime.NumCPU(), NetworkRxBPS: rxBPS, NetworkTxBPS: txBPS, ActiveIPs: len(c.seenIPs), CollectedAt: now}, nil
}

func readCPU(path string) (uint64, uint64, error) {
	data, err := os.ReadFile(path); if err != nil { return 0, 0, err }
	fields := strings.Fields(strings.SplitN(string(data), "\n", 2)[0])
	if len(fields) < 5 || fields[0] != "cpu" { return 0, 0, fmt.Errorf("invalid %s", path) }
	var total uint64
	values := make([]uint64, 0, len(fields)-1)
	for _, field := range fields[1:] { value, parseErr := strconv.ParseUint(field, 10, 64); if parseErr != nil { return 0, 0, parseErr }; values = append(values, value); total += value }
	idle := values[3]
	if len(values) > 4 { idle += values[4] }
	return total, idle, nil
}

func readRAM(path string) (float64, error) {
	file, err := os.Open(path); if err != nil { return 0, err }; defer file.Close()
	var total, available float64
	scanner := bufio.NewScanner(file)
	for scanner.Scan() { fields := strings.Fields(scanner.Text()); if len(fields) < 2 { continue }; value, _ := strconv.ParseFloat(fields[1], 64); if fields[0] == "MemTotal:" { total = value }; if fields[0] == "MemAvailable:" { available = value } }
	if err := scanner.Err(); err != nil { return 0, err }
	if total <= 0 { return 0, fmt.Errorf("MemTotal missing in %s", path) }
	return (total - available) * 100 / total, nil
}

func readNetwork(path string) (uint64, uint64, error) {
	file, err := os.Open(path); if err != nil { return 0, 0, err }; defer file.Close()
	var rx, tx uint64
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text()); separator := strings.IndexByte(line, ':'); if separator < 0 { continue }
		name := strings.TrimSpace(line[:separator]); if name == "lo" { continue }
		fields := strings.Fields(line[separator+1:]); if len(fields) < 9 { continue }
		rxValue, rxErr := strconv.ParseUint(fields[0], 10, 64); txValue, txErr := strconv.ParseUint(fields[8], 10, 64)
		if rxErr == nil { rx += rxValue }; if txErr == nil { tx += txValue }
	}
	return rx, tx, scanner.Err()
}

func readLoad(path string) float64 {
	data, err := os.ReadFile(path); if err != nil { return 0 }
	fields := strings.Fields(string(data)); if len(fields) == 0 { return 0 }
	value, _ := strconv.ParseFloat(fields[0], 64); return value
}

func readActiveIPVSClients(path string) []string {
	file, err := os.Open(path); if err != nil { return nil }; defer file.Close()
	unique := make(map[string]struct{})
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 || (fields[0] != "TCP" && fields[0] != "UDP") { continue }
		unique[fields[1]] = struct{}{}
	}
	result := make([]string, 0, len(unique)); for address := range unique { result = append(result, address) }; return result
}
