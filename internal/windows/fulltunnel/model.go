package fulltunnel

import "time"

// Logger is the minimal logging contract used by the scaffold so the Windows
// client can wire package events into its own log surface later.
type Logger interface {
	Printf(format string, v ...interface{})
}

// Stage describes the current lifecycle phase of the full-tunnel scaffold.
type Stage string

const (
	StageIdle         Stage = "idle"
	StageStarting     Stage = "starting"
	StageAdapterSetup Stage = "adapter_setup"
	StageAdapterReady Stage = "adapter_ready"
	StageRouteSetup   Stage = "route_setup"
	StageReady        Stage = "ready"
	StageStopping     Stage = "stopping"
	StageStopped      Stage = "stopped"
	StageFailed       Stage = "failed"
)

// RouteMode describes the intended routing strategy once route application is
// implemented on Windows.
type RouteMode string

const (
	RouteModeFullTunnel  RouteMode = "full_tunnel"
	RouteModeSplitTunnel RouteMode = "split_tunnel"
)

// Snapshot is a UI-facing status model. The Windows desktop client can poll
// Snapshot and render adapter / route lifecycle state without coupling its UI
// to the backend implementation details.
type Snapshot struct {
	Stage         Stage
	Message       string
	LastError     string
	AdminRequired bool
	Adapter       AdapterStatus
	Routes        RouteStatus
	UpdatedAt     time.Time
}

// AdapterConfig declares the target adapter state once real Windows support is
// implemented.
type AdapterConfig struct {
	Name      string
	Provider  string
	MTU       int
	Addresses []string
}

// AdapterStatus is safe for UI/log consumption.
type AdapterStatus struct {
	Name      string
	Provider  string
	Addresses []string
	Ready     bool
}

// RoutePlan declares the desired route lifecycle once Windows route control is
// implemented.
type RoutePlan struct {
	Mode            RouteMode
	IPv4CIDRs       []string
	IPv6CIDRs       []string
	DNSServers      []string
	AllowRollback   bool
	RequiresDefault bool
}

// RouteStatus is safe for UI/log consumption.
type RouteStatus struct {
	Mode            RouteMode
	IPv4CIDRs       []string
	IPv6CIDRs       []string
	DNSServers      []string
	Applied         bool
	AllowRollback   bool
	RequiresDefault bool
}

// StartRequest groups the intended adapter and route work for a future
// full-tunnel session. It deliberately excludes packet transport details.
type StartRequest struct {
	Adapter AdapterConfig
	Routes  RoutePlan
}
