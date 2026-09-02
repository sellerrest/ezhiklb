package agent

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"ezhiklb-node-agent/internal/domain"
)

func CollectIPVSStats(ctx context.Context, runner Runner) ([]domain.ServiceStat, error) {
	output, err := runner.Run(ctx, "ipvsadm", []string{"-Ln", "--stats", "--exact"}, "")
	if err != nil {
		return nil, err
	}
	return ParseIPVSStats(output, time.Now().UTC())
}

func ParseIPVSStats(output string, collectedAt time.Time) ([]domain.ServiceStat, error) {
	var result []domain.ServiceStat
	var current *domain.ServiceStat
	for _, raw := range strings.Split(output, "\n") {
		fields := strings.Fields(raw)
		if len(fields) == 0 {
			continue
		}
		if fields[0] == "TCP" || fields[0] == "UDP" {
			if len(fields) < 7 {
				return nil, fmt.Errorf("invalid IPVS service stats line: %q", raw)
			}
			address, port, err := splitAddressPort(fields[1])
			if err != nil {
				return nil, err
			}
			stat := domain.ServiceStat{Protocol: domain.Protocol(strings.ToLower(fields[0])), ListenAddress: address, ListenPort: port, CollectedAt: collectedAt}
			if err := parseCounters(fields[2:7], &stat); err != nil {
				return nil, err
			}
			result = append(result, stat)
			current = &result[len(result)-1]
			continue
		}
		if fields[0] == "->" && current != nil {
			if len(fields) < 7 {
				return nil, fmt.Errorf("invalid IPVS destination stats line: %q", raw)
			}
			address, port, err := splitAddressPort(fields[1])
			if err != nil {
				return nil, err
			}
			stat := domain.ServiceStat{Protocol: current.Protocol, ListenAddress: current.ListenAddress, ListenPort: current.ListenPort, BackendAddress: address, BackendPort: port, CollectedAt: collectedAt}
			if err := parseCounters(fields[len(fields)-5:], &stat); err != nil {
				return nil, err
			}
			result = append(result, stat)
		}
	}
	return result, nil
}

func splitAddressPort(value string) (string, uint16, error) {
	separator := strings.LastIndexByte(value, ':')
	if separator < 1 {
		return "", 0, fmt.Errorf("invalid IPVS address: %q", value)
	}
	port, err := strconv.ParseUint(value[separator+1:], 10, 16)
	if err != nil {
		return "", 0, fmt.Errorf("invalid IPVS port in %q: %w", value, err)
	}
	return value[:separator], uint16(port), nil
}

func parseCounters(values []string, stat *domain.ServiceStat) error {
	if len(values) != 5 {
		return fmt.Errorf("expected five IPVS counters")
	}
	counters := []*uint64{&stat.Connections, &stat.IncomingPackets, &stat.OutgoingPackets, &stat.IncomingBytes, &stat.OutgoingBytes}
	for index, value := range values {
		parsed, err := strconv.ParseUint(value, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid IPVS counter %q: %w", value, err)
		}
		*counters[index] = parsed
	}
	return nil
}
