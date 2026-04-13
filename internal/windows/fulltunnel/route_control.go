package fulltunnel

import (
	"context"
	"strings"
)

const routeMetricDefault = 0

type routeHandle struct {
	status RouteStatus
}

func newWindowsRouteHandle(adapter AdapterStatus, plan RoutePlan) *routeHandle {
	status := RouteStatus{
		State:           RouteStatePlanned,
		Mode:            plan.Mode,
		IPv4CIDRs:       append([]string(nil), plan.IPv4CIDRs...),
		IPv6CIDRs:       append([]string(nil), plan.IPv6CIDRs...),
		DNSServers:      append([]string(nil), plan.DNSServers...),
		AllowRollback:   plan.AllowRollback,
		RequiresDefault: plan.RequiresDefault,
	}
	status.Operations = buildRouteOperations(adapter, plan)
	status.Rollback = buildRollbackOperations(adapter, status.Operations, plan)
	status.CleanupRequired = len(status.Rollback) > 0
	return &routeHandle{status: status}
}

func (h *routeHandle) Status() RouteStatus {
	if h == nil {
		return RouteStatus{}
	}
	return cloneRouteStatus(h.status)
}

func (h *routeHandle) markFailed(err error) {
	if h == nil {
		return
	}
	h.status.State = RouteStateFailed
	h.status.LastError = errorString(err)
}

func (h *routeHandle) markApplied() {
	if h == nil {
		return
	}
	h.status.State = RouteStateApplied
	h.status.Applied = true
	h.status.CleanupRequired = len(h.status.Rollback) > 0
	for idx := range h.status.Operations {
		h.status.Operations[idx].State = RouteOperationApplied
	}
	for idx := range h.status.Rollback {
		h.status.Rollback[idx].State = RouteOperationCleanupPending
	}
}

func (h *routeHandle) Cleanup(context.Context) error {
	if h == nil {
		return nil
	}
	if h.status.Applied {
		h.status.State = RouteStateCleaned
	}
	h.status.Applied = false
	h.status.CleanupRequired = false
	for idx := range h.status.Rollback {
		if h.status.Rollback[idx].State == RouteOperationCleanupPending {
			h.status.Rollback[idx].State = RouteOperationCleaned
		}
	}
	return nil
}

func buildRouteOperations(adapter AdapterStatus, plan RoutePlan) []RouteOperationStatus {
	var operations []RouteOperationStatus
	interfaceArg := routeInterfaceArgument(adapter)

	for _, cidr := range plan.IPv4CIDRs {
		cidr = strings.TrimSpace(cidr)
		if cidr == "" {
			continue
		}
		operations = append(operations, RouteOperationStatus{
			Kind:          RouteOperationAddRoute,
			Family:        "ipv4",
			Target:        cidr,
			Via:           "0.0.0.0",
			Metric:        routeMetricDefault,
			Interface:     adapter.Name,
			InterfaceLUID: adapter.NativeLUID,
			Command: CommandSpec{
				Executable: "netsh.exe",
				Args: []string{
					"interface", "ipv4", "add", "route",
					"prefix=" + cidr,
					"interface=" + interfaceArg,
					"nexthop=0.0.0.0",
					"metric=0",
					"store=active",
				},
			},
			State: RouteOperationPlanned,
			Note:  "Route command scaffold only; Windows route execution is not wired yet.",
		})
	}

	for _, cidr := range plan.IPv6CIDRs {
		cidr = strings.TrimSpace(cidr)
		if cidr == "" {
			continue
		}
		operations = append(operations, RouteOperationStatus{
			Kind:          RouteOperationAddRoute,
			Family:        "ipv6",
			Target:        cidr,
			Via:           "::",
			Metric:        routeMetricDefault,
			Interface:     adapter.Name,
			InterfaceLUID: adapter.NativeLUID,
			Command: CommandSpec{
				Executable: "netsh.exe",
				Args: []string{
					"interface", "ipv6", "add", "route",
					"prefix=" + cidr,
					"interface=" + interfaceArg,
					"publish=no",
					"store=active",
				},
			},
			State: RouteOperationPlanned,
			Note:  "Route command scaffold only; Windows route execution is not wired yet.",
		})
	}

	if dnsServers := trimStrings(plan.DNSServers); len(dnsServers) > 0 {
		args := []string{
			"-NoProfile",
			"-NonInteractive",
			"-Command",
			"Set-DnsClientServerAddress",
			"-InterfaceAlias",
			adapter.Name,
			"-ServerAddresses",
		}
		args = append(args, dnsServers...)
		operations = append(operations, RouteOperationStatus{
			Kind:          RouteOperationSetDNS,
			Target:        strings.Join(dnsServers, ","),
			Interface:     adapter.Name,
			InterfaceLUID: adapter.NativeLUID,
			Command: CommandSpec{
				Executable: "powershell.exe",
				Args:       args,
			},
			State: RouteOperationPlanned,
			Note:  "DNS command scaffold only; previous resolver state capture is not implemented yet.",
		})
	}

	return operations
}

func buildRollbackOperations(adapter AdapterStatus, operations []RouteOperationStatus, plan RoutePlan) []RollbackStatus {
	if !plan.AllowRollback {
		return nil
	}

	var rollback []RollbackStatus
	interfaceArg := routeInterfaceArgument(adapter)

	for _, operation := range operations {
		switch operation.Kind {
		case RouteOperationAddRoute:
			args := []string{
				"interface",
				operation.Family,
				"delete",
				"route",
				"prefix=" + operation.Target,
				"interface=" + interfaceArg,
				"store=active",
			}
			rollback = append(rollback, RollbackStatus{
				Kind:          operation.Kind,
				Family:        operation.Family,
				Target:        operation.Target,
				Interface:     operation.Interface,
				InterfaceLUID: operation.InterfaceLUID,
				Command: CommandSpec{
					Executable: "netsh.exe",
					Args:       args,
				},
				State: RouteOperationPlanned,
				Note:  "Rollback scaffold for route deletion.",
			})
		case RouteOperationSetDNS:
			rollback = append(rollback, RollbackStatus{
				Kind:          operation.Kind,
				Target:        operation.Target,
				Interface:     operation.Interface,
				InterfaceLUID: operation.InterfaceLUID,
				Command: CommandSpec{
					Executable: "powershell.exe",
					Args: []string{
						"-NoProfile",
						"-NonInteractive",
						"-Command",
						"Set-DnsClientServerAddress",
						"-InterfaceAlias",
						adapter.Name,
						"-ServerAddresses",
						"${captured_dns_servers}",
					},
					Requires: []string{"captured_dns_servers"},
				},
				State: RouteOperationPlanned,
				Note:  "Rollback requires DNS server capture before apply.",
			})
		}
	}

	return rollback
}

func routeInterfaceArgument(adapter AdapterStatus) string {
	if name := strings.TrimSpace(adapter.Name); name != "" {
		return name
	}
	return "${adapter_alias}"
}

func trimStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out = append(out, value)
	}
	return out
}
