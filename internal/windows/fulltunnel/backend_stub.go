//go:build !windows

package fulltunnel

import (
	"context"
	"fmt"
	"runtime"
)

func newPlatformAdapterBackend(_ Logger) AdapterBackend {
	return unsupportedAdapterBackend{}
}

func newPlatformRouteBackend(_ Logger) RouteBackend {
	return unsupportedRouteBackend{}
}

type unsupportedAdapterBackend struct{}

func (unsupportedAdapterBackend) EnsureAdapter(_ context.Context, _ AdapterConfig) (AdapterHandle, error) {
	return nil, fmt.Errorf("%w: Windows full-tunnel adapter backend is unavailable on %s", ErrNotImplemented, runtime.GOOS)
}

type unsupportedRouteBackend struct{}

func (unsupportedRouteBackend) ApplyRoutes(_ context.Context, plan RoutePlan) (RouteStatus, error) {
	return RouteStatus{
		Mode:            plan.Mode,
		IPv4CIDRs:       append([]string(nil), plan.IPv4CIDRs...),
		IPv6CIDRs:       append([]string(nil), plan.IPv6CIDRs...),
		DNSServers:      append([]string(nil), plan.DNSServers...),
		AllowRollback:   plan.AllowRollback,
		RequiresDefault: plan.RequiresDefault,
	}, fmt.Errorf("%w: Windows full-tunnel route backend is unavailable on %s", ErrNotImplemented, runtime.GOOS)
}

func (unsupportedRouteBackend) Cleanup(context.Context) error {
	return nil
}
