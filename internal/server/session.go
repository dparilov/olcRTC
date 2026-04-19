package server

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// Session represents an active runtime session for a tenant.
type Session struct {
	SessionID  string    `json:"session_id"`
	TenantID   string    `json:"tenant_id"`
	DeviceID   string    `json:"device_id"`
	SOCKSPort  int       `json:"socks_port"`
	APIPort    int       `json:"api_port"`
	RoomID     string    `json:"room_id,omitempty"`
	Status     string    `json:"status"` // active / terminating / terminated
	CreatedAt  time.Time `json:"created_at"`
	LastActive time.Time `json:"last_active"`
}

// SessionManager manages session lifecycle with TTL and takeover.
type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*Session // session_id -> session
	byTenant map[string]string   // tenant_id -> active session_id

	// Port pool
	portPool  []int
	portInUse map[int]bool

	supervisor *Supervisor
	registry   *TenantRegistry

	idleTTL time.Duration // default 30 min

	// Rate limiting: last session creation per tenant
	lastCreate map[string]time.Time
}

// NewSessionManager creates a session manager with a port pool.
func NewSessionManager(portStart, portEnd int, supervisor *Supervisor, registry *TenantRegistry) *SessionManager {
	pool := make([]int, 0, portEnd-portStart)
	for p := portStart; p < portEnd; p++ {
		pool = append(pool, p)
	}
	return &SessionManager{
		sessions:   make(map[string]*Session),
		byTenant:   make(map[string]string),
		portPool:   pool,
		portInUse:  make(map[int]bool),
		supervisor: supervisor,
		registry:   registry,
		idleTTL:    30 * time.Minute,
		lastCreate: make(map[string]time.Time),
	}
}

// allocatePort takes a port from the pool.
func (sm *SessionManager) allocatePort() (int, int, error) {
	for _, p := range sm.portPool {
		if !sm.portInUse[p] {
			apiPort := p + 1000 // SOCKS 2080 -> API 3080
			if !sm.portInUse[apiPort] {
				sm.portInUse[p] = true
				sm.portInUse[apiPort] = true
				return p, apiPort, nil
			}
		}
	}
	return 0, 0, fmt.Errorf("no available ports in pool")
}

// releasePort returns a port to the pool.
func (sm *SessionManager) releasePort(socksPort, apiPort int) {
	delete(sm.portInUse, socksPort)
	delete(sm.portInUse, apiPort)
}

// CreateSession creates a new session for a tenant, killing any existing session.
func (sm *SessionManager) CreateSession(ctx context.Context, tenantID, deviceID, secret string) (*Session, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Rate limiting: 1 session/minute per tenant
	if last, ok := sm.lastCreate[tenantID]; ok {
		if time.Since(last) < time.Minute {
			return nil, fmt.Errorf("rate limited: wait %v before creating new session",
				time.Minute-time.Since(last))
		}
	}

	// Takeover: kill existing session for this tenant
	if existingID, ok := sm.byTenant[tenantID]; ok {
		if existing, ok := sm.sessions[existingID]; ok {
			log.Printf("[SESSION] Takeover: killing old session %s for tenant %s", existingID, tenantID)
			sm.terminateSessionLocked(ctx, existing)
		}
	}

	// Allocate ports
	socksPort, apiPort, err := sm.allocatePort()
	if err != nil {
		return nil, err
	}

	// Generate session ID
	now := time.Now()
	sessionID := fmt.Sprintf("s-%s-%d", tenantID, now.UnixMilli())

	session := &Session{
		SessionID:  sessionID,
		TenantID:   tenantID,
		DeviceID:   deviceID,
		SOCKSPort:  socksPort,
		APIPort:    apiPort,
		Status:     "active",
		CreatedAt:  now,
		LastActive: now,
	}

	sm.sessions[sessionID] = session
	sm.byTenant[tenantID] = sessionID
	sm.lastCreate[tenantID] = now

	// Start tenant runtime with session ports
	tenant := sm.registry.GetTenant(tenantID)
	if tenant != nil {
		tenant.SOCKSPort = socksPort
		tenant.APIPort = apiPort
		tenant.Secret = secret
		go func() {
			if err := sm.supervisor.StartTenant(ctx, tenant); err != nil {
				log.Printf("[SESSION] Failed to start runtime for session %s: %v", sessionID, err)
				sm.mu.Lock()
				session.Status = "failed"
				sm.mu.Unlock()
			}
		}()
	}

	log.Printf("[SESSION] Created: id=%s tenant=%s device=%s socks=%d api=%d",
		sessionID, tenantID, deviceID, socksPort, apiPort)

	return session, nil
}

