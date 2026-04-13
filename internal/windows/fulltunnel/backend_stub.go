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

func (unsupportedRouteBackend) ApplyRoutes(_ context.Context, adapter AdapterStatus, plan RoutePlan) (RouteHandle, error) {
	handle := newWindowsRouteHandle(nil, nil, adapter, plan)
	handle.markFailed(fmt.Errorf("%w: Windows full-tunnel route backend is unavailable on %s", ErrNotImplemented, runtime.GOOS))
	return handle, fmt.Errorf("%w: Windows full-tunnel route backend is unavailable on %s", ErrNotImplemented, runtime.GOOS)
}
