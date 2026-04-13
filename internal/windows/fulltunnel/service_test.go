package fulltunnel

import (
	"context"
	"errors"
	"testing"
)

func TestNormalizeAdapterProvider(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "default empty", input: "", expected: wintunProvider},
		{name: "trim and lowercase", input: "  WinTun  ", expected: wintunProvider},
		{name: "other provider", input: "wireguard", expected: "wireguard"},
	}

	for _, test := range tests {
		if actual := normalizeAdapterProvider(test.input); actual != test.expected {
			t.Fatalf("%s: normalizeAdapterProvider(%q) = %q, want %q", test.name, test.input, actual, test.expected)
		}
	}
}

func TestManagerStartStopSuccess(t *testing.T) {
	adapter := &fakeAdapterBackend{
		handle: &fakeAdapterHandle{
			status: AdapterStatus{
				Name:      "olcRTC Full Tunnel",
				Provider:  "wintun",
				Addresses: []string{"10.250.0.2/32"},
			},
		},
	}
	routes := &fakeRouteBackend{
		handle: &fakeRouteHandle{status: RouteStatus{
			State:           RouteStateApplied,
			Mode:            RouteModeFullTunnel,
			IPv4CIDRs:       []string{"0.0.0.0/0"},
			DNSServers:      []string{"1.1.1.1"},
			Applied:         true,
			AllowRollback:   true,
			RequiresDefault: true,
			Operations: []RouteOperationStatus{
				{Kind: RouteOperationAddRoute, Family: "ipv4", Target: "0.0.0.0/0", State: RouteOperationApplied},
			},
			Rollback: []RollbackStatus{
				{Kind: RouteOperationAddRoute, Family: "ipv4", Target: "0.0.0.0/0", State: RouteOperationCleanupPending},
			},
			CleanupRequired: true,
		}},
	}
	manager := newManager(nil, adapter, routes)

	err := manager.Start(context.Background(), StartRequest{
		Adapter: AdapterConfig{
			Name:      "olcRTC Full Tunnel",
			Provider:  "wintun",
			Addresses: []string{"10.250.0.2/32"},
		},
		Routes: RoutePlan{
			Mode:            RouteModeFullTunnel,
			IPv4CIDRs:       []string{"0.0.0.0/0"},
			DNSServers:      []string{"1.1.1.1"},
			AllowRollback:   true,
			RequiresDefault: true,
		},
	})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	snapshot := manager.Snapshot()
	if snapshot.Stage != StageReady {
		t.Fatalf("Stage = %q, want %q", snapshot.Stage, StageReady)
	}
	if !snapshot.Adapter.Ready {
		t.Fatal("adapter should be marked ready")
	}
	if !snapshot.Routes.Applied {
		t.Fatal("routes should be marked applied")
	}
	if snapshot.Routes.State != RouteStateApplied {
		t.Fatalf("route state = %q, want %q", snapshot.Routes.State, RouteStateApplied)
	}

	if err := manager.Stop(context.Background()); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
	if !routes.handle.cleaned {
		t.Fatal("route cleanup was not called")
	}
	if !adapter.handle.closed {
		t.Fatal("adapter close was not called")
	}
}