// terminateSessionLocked gracefully terminates a session (must hold mu).
func (sm *SessionManager) terminateSessionLocked(ctx context.Context, session *Session) {
	if session.Status == "terminated" {
		return
	}
	session.Status = "terminating"
	log.Printf("[SESSION] Terminating: %s (tenant %s)", session.SessionID, session.TenantID)

	// Kill the runtime process
	if sm.supervisor != nil {
		sm.supervisor.StopTenant(session.TenantID)
	}

	// Release ports
	sm.releasePort(session.SOCKSPort, session.APIPort)

	// Cleanup maps
	if activeID, ok := sm.byTenant[session.TenantID]; ok && activeID == session.SessionID {
		delete(sm.byTenant, session.TenantID)
	}

	session.Status = "terminated"
	log.Printf("[SESSION] Terminated: %s", session.SessionID)
}

// TerminateSession terminates a session by ID.
func (sm *SessionManager) TerminateSession(ctx context.Context, sessionID string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[sessionID]
	if !ok {
		return fmt.Errorf("session %s not found", sessionID)
	}
	sm.terminateSessionLocked(ctx, session)
	delete(sm.sessions, sessionID)
	return nil
}

// GetSession returns a session by ID.
func (sm *SessionManager) GetSession(sessionID string) *Session {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.sessions[sessionID]
}

// GetTenantSession returns the active session for a tenant.
func (sm *SessionManager) GetTenantSession(tenantID string) *Session {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	if sid, ok := sm.byTenant[tenantID]; ok {
		return sm.sessions[sid]
	}
	return nil
}

// TouchSession updates the last active timestamp.
func (sm *SessionManager) TouchSession(sessionID string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if s, ok := sm.sessions[sessionID]; ok {
		s.LastActive = time.Now()
	}
}

// StartCleanupLoop runs a background goroutine that reaps idle sessions.
func (sm *SessionManager) StartCleanupLoop(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sm.cleanupIdleSessions(ctx)
			}
		}
	}()
	log.Printf("[SESSION] Cleanup loop started (TTL: %v)", sm.idleTTL)
}

// cleanupIdleSessions terminates sessions that have been idle beyond TTL.
func (sm *SessionManager) cleanupIdleSessions(ctx context.Context) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	now := time.Now()
	var toDelete []string
	for id, s := range sm.sessions {
		if s.Status == "active" && now.Sub(s.LastActive) > sm.idleTTL {
			log.Printf("[SESSION] Idle timeout: %s (tenant %s, idle %v)",
				id, s.TenantID, now.Sub(s.LastActive))
			sm.terminateSessionLocked(ctx, s)
			toDelete = append(toDelete, id)
		}
	}
	for _, id := range toDelete {
		delete(sm.sessions, id)
	}
	if len(toDelete) > 0 {
		log.Printf("[SESSION] Cleaned up %d idle sessions", len(toDelete))
	}
}

// ActiveCount returns the number of active sessions.
func (sm *SessionManager) ActiveCount() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	count := 0
	for _, s := range sm.sessions {
		if s.Status == "active" {
			count++
		}
	}
	return count
}
