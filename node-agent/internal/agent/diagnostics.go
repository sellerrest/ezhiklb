package agent

import (
	"context"
	"strings"
	"time"

	"ezhiklb-node-agent/internal/domain"
)

// CollectDiagnostics reports read-only data-plane checks.
func CollectDiagnostics(ctx context.Context, runner Runner, services []Service) domain.NodeDiagnostics {
	result := domain.NodeDiagnostics{CheckedAt: time.Now().UTC(), ServiceCount: len(services)}
	for _, service := range services { result.DestinationCount += len(service.Destinations) }
	if _, err := runner.Run(ctx, "ipvsadm", []string{"-Ln"}, ""); err == nil { result.IPVSAvailable = true } else { result.Error = err.Error() }
	filter, filterErr := runner.Run(ctx, "iptables", []string{"-w", "5", "-S", "EZHIKLB-FORWARD"}, "")
	nat, natErr := runner.Run(ctx, "iptables", []string{"-w", "5", "-t", "nat", "-S", "EZHIKLB-SNAT"}, "")
	result.FirewallReady = filterErr == nil && natErr == nil && strings.Contains(filter, "EZHIKLB-FORWARD") && strings.Contains(nat, "EZHIKLB-SNAT")
	if result.Error == "" && (filterErr != nil || natErr != nil) { result.Error = "EzhikLB firewall chains are unavailable" }
	return result
}
