package fulltunnel

import "testing"

func TestNewWindowsRouteHandleBuildsWindowsCommandScaffold(t *testing.T) {
	handle := newWindowsRouteHandle(
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
