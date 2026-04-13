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
	return windowsRouteBackend{
		log:    logger,
		runner: dryRunCommandRunner{log: logger},
	}
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
	log    Logger
	runner CommandRunner
}

func (b windowsRouteBackend) ApplyRoutes(ctx context.Context, adapter AdapterStatus, plan RoutePlan) (RouteHandle, error) {
	if b.runner == nil {
		b.runner = dryRunCommandRunner{log: b.log}
	}

	handle := newWindowsRouteHandle(b.log, b.runner, adapter, plan)
	status := handle.Status()
	b.log.Printf(
		"full-tunnel: Windows route runner planned mode=%q adapter=%q ipv4_ops=%d ipv6_ops=%d dns=%d rollback=%d default=%v",
		plan.Mode,
		adapter.Name,
		countRouteFamily(status.Operations, "ipv4"),
		countRouteFamily(status.Operations, "ipv6"),
		countOperationKind(status.Operations, RouteOperationSetDNS),
		len(status.Rollback),
		plan.RequiresDefault,
	)
	if err := handle.Apply(ctx); err != nil {
		return handle, err
	}
	return handle, nil
}

func countRouteFamily(operations []RouteOperationStatus, family string) int {
	count := 0
	for _, operation := range operations {
		if operation.Kind == RouteOperationAddRoute && operation.Family == family {
			count++
		}
	}
	return count
}

func countOperationKind(operations []RouteOperationStatus, kind RouteOperationKind) int {
	count := 0
	for _, operation := range operations {
		if operation.Kind == kind {
			count++
		}
	}
	return count
}
