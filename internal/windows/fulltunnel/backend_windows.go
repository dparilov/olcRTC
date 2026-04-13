//go:build windows

package fulltunnel

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

func newPlatformAdapterBackend(logger Logger) AdapterBackend {
	return wintunAdapterBackend{log: logger}
}

func newPlatformRouteBackend(logger Logger) RouteBackend {
	return windowsRouteBackend{log: logger}
}

type wintunAdapterBackend struct {
	log Logger
}

func (b wintunAdapterBackend) EnsureAdapter(ctx context.Context, cfg AdapterConfig) (AdapterHandle, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	provider := normalizeAdapterProvider(cfg.Provider)
	if provider != wintunProvider {
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedAdapterProvider, cfg.Provider)
	}

	name := strings.TrimSpace(cfg.Name)
	if name == "" {
		return nil, errors.New("adapter name is required")
	}

	probe, err := ProbeWintun()
	if err != nil {
		return nil, err
	}

	dll, err := loadWintun()
	if err != nil {
		return nil, err
	}

	adapter, existing, err := dll.openOrCreateAdapter(name, defaultWintunTunnelType)
	if err != nil {
		return nil, err
	}

	status := AdapterStatus{
		Name:       name,
		Provider:   provider,
		Addresses:  append([]string(nil), cfg.Addresses...),
		Existing:   existing,
		NativeLUID: dll.adapterLUID(adapter),
	}
	handle := &wintunAdapterHandle{
		dll:     dll,
		adapter: adapter,
		status:  status,
	}

	if err := ctx.Err(); err != nil {
		handle.Close(context.Background())
		return nil, err
	}

	b.log.Printf(
		"full-tunnel: Wintun adapter ready name=%q existing=%t luid=%d mtu=%d addresses=%v exports=%v",
		status.Name,
		status.Existing,
		status.NativeLUID,
		cfg.MTU,
		cfg.Addresses,
		probe.Exports,
	)
	return handle, nil
}

type wintunAdapterHandle struct {
	dll     *wintunDLL
	adapter uintptr
	status  AdapterStatus
	closed  bool
}

func (h *wintunAdapterHandle) Status() AdapterStatus {
	return cloneAdapterStatus(h.status)
}

func (h *wintunAdapterHandle) Close(context.Context) error {
	if h == nil || h.closed || h.adapter == 0 {
		return nil
	}

	h.dll.closeAdapter(h.adapter)
	h.closed = true
	h.adapter = 0
	h.status.Ready = false
	return nil
}

type windowsRouteBackend struct {
	log Logger
}

func (b windowsRouteBackend) ApplyRoutes(_ context.Context, plan RoutePlan) (RouteStatus, error) {
	b.log.Printf("full-tunnel: Windows route scaffold placeholder mode=%q ipv4=%v ipv6=%v dns=%v rollback=%v default=%v", plan.Mode, plan.IPv4CIDRs, plan.IPv6CIDRs, plan.DNSServers, plan.AllowRollback, plan.RequiresDefault)
	return RouteStatus{
		Mode:            plan.Mode,
		IPv4CIDRs:       append([]string(nil), plan.IPv4CIDRs...),
		IPv6CIDRs:       append([]string(nil), plan.IPv6CIDRs...),
		DNSServers:      append([]string(nil), plan.DNSServers...),
		AllowRollback:   plan.AllowRollback,
		RequiresDefault: plan.RequiresDefault,
	}, fmt.Errorf("%w: Windows route application is not implemented", ErrNotImplemented)
}

func (b windowsRouteBackend) Cleanup(_ context.Context) error {
	b.log.Printf("full-tunnel: Windows route cleanup placeholder invoked")
	return nil
}
