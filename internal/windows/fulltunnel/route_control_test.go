package fulltunnel

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestNewWindowsRouteHandleBuildsWindowsCommandScaffold(t *testing.T) {
	handle := newWindowsRouteHandle(
		nil,
		nil,
		AdapterStatus{
			Name:       "olcRTC Full Tunnel",
			Provider:   "wintun",
			NativeLUID: 42,
		},
		RoutePlan{
			Mode:            RouteModeFullTunnel,
			IPv4CIDRs:       []string{"0.0.0.0/0"},
			IPv6CIDRs:       []string{"::/0"},
			DNSServers:      []string{"1.1.1.1", "2606:4700:4700::1111"},
			AllowRollback:   true,
			RequiresDefault: true,
		},
	)

	status := handle.Status()
	if status.State != RouteStatePlanned {
		t.Fatalf("route state = %q, want %q", status.State, RouteStatePlanned)
	}
	if len(status.Operations) != 3 {
		t.Fatalf("operations = %d, want 3", len(status.Operations))
	}
	if len(status.Rollback) != 3 {
		t.Fatalf("rollback = %d, want 3", len(status.Rollback))
	}

	if got := status.Operations[0].Command.Executable; got != "netsh.exe" {
		t.Fatalf("ipv4 route executable = %q, want netsh.exe", got)
	}
	if got := status.Operations[1].Command.Executable; got != "netsh.exe" {
		t.Fatalf("ipv6 route executable = %q, want netsh.exe", got)
	}
	if got := status.Operations[2].Command.Executable; got != "powershell.exe" {
		t.Fatalf("dns executable = %q, want powershell.exe", got)
	}
	if len(status.Rollback[2].Command.Requires) != 1 || status.Rollback[2].Command.Requires[0] != "captured_dns_servers" {
		t.Fatalf("dns rollback requires = %v, want [captured_dns_servers]", status.Rollback[2].Command.Requires)
	}
	if !status.CleanupRequired {
		t.Fatal("cleanup should be required when rollback is enabled")
	}
}

func TestRouteHandleApplyAndCleanupDryRun(t *testing.T) {
	handle := newWindowsRouteHandle(
		nil,
		dryRunCommandRunner{},
		AdapterStatus{Name: "olcRTC Full Tunnel", NativeLUID: 42},
		RoutePlan{
			Mode:          RouteModeFullTunnel,
			IPv4CIDRs:     []string{"0.0.0.0/0"},
			DNSServers:    []string{"1.1.1.1"},
			AllowRollback: true,
		},
	)

	if err := handle.Apply(context.Background()); err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}

	status := handle.Status()
	if status.State != RouteStateApplied {
		t.Fatalf("state after apply = %q, want %q", status.State, RouteStateApplied)
	}
	for idx, operation := range status.Operations {
		if operation.State != RouteOperationApplied {
			t.Fatalf("operation %d state = %q, want %q", idx, operation.State, RouteOperationApplied)
		}
		if !operation.DryRun {
			t.Fatalf("operation %d should be marked dry-run", idx)
		}
		if operation.Attempts != 1 {
			t.Fatalf("operation %d attempts = %d, want 1", idx, operation.Attempts)
		}
	}

	if err := handle.Cleanup(context.Background()); err != nil {
		t.Fatalf("Cleanup returned error: %v", err)
	}

	status = handle.Status()
	if status.State != RouteStateRolledBack {
		t.Fatalf("state after cleanup = %q, want %q", status.State, RouteStateRolledBack)
	}
	for idx, rollback := range status.Rollback {
		if rollback.State != RouteOperationCleaned {
			t.Fatalf("rollback %d state = %q, want %q", idx, rollback.State, RouteOperationCleaned)
		}
		if rollback.Attempts != 1 {
			t.Fatalf("rollback %d attempts = %d, want 1", idx, rollback.Attempts)
		}
	}
}

