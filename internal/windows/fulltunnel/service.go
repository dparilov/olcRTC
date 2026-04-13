package fulltunnel

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"
)

var (
	// ErrNotImplemented is returned by placeholder backends that describe a
	// future Windows integration point but do not yet perform system changes.
	ErrNotImplemented = errors.New("full-tunnel scaffold placeholder")

	// ErrUnsupportedAdapterProvider is returned when the requested adapter
	// provider does not match the currently implemented Windows backend.
	ErrUnsupportedAdapterProvider = errors.New("unsupported tunnel adapter provider")

	// ErrWintunUnavailable is returned when the Wintun DLL or required exports
	// are not available on the host.
	ErrWintunUnavailable = errors.New("wintun integration is unavailable")

	// ErrAlreadyStarted is returned when Start is called while the lifecycle is
	// already active.
	ErrAlreadyStarted = errors.New("full-tunnel lifecycle already started")
)

// AdapterHandle tracks a provisioned adapter and exposes cleanup.
type AdapterHandle interface {
	Status() AdapterStatus
	Close(context.Context) error
}

// AdapterBackend provisions the Windows tunnel adapter.
type AdapterBackend interface {
	EnsureAdapter(context.Context, AdapterConfig) (AdapterHandle, error)
}

// RouteBackend applies and rolls back Windows routes.
type RouteBackend interface {
	ApplyRoutes(context.Context, RoutePlan) (RouteStatus, error)
	Cleanup(context.Context) error
}

// Manager coordinates the future Windows adapter and route lifecycle.
type Manager struct {
	log Logger

	adapter AdapterBackend
	routes  RouteBackend

	mu            sync.Mutex
	started       bool
	adapterHandle AdapterHandle
	snapshot      Snapshot
}

// NewManager creates a scaffold wired to the platform defaults.
func NewManager(logger Logger) *Manager {
	return newManager(logger, newPlatformAdapterBackend(logger), newPlatformRouteBackend(logger))
}

func newManager(logger Logger, adapter AdapterBackend, routes RouteBackend) *Manager {
	if logger == nil {
		logger = stdLogger{}
	}

	return &Manager{
		log:     logger,
		adapter: adapter,
		routes:  routes,
		snapshot: Snapshot{
			Stage:         StageIdle,
			Message:       "Windows full-tunnel scaffold is idle",
			AdminRequired: true,
			UpdatedAt:     time.Now(),
		},
	}
}

// Snapshot returns the latest lifecycle state for UI/log consumption.
func (m *Manager) Snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return cloneSnapshot(m.snapshot)
}

// Start advances the lifecycle through adapter and route setup. Current
// placeholder backends return explicit not-implemented errors while preserving
// the real control flow and rollback structure.
func (m *Manager) Start(ctx context.Context, req StartRequest) error {
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return ErrAlreadyStarted
	}
	m.started = true
	m.snapshot = Snapshot{
		Stage:         StageStarting,
		Message:       "Starting Windows full-tunnel scaffold",
		AdminRequired: true,
		Adapter: AdapterStatus{
			Name:      req.Adapter.Name,
			Provider:  req.Adapter.Provider,
			Addresses: append([]string(nil), req.Adapter.Addresses...),
		},
		Routes: RouteStatus{
			Mode:            req.Routes.Mode,
			IPv4CIDRs:       append([]string(nil), req.Routes.IPv4CIDRs...),
			IPv6CIDRs:       append([]string(nil), req.Routes.IPv6CIDRs...),
			DNSServers:      append([]string(nil), req.Routes.DNSServers...),
			AllowRollback:   req.Routes.AllowRollback,
			RequiresDefault: req.Routes.RequiresDefault,
		},
		UpdatedAt: time.Now(),
	}
	m.mu.Unlock()

	m.log.Printf("full-tunnel: lifecycle start requested adapter=%q provider=%q mode=%q", req.Adapter.Name, req.Adapter.Provider, req.Routes.Mode)
	m.setStage(StageAdapterSetup, "Preparing Windows tunnel adapter")

	handle, err := m.adapter.EnsureAdapter(ctx, req.Adapter)
	if err != nil {
		return m.failStart("adapter setup failed", err)
	}

	m.mu.Lock()
	m.adapterHandle = handle
	m.snapshot.Adapter = cloneAdapterStatus(handle.Status())
	m.snapshot.Adapter.Ready = true
	m.snapshot.UpdatedAt = time.Now()
	m.mu.Unlock()

	m.setStage(StageAdapterReady, "Windows tunnel adapter placeholder is ready for route work")
	m.setStage(StageRouteSetup, "Preparing Windows route changes")

	routeStatus, err := m.routes.ApplyRoutes(ctx, req.Routes)
	if err != nil {
		return m.failAfterAdapter(ctx, "route setup failed", err)
	}

	m.mu.Lock()
	m.snapshot.Routes = cloneRouteStatus(routeStatus)
	m.snapshot.Routes.Applied = true
	m.snapshot.UpdatedAt = time.Now()
	m.mu.Unlock()

	m.setStage(StageReady, "Windows full-tunnel scaffold reached route-ready state")
	return nil
}

