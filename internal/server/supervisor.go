package server

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"sync"
	"time"
)

// TenantProcess tracks a running tenant runtime process.
type TenantProcess struct {
	Tenant  *Tenant
	Cmd     *exec.Cmd
	Cancel  context.CancelFunc
	Started time.Time
	Status  string // starting / running / stopped / failed
}

// Supervisor manages tenant runtime processes.
type Supervisor struct {
	mu        sync.RWMutex
	processes map[string]*TenantProcess // keyed by tenant_id
	registry  *TenantRegistry
	binary    string // path to olcrtc binary
	dnsServer string
}

// NewSupervisor creates a process supervisor.
func NewSupervisor(registry *TenantRegistry, binary, dnsServer string) *Supervisor {
	return &Supervisor{
		processes: make(map[string]*TenantProcess),
		registry:  registry,
		binary:    binary,
		dnsServer: dnsServer,
	}
}

// StartTenant launches a runtime process for a tenant.
func (s *Supervisor) StartTenant(ctx context.Context, tenant *Tenant) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Kill any existing process for this tenant before starting new one
	if existing, ok := s.processes[tenant.TenantID]; ok {
		if existing.Status == "running" && existing.Cmd != nil && existing.Cmd.Process != nil {
			log.Printf("[SUPERVISOR] Killing old tenant %s (pid %d) before restart", tenant.TenantID, existing.Cmd.Process.Pid)
			existing.Cancel()
			existing.Cmd.Process.Kill()
			existing.Status = "stopped"
		}
	}

	// Build command args for tenant runtime process
	args := []string{
		"-mode", "srv",
		"--discover",
		"-debug",
		"-socks-port", fmt.Sprintf("%d", tenant.SOCKSPort),
		"-api-port", fmt.Sprintf("%d", tenant.APIPort),
		"-dns", s.dnsServer,
	}

	procCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(procCtx, s.binary, args...)

	// Set environment with tenant-specific secrets
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("OLCRTC_MASTER_SECRET=%s", tenant.Secret),
	)
	if tenant.OAuthToken != "" {
		cmd.Env = append(cmd.Env, fmt.Sprintf("OLCRTC_OAUTH_TOKEN=%s", tenant.OAuthToken))
	}

	// Redirect output to log
	cmd.Stdout = &tenantLogger{prefix: fmt.Sprintf("[TENANT:%s]", tenant.TenantID)}
	cmd.Stderr = cmd.Stdout

	proc := &TenantProcess{
		Tenant:  tenant,
		Cmd:     cmd,
		Cancel:  cancel,
		Started: time.Now(),
		Status:  "starting",
	}
	s.processes[tenant.TenantID] = proc

	// Start the process
	if err := cmd.Start(); err != nil {
		proc.Status = "failed"
		cancel()
		return fmt.Errorf("start tenant %s: %w", tenant.TenantID, err)
	}

	proc.Status = "running"
	tenant.Status = "active"
	log.Printf("[SUPERVISOR] Started tenant %s (pid %d, SOCKS port %d, API port %d)",
		tenant.TenantID, cmd.Process.Pid, tenant.SOCKSPort, tenant.APIPort)

	// Monitor process in background
	go func() {
		err := cmd.Wait()
		s.mu.Lock()
		if p, ok := s.processes[tenant.TenantID]; ok && p == proc {
			p.Status = "stopped"
			tenant.Status = "idle"
			if err != nil {
				p.Status = "failed"
				log.Printf("[SUPERVISOR] Tenant %s process exited with error: %v", tenant.TenantID, err)
			} else {
				log.Printf("[SUPERVISOR] Tenant %s process exited cleanly", tenant.TenantID)
			}
		}
		s.mu.Unlock()
	}()

	return nil
}

// StopTenant stops a tenant runtime process.
func (s *Supervisor) StopTenant(tenantID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	proc, ok := s.processes[tenantID]
	if !ok || proc.Status != "running" {
		return
	}

	log.Printf("[SUPERVISOR] Stopping tenant %s", tenantID)
	proc.Cancel()
	proc.Status = "stopped"
	proc.Tenant.Status = "idle"
}

// RestartTenant stops and restarts a tenant process.
func (s *Supervisor) RestartTenant(ctx context.Context, tenant *Tenant) error {
	s.StopTenant(tenant.TenantID)
	time.Sleep(1 * time.Second) // allow process to exit
	return s.StartTenant(ctx, tenant)
}

// GetProcess returns the process info for a tenant.
func (s *Supervisor) GetProcess(tenantID string) *TenantProcess {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.processes[tenantID]
}

// StopAll stops all tenant processes.
func (s *Supervisor) StopAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, proc := range s.processes {
		if proc.Status == "running" {
			log.Printf("[SUPERVISOR] Stopping tenant %s", id)
			proc.Cancel()
			proc.Status = "stopped"
		}
	}
}

// tenantLogger prefixes log output with tenant ID.
type tenantLogger struct {
	prefix string
}

func (l *tenantLogger) Write(p []byte) (int, error) {
	log.Printf("%s %s", l.prefix, string(p))
	return len(p), nil
}