func TestRouteHandleApplyFailureTriggersReverseRollback(t *testing.T) {
	runner := &recordingCommandRunner{
		failures: map[string]error{
			"apply:powershell.exe:Set-DnsClientServerAddress": errors.New("dns apply failed"),
		},
	}
	handle := newWindowsRouteHandle(
		nil,
		runner,
		AdapterStatus{Name: "olcRTC Full Tunnel", NativeLUID: 42},
		RoutePlan{
			Mode:          RouteModeFullTunnel,
			IPv4CIDRs:     []string{"0.0.0.0/0"},
			DNSServers:    []string{"1.1.1.1"},
			AllowRollback: true,
		},
	)

	err := handle.Apply(context.Background())
	if err == nil {
		t.Fatal("Apply error = nil, want failure")
	}

	status := handle.Status()
	if status.State != RouteStateFailed {
		t.Fatalf("state = %q, want %q", status.State, RouteStateFailed)
	}
	if status.Operations[1].State != RouteOperationFailed {
		t.Fatalf("dns operation state = %q, want %q", status.Operations[1].State, RouteOperationFailed)
	}
	if status.Rollback[1].State != RouteOperationCleaned {
		t.Fatalf("dns rollback state = %q, want %q", status.Rollback[1].State, RouteOperationCleaned)
	}
	if status.Rollback[0].State != RouteOperationCleaned {
		t.Fatalf("route rollback state = %q, want %q", status.Rollback[0].State, RouteOperationCleaned)
	}

	wantOrder := []string{
		"apply:netsh.exe:prefix=0.0.0.0/0",
		"apply:powershell.exe:Set-DnsClientServerAddress",
		"rollback:powershell.exe:Set-DnsClientServerAddress",
		"rollback:netsh.exe:prefix=0.0.0.0/0",
	}
	for idx, want := range wantOrder {
		if idx >= len(runner.calls) || runner.calls[idx] != want {
			t.Fatalf("call %d = %q, want %q (all calls=%v)", idx, runner.calls[idx], want, runner.calls)
		}
	}
}

func TestRouteHandleRollbackFailurePropagates(t *testing.T) {
	runner := &recordingCommandRunner{
		failures: map[string]error{
			"rollback:netsh.exe:prefix=0.0.0.0/0": errors.New("route delete failed"),
		},
	}
	handle := newWindowsRouteHandle(
		nil,
		runner,
		AdapterStatus{Name: "olcRTC Full Tunnel", NativeLUID: 42},
		RoutePlan{
			Mode:          RouteModeFullTunnel,
			IPv4CIDRs:     []string{"0.0.0.0/0"},
			AllowRollback: true,
		},
	)

	if err := handle.Apply(context.Background()); err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	err := handle.Cleanup(context.Background())
	if err == nil {
		t.Fatal("Cleanup error = nil, want rollback failure")
	}

	status := handle.Status()
	if status.State != RouteStateFailed {
		t.Fatalf("state = %q, want %q", status.State, RouteStateFailed)
	}
	if status.Rollback[0].State != RouteOperationFailed {
		t.Fatalf("rollback state = %q, want %q", status.Rollback[0].State, RouteOperationFailed)
	}
	if status.LastError == "" {
		t.Fatal("last error should be populated")
	}
}

type recordingCommandRunner struct {
	failures map[string]error
	calls    []string
}

func (r *recordingCommandRunner) Run(_ context.Context, exec CommandExecution) (CommandResult, error) {
	key := fmt.Sprintf("%s:%s:%s", exec.Phase, exec.Spec.Executable, commandTraceTarget(exec.Spec.Args))
	r.calls = append(r.calls, key)
	if err, ok := r.failures[key]; ok {
		return CommandResult{
			ExitCode: 1,
			Stderr:   err.Error(),
		}, err
	}
	return CommandResult{
		ExitCode: 0,
		Stdout:   key,
	}, nil
}

func commandTraceTarget(args []string) string {
	for _, arg := range args {
		if strings.HasPrefix(arg, "prefix=") {
			return arg
		}
		if arg == "Set-DnsClientServerAddress" {
			return arg
		}
	}
	return ""
}