// Stop rolls back route state first, then closes the adapter handle.
func (m *Manager) Stop(ctx context.Context) error {
	m.mu.Lock()
	if !m.started {
		m.snapshot.Stage = StageStopped
		m.snapshot.Message = "Windows full-tunnel scaffold is already stopped"
		m.snapshot.UpdatedAt = time.Now()
		m.mu.Unlock()
		return nil
	}
	m.snapshot.Stage = StageStopping
	m.snapshot.Message = "Stopping Windows full-tunnel scaffold"
	m.snapshot.UpdatedAt = time.Now()
	handle := m.adapterHandle
	m.mu.Unlock()

	m.log.Printf("full-tunnel: stop requested")

	var errs []error
	if err := m.routes.Cleanup(ctx); err != nil {
		errs = append(errs, fmt.Errorf("route cleanup: %w", err))
	}
	if handle != nil {
		if err := handle.Close(ctx); err != nil {
			errs = append(errs, fmt.Errorf("adapter cleanup: %w", err))
		}
	}

	m.mu.Lock()
	m.started = false
	m.adapterHandle = nil
	m.snapshot.Stage = StageStopped
	m.snapshot.Message = "Windows full-tunnel scaffold stopped"
	m.snapshot.LastError = errorString(errors.Join(errs...))
	m.snapshot.Adapter.Ready = false
	m.snapshot.Routes.Applied = false
	m.snapshot.UpdatedAt = time.Now()
	m.mu.Unlock()

	return errors.Join(errs...)
}

func (m *Manager) failStart(message string, err error) error {
	wrapped := fmt.Errorf("%s: %w", message, err)

	m.mu.Lock()
	m.started = false
	m.adapterHandle = nil
	m.snapshot.Stage = StageFailed
	m.snapshot.Message = message
	m.snapshot.LastError = wrapped.Error()
	m.snapshot.UpdatedAt = time.Now()
	m.mu.Unlock()

	m.log.Printf("full-tunnel: %s: %v", message, err)
	return wrapped
}

func (m *Manager) failAfterAdapter(ctx context.Context, message string, err error) error {
	handle := m.currentHandle()

	var errs []error
	errs = append(errs, fmt.Errorf("%s: %w", message, err))

	if cleanupErr := m.routes.Cleanup(ctx); cleanupErr != nil {
		errs = append(errs, fmt.Errorf("route cleanup after failure: %w", cleanupErr))
	}
	if handle != nil {
		if closeErr := handle.Close(ctx); closeErr != nil {
			errs = append(errs, fmt.Errorf("adapter cleanup after failure: %w", closeErr))
		}
	}

	joined := errors.Join(errs...)

	m.mu.Lock()
	m.started = false
	m.adapterHandle = nil
	m.snapshot.Stage = StageFailed
	m.snapshot.Message = message
	m.snapshot.LastError = errorString(joined)
	m.snapshot.Adapter.Ready = false
	m.snapshot.Routes.Applied = false
	m.snapshot.UpdatedAt = time.Now()
	m.mu.Unlock()

	m.log.Printf("full-tunnel: %s: %v", message, joined)
	return joined
}

func (m *Manager) currentHandle() AdapterHandle {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.adapterHandle
}

func (m *Manager) setStage(stage Stage, message string) {
	m.mu.Lock()
	m.snapshot.Stage = stage
	m.snapshot.Message = message
	m.snapshot.UpdatedAt = time.Now()
	m.mu.Unlock()
}

func cloneSnapshot(src Snapshot) Snapshot {
	dst := src
	dst.Adapter = cloneAdapterStatus(src.Adapter)
	dst.Routes = cloneRouteStatus(src.Routes)
	return dst
}

func cloneAdapterStatus(src AdapterStatus) AdapterStatus {
	dst := src
	dst.Addresses = append([]string(nil), src.Addresses...)
	return dst
}

func cloneRouteStatus(src RouteStatus) RouteStatus {
	dst := src
	dst.IPv4CIDRs = append([]string(nil), src.IPv4CIDRs...)
	dst.IPv6CIDRs = append([]string(nil), src.IPv6CIDRs...)
	dst.DNSServers = append([]string(nil), src.DNSServers...)
	return dst
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

type stdLogger struct{}

func (stdLogger) Printf(format string, v ...interface{}) {
	log.Printf(format, v...)
}
