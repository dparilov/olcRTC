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

// CommandSpec models a Windows-facing command invocation without claiming that
// the package already executes it.
type CommandSpec struct {
	Executable string
	Args       []string
	Requires   []string
}

// CommandPhase distinguishes forward execution from rollback work.
type CommandPhase string

const (
	CommandPhaseApply    CommandPhase = "apply"
	CommandPhaseRollback CommandPhase = "rollback"
)

// CommandResult summarizes one command attempt.
type CommandResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
	DryRun   bool
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
	Name       string
	Provider   string
	Addresses  []string
	Existing   bool
	NativeLUID uint64
	Ready      bool
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

// RouteState summarizes where route control sits in its own lifecycle.
type RouteState string

const (
	RouteStateIdle            RouteState = "idle"
	RouteStatePlanned         RouteState = "planned"
	RouteStateApplying        RouteState = "applying"
	RouteStateApplied         RouteState = "applied"
	RouteStateRollbackPending RouteState = "rollback_pending"
	RouteStateRolledBack      RouteState = "rolled_back"
	RouteStateCleaned         RouteState = "cleaned"
	RouteStateFailed          RouteState = "failed"
)

// RouteOperationKind describes the Windows control action the scaffold tracks.
type RouteOperationKind string

const (
	RouteOperationAddRoute RouteOperationKind = "add_route"
	RouteOperationSetDNS   RouteOperationKind = "set_dns"
)

// RouteOperationState tracks the state of an individual Windows control step.
type RouteOperationState string

const (
	RouteOperationPlanned        RouteOperationState = "planned"
	RouteOperationApplying       RouteOperationState = "applying"
	RouteOperationApplied        RouteOperationState = "applied"
	RouteOperationCleanupPending RouteOperationState = "cleanup_pending"
	RouteOperationCleanupRunning RouteOperationState = "cleanup_running"
	RouteOperationCleaned        RouteOperationState = "cleaned"
	RouteOperationSkipped        RouteOperationState = "skipped"
	RouteOperationFailed         RouteOperationState = "failed"
)

// RouteOperationStatus captures one Windows route or DNS action plus the
// command shape that will eventually execute it.
type RouteOperationStatus struct {
	Kind          RouteOperationKind
	Family        string
	Target        string
	Via           string
	Metric        int
	Interface     string
	InterfaceLUID uint64
	Command       CommandSpec
	State         RouteOperationState
	Attempts      int
	LastExitCode  int
	StartedAt     time.Time
	CompletedAt   time.Time
	Stdout        string
	Stderr        string
	LastError     string
	DryRun        bool
	Note          string
}

// RollbackStatus captures the corresponding Windows cleanup action.
type RollbackStatus struct {
	Kind          RouteOperationKind
	Family        string
	Target        string
	Interface     string
	InterfaceLUID uint64
	Command       CommandSpec
	State         RouteOperationState
	Attempts      int
	LastExitCode  int
	StartedAt     time.Time
	CompletedAt   time.Time
	Stdout        string
	Stderr        string
	LastError     string
	DryRun        bool
	Note          string
}

// RouteStatus is safe for UI/log consumption.
type RouteStatus struct {
	State           RouteState
	Mode            RouteMode
	IPv4CIDRs       []string
	IPv6CIDRs       []string
	DNSServers      []string
	Applied         bool
	AllowRollback   bool
	RequiresDefault bool
	Operations      []RouteOperationStatus
	Rollback        []RollbackStatus
	CleanupRequired bool
	LastError       string
}

// StartRequest groups the intended adapter and route work for a future
// full-tunnel session. It deliberately excludes packet transport details.
type StartRequest struct {
	Adapter AdapterConfig
	Routes  RoutePlan
}