func TestManagerStartRouteFailureRollsBackAdapter(t *testing.T) {
	adapter := &fakeAdapterBackend{
		handle: &fakeAdapterHandle{
			status: AdapterStatus{Name: "olcRTC Full Tunnel", Provider: "wintun"},
		},
	}
	routes := &fakeRouteBackend{
		handle: &fakeRouteHandle{
			status: RouteStatus{
				State:         RouteStatePlanned,
				Mode:          RouteModeFullTunnel,
				IPv4CIDRs:     []string{"0.0.0.0/0"},
				DNSServers:    []string{"1.1.1.1"},
				AllowRollback: true,
				Operations: []RouteOperationStatus{
					{
						Kind:      RouteOperationAddRoute,
						Family:    "ipv4",
						Target:    "0.0.0.0/0",
						State:     RouteOperationPlanned,
						Command:   CommandSpec{Executable: "netsh.exe", Args: []string{"interface", "ipv4", "add", "route"}},
						Interface: "olcRTC Full Tunnel",
					},
					{
						Kind:      RouteOperationSetDNS,
						Target:    "1.1.1.1",
						State:     RouteOperationPlanned,
						Command:   CommandSpec{Executable: "powershell.exe", Args: []string{"Set-DnsClientServerAddress"}},
						Interface: "olcRTC Full Tunnel",
					},
				},
				Rollback: []RollbackStatus{
					{
						Kind:      RouteOperationAddRoute,
						Family:    "ipv4",
						Target:    "0.0.0.0/0",
						State:     RouteOperationPlanned,
						Command:   CommandSpec{Executable: "netsh.exe", Args: []string{"interface", "ipv4", "delete", "route"}},
						Interface: "olcRTC Full Tunnel",
					},
				},
				CleanupRequired: true,
			},
		},
		applyErr: errors.New("route apply failed"),
	}
	manager := newManager(nil, adapter, routes)

	err := manager.Start(context.Background(), StartRequest{
		Adapter: AdapterConfig{Name: "olcRTC Full Tunnel", Provider: "wintun"},
		Routes:  RoutePlan{Mode: RouteModeFullTunnel, AllowRollback: true},
	})
	if err == nil {
		t.Fatal("Start error = nil, want failure")
	}
	if !routes.handle.cleaned {
		t.Fatal("route cleanup was not called")
	}
	if !adapter.handle.closed {
		t.Fatal("adapter close was not called after route failure")
	}

	snapshot := manager.Snapshot()
	if snapshot.Stage != StageFailed {
		t.Fatalf("Stage = %q, want %q", snapshot.Stage, StageFailed)
	}
	if snapshot.LastError == "" {
		t.Fatal("LastError should be populated")
	}
	if len(snapshot.Routes.Operations) != 2 {
		t.Fatalf("route operations = %d, want 2", len(snapshot.Routes.Operations))
	}
	if snapshot.Routes.Operations[0].Command.Executable != "netsh.exe" {
		t.Fatalf("first route command executable = %q, want netsh.exe", snapshot.Routes.Operations[0].Command.Executable)
	}
	if snapshot.Routes.State != RouteStateFailed {
		t.Fatalf("route state = %q, want %q", snapshot.Routes.State, RouteStateFailed)
	}
	if snapshot.Routes.LastError == "" {
		t.Fatal("route last error should be populated")
	}
}

type fakeAdapterBackend struct {
	handle *fakeAdapterHandle
	err    error
}

func (b *fakeAdapterBackend) EnsureAdapter(context.Context, AdapterConfig) (AdapterHandle, error) {
	if b.err != nil {
		return nil, b.err
	}
	return b.handle, nil
}

type fakeAdapterHandle struct {
	status AdapterStatus
	closed bool
	err    error
}

func (h *fakeAdapterHandle) Status() AdapterStatus {
	return h.status
}

func (h *fakeAdapterHandle) Close(context.Context) error {
	h.closed = true
	return h.err
}

type fakeRouteBackend struct {
	handle   *fakeRouteHandle
	applyErr error
}

func (b *fakeRouteBackend) ApplyRoutes(context.Context, AdapterStatus, RoutePlan) (RouteHandle, error) {
	if b.applyErr != nil {
		return b.handle, b.applyErr
	}
	return b.handle, nil
}

type fakeRouteHandle struct {
	status  RouteStatus
	cleaned bool
	err     error
}

func (h *fakeRouteHandle) Status() RouteStatus {
	return h.status
}

func (h *fakeRouteHandle) Cleanup(context.Context) error {
	h.cleaned = true
	h.status.State = RouteStateCleaned
	h.status.Applied = false
	h.status.CleanupRequired = false
	return h.err
}
