package fulltunnel

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

const routeMetricDefault = 0

type routeHandle struct {
	runner   CommandRunner
	status   RouteStatus
	captured map[string]string
}

func newWindowsRouteHandle(logger Logger, runner CommandRunner, adapter AdapterStatus, plan RoutePlan) *routeHandle {
	if logger == nil {
		logger = stdLogger{}
	}
	if runner == nil {
		runner = dryRunCommandRunner{log: logger}
	}

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

	return &routeHandle{
		runner:   runner,
		status:   status,
		captured: make(map[string]string),
	}
}

func (h *routeHandle) Status() RouteStatus {
	if h == nil {
		return RouteStatus{}
	}
	return cloneRouteStatus(h.status)
}

func (h *routeHandle) Apply(ctx context.Context) error {
	if h == nil {
		return nil
	}

	h.status.State = RouteStateApplying
	h.status.LastError = ""

	for idx := range h.status.Operations {
		if err := ctx.Err(); err != nil {
			h.markFailed(err)
			return err
		}

		if err := h.captureRollbackState(h.status.Operations[idx]); err != nil {
			h.status.Operations[idx].State = RouteOperationFailed
			h.status.Operations[idx].LastError = err.Error()
			h.markFailed(err)
			return err
		}

		h.status.Operations[idx].State = RouteOperationApplying
		h.status.Operations[idx].StartedAt = time.Now()
		result, err := h.runner.Run(ctx, CommandExecution{
			Phase:        CommandPhaseApply,
			Spec:         h.status.Operations[idx].Command,
			Operation:    h.status.Operations[idx].Kind,
			Target:       h.status.Operations[idx].Target,
			Family:       h.status.Operations[idx].Family,
			Interface:    h.status.Operations[idx].Interface,
			Captured:     cloneCapturedValues(h.captured),
			OperationIdx: idx,
		})
		h.recordOperationResult(idx, result, err)
		if err != nil {
			rollbackErr := h.rollback(ctx)
			joined := errors.Join(err, rollbackErr)
			h.markFailed(joined)
			return joined
		}
	}

	h.markApplied()
	return nil
}

func (h *routeHandle) Cleanup(ctx context.Context) error {
	if h == nil {
		return nil
	}
	return h.rollback(ctx)
}

func (h *routeHandle) rollback(ctx context.Context) error {
	if h == nil {
		return nil
	}

	if len(h.status.Rollback) == 0 {
		h.status.Applied = false
		h.status.CleanupRequired = false
		if h.status.State != RouteStateFailed {
			h.status.State = RouteStateCleaned
		}
		return nil
	}

	h.status.State = RouteStateRollbackPending

	var errs []error
	for idx := len(h.status.Rollback) - 1; idx >= 0; idx-- {
		if !h.rollbackEligible(idx) {
			continue
		}
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)
			h.status.Rollback[idx].State = RouteOperationFailed
			h.status.Rollback[idx].LastError = err.Error()
			continue
		}

		resolved, err := resolveCommandSpec(h.status.Rollback[idx].Command, h.captured)
		if err != nil {
			errs = append(errs, err)
			h.status.Rollback[idx].State = RouteOperationFailed
			h.status.Rollback[idx].LastError = err.Error()
			continue
		}

		h.status.Rollback[idx].State = RouteOperationCleanupRunning
		h.status.Rollback[idx].StartedAt = time.Now()
		result, runErr := h.runner.Run(ctx, CommandExecution{
			Phase:        CommandPhaseRollback,
			Spec:         resolved,
			Operation:    h.status.Rollback[idx].Kind,
			Target:       h.status.Rollback[idx].Target,
			Family:       h.status.Rollback[idx].Family,
			Interface:    h.status.Rollback[idx].Interface,
			Captured:     cloneCapturedValues(h.captured),
			OperationIdx: idx,
		})
		h.recordRollbackResult(idx, result, runErr)
		if runErr != nil {
			errs = append(errs, runErr)
		}
	}

	h.status.Applied = false
	h.status.CleanupRequired = false

	joined := errors.Join(errs...)
	if joined != nil {
		h.status.State = RouteStateFailed
		h.status.LastError = joined.Error()
		return joined
	}

	h.status.State = RouteStateRolledBack
	h.status.LastError = ""
	return nil
}

func (h *routeHandle) rollbackEligible(idx int) bool {
	switch h.status.Rollback[idx].State {
	case RouteOperationPlanned, RouteOperationCleanupPending, RouteOperationFailed:
		return true
	default:
		return false
	}
}

func (h *routeHandle) captureRollbackState(operation RouteOperationStatus) error {
	if operation.Kind != RouteOperationSetDNS || !h.status.AllowRollback {
		return nil
	}
	if _, ok := h.captured["captured_dns_servers"]; ok {
		return nil
	}

	// Previous DNS resolver capture is still unfinished. Keep a placeholder so
	// rollback sequencing can be exercised without pretending the host changed.
	h.captured["captured_dns_servers"] = "dhcp"
	return nil
}

func (h *routeHandle) recordOperationResult(idx int, result CommandResult, err error) {
	status := &h.status.Operations[idx]
	status.Attempts++
	status.LastExitCode = result.ExitCode
	status.CompletedAt = time.Now()
	status.Stdout = result.Stdout
	status.Stderr = result.Stderr
	status.DryRun = result.DryRun
	status.LastError = errorString(err)
	if err != nil {
		status.State = RouteOperationFailed
		return
	}
	status.State = RouteOperationApplied
	if idx < len(h.status.Rollback) {
		h.status.Rollback[idx].State = RouteOperationCleanupPending
	}
}

func (h *routeHandle) recordRollbackResult(idx int, result CommandResult, err error) {
	status := &h.status.Rollback[idx]
	status.Attempts++
	status.LastExitCode = result.ExitCode
	status.CompletedAt = time.Now()
	status.Stdout = result.Stdout
	status.Stderr = result.Stderr
	status.DryRun = result.DryRun
	status.LastError = errorString(err)
	if err != nil {
		status.State = RouteOperationFailed
		return
	}
	status.State = RouteOperationCleaned
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
		if h.status.Operations[idx].State == RouteOperationPlanned {
			h.status.Operations[idx].State = RouteOperationApplied
		}
	}
	for idx := range h.status.Rollback {
		if h.status.Rollback[idx].State == RouteOperationPlanned {
			h.status.Rollback[idx].State = RouteOperationCleanupPending
		}
	}
}

func resolveCommandSpec(spec CommandSpec, captured map[string]string) (CommandSpec, error) {
	resolved := CommandSpec{
		Executable: spec.Executable,
		Args:       append([]string(nil), spec.Args...),
		Requires:   append([]string(nil), spec.Requires...),
	}

	for _, key := range resolved.Requires {
		value, ok := captured[key]
		if !ok {
			return CommandSpec{}, fmt.Errorf("missing rollback dependency %q", key)
		}
		for idx := range resolved.Args {
			resolved.Args[idx] = strings.ReplaceAll(resolved.Args[idx], "${"+key+"}", value)
		}
	}

	return resolved, nil
}

func cloneCapturedValues(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
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
			Note:  "Windows route execution is modeled through a runner scaffold; default backend is dry-run only.",
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
			Note:  "Windows route execution is modeled through a runner scaffold; default backend is dry-run only.",
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
			Note:  "DNS execution is modeled through a runner scaffold; previous resolver capture remains a placeholder.",
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
				Note:  "Rollback is sequenced in reverse apply order through the runner scaffold.",
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
				Note:  "Rollback depends on captured DNS state; the current scaffold injects a placeholder until live capture exists.",
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
