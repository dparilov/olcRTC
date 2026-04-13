package fulltunnel

import (
	"context"
	"errors"
	"testing"
)

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
		status: RouteStatus{
			Mode:            RouteModeFullTunnel,
			IPv4CIDRs:       []string{"0.0.0.0/0"},
			DNSServers:      []string{"1.1.1.1"},
			AllowRollback:   true,
			RequiresDefault: true,
		},
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

	if err := manager.Stop(context.Background()); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
	if !routes.cleaned {
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
	routes := &fakeRouteBackend{applyErr: errors.New("route apply failed")}
	manager := newManager(nil, adapter, routes)

	err := manager.Start(context.Background(), StartRequest{
		Adapter: AdapterConfig{Name: "olcRTC Full Tunnel", Provider: "wintun"},
		Routes:  RoutePlan{Mode: RouteModeFullTunnel, AllowRollback: true},
	})
	if err == nil {
		t.Fatal("Start error = nil, want failure")
	}
	if !routes.cleaned {
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
	status   RouteStatus
	applyErr error
	cleanErr error
	cleaned  bool
}

func (b *fakeRouteBackend) ApplyRoutes(context.Context, RoutePlan) (RouteStatus, error) {
	if b.applyErr != nil {
		return RouteStatus{}, b.applyErr
	}
	return b.status, nil
}

func (b *fakeRouteBackend) Cleanup(context.Context) error {
	b.cleaned = true
	return b.cleanErr
}
