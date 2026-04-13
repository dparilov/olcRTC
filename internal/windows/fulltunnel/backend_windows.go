//go:build windows

package fulltunnel

import (
	"context"
	"fmt"
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

func (b wintunAdapterBackend) EnsureAdapter(_ context.Context, cfg AdapterConfig) (AdapterHandle, error) {
	b.log.Printf("full-tunnel: Wintun scaffold placeholder adapter=%q mtu=%d addresses=%v", cfg.Name, cfg.MTU, cfg.Addresses)
	return nil, fmt.Errorf("%w: Wintun adapter provisioning is not implemented", ErrNotImplemented)
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
