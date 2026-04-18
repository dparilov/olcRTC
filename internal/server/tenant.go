package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Tenant represents a registered tenant in the multi-tenant server.
type Tenant struct {
	TenantID       string    `json:"tenant_id"`
	SecretHash     string    `json:"secret_hash"`      // SHA256(secret)[:16] for uniqueness
	Secret         string    `json:"-"`                 // raw secret, NOT persisted to state file
	OAuthToken     string    `json:"-"`                 // raw OAuth token, NOT persisted to state file
	SOCKSPort      int       `json:"socks_port"`        // server-assigned stable SOCKS port
	APIPort        int       `json:"api_port"`          // tenant runtime API port (SOCKS + 7000, guaranteed no collision with bootstrap 8080)
	FallbackEnabled bool     `json:"fallback_enabled"`
	DiskPath       string    `json:"disk_path"`         // tenant-specific Disk path
	Status         string    `json:"status"`            // registered / active / idle / disabled
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// TenantRegistry manages tenant lifecycle and port allocation.
type TenantRegistry struct {
	mu       sync.RWMutex
	tenants  map[string]*Tenant // keyed by tenant_id
	byHash   map[string]string  // secret_hash -> tenant_id (uniqueness index)
	nextPort int                // next available SOCKS port
	portEnd  int                // end of port range

	stateDir string // directory for state persistence

	// OnRegistered is called after a new tenant is registered.
	// Bootstrap server uses this to auto-start the tenant runtime process.
	OnRegistered func(tenant *Tenant)
}

// NewTenantRegistry creates a new registry with the given port range.
func NewTenantRegistry(portStart, portEnd int, stateDir string) *TenantRegistry {
	r := &TenantRegistry{
		tenants:  make(map[string]*Tenant),
		byHash:   make(map[string]string),
		nextPort: portStart,
		portEnd:  portEnd,
		stateDir: stateDir,
	}
	// Load persisted state
	r.loadState()
	return r
}

// secretFingerprint returns the first 32 hex chars of SHA256(secret).
// 32 hex = 128 bits — sufficient for uniqueness with no realistic collision risk.
func secretFingerprint(secret string) string {
	h := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(h[:])[:32]
}

// Register creates a new tenant or returns existing if same secret.
func (r *TenantRegistry) Register(secret string) (*Tenant, error) {
	if secret == "" {
		return nil, fmt.Errorf("secret is required")
	}
	if len(secret) < 8 {
		return nil, fmt.Errorf("secret must be at least 8 characters")
	}

	hash := secretFingerprint(secret)

	r.mu.Lock()
	defer r.mu.Unlock()

	// Check uniqueness — same secret = same tenant (idempotent)
	if existingID, ok := r.byHash[hash]; ok {
		tenant := r.tenants[existingID]
		// Update secret in memory (re-registration)
		tenant.Secret = secret
		tenant.UpdatedAt = time.Now()
		log.Printf("[TENANT] Re-registration for tenant %s (port %d)", tenant.TenantID, tenant.SOCKSPort)
		return tenant, nil
	}

	// Allocate port
	port := r.nextPort
	if port > r.portEnd {
		return nil, fmt.Errorf("no available ports (range %d-%d exhausted)", r.nextPort, r.portEnd)
	}
	r.nextPort = port + 1

	// Create tenant
	now := time.Now()
	tenantID := fmt.Sprintf("t-%s", hash[:8])
	tenant := &Tenant{
		TenantID:        tenantID,
		SecretHash:      hash,
		Secret:          secret,
		SOCKSPort:       port,
		APIPort:         port + 7001, // API port = SOCKS port + 7001 (e.g., 1080 -> 8081, avoids bootstrap 8080)
		FallbackEnabled: false,
		DiskPath:        fmt.Sprintf("app:/olcrtc/tenants/%s/active-room.json", tenantID),
		Status:          "registered",
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	r.tenants[tenantID] = tenant
	r.byHash[hash] = tenantID

	log.Printf("[TENANT] Registered: id=%s port=%d hash=%s", tenantID, port, hash[:8])

	// Persist state
	r.saveStateLocked()

	// Auto-start tenant runtime process (Option A from spec)
	if r.OnRegistered != nil {
		go r.OnRegistered(tenant)
	}

	return tenant, nil
}

// AttachOAuth adds an OAuth token to a tenant.
func (r *TenantRegistry) AttachOAuth(tenantID, secret, oauthToken string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	tenant, ok := r.tenants[tenantID]
	if !ok {
		return fmt.Errorf("tenant %s not found", tenantID)
	}

	// Verify secret matches
	if secretFingerprint(secret) != tenant.SecretHash {
		return fmt.Errorf("secret mismatch")
	}

	tenant.OAuthToken = oauthToken
	tenant.FallbackEnabled = oauthToken != ""
	tenant.UpdatedAt = time.Now()

	log.Printf("[TENANT] OAuth attached: id=%s fallback=%v", tenantID, tenant.FallbackEnabled)

	r.saveStateLocked()
	return nil
}

// GetTenant returns a tenant by ID.
func (r *TenantRegistry) GetTenant(tenantID string) *Tenant {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.tenants[tenantID]
}

// GetBySecret returns a tenant by secret (via hash lookup).
func (r *TenantRegistry) GetBySecret(secret string) *Tenant {
	hash := secretFingerprint(secret)
	r.mu.RLock()
	defer r.mu.RUnlock()
	if id, ok := r.byHash[hash]; ok {
		return r.tenants[id]
	}
	return nil
}

// FindBySignature tries all tenant secrets to verify a signed record.
// Returns the matching tenant or nil.
func (r *TenantRegistry) FindBySignature(verifyFunc func(secret string) error) *Tenant {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, t := range r.tenants {
		if t.Secret != "" {
			if err := verifyFunc(t.Secret); err == nil {
				return t
			}
		}
	}
	return nil
}

// AllTenants returns a copy of all tenants (without secrets).
func (r *TenantRegistry) AllTenants() []*Tenant {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]*Tenant, 0, len(r.tenants))
	for _, t := range r.tenants {
		result = append(result, t)
	}
	return result
}

// --- State persistence ---

type stateFile struct {
	Tenants  []tenantState `json:"tenants"`
	NextPort int           `json:"next_port"`
}

type tenantState struct {
	TenantID        string    `json:"tenant_id"`
	SecretHash      string    `json:"secret_hash"`
	SOCKSPort       int       `json:"socks_port"`
	APIPort         int       `json:"api_port"`
	FallbackEnabled bool      `json:"fallback_enabled"`
	DiskPath        string    `json:"disk_path"`
	Status          string    `json:"status"`
	CreatedAt       time.Time `json:"created_at"`
}

func (r *TenantRegistry) stateFilePath() string {
	return filepath.Join(r.stateDir, "tenants.json")
}

func (r *TenantRegistry) saveStateLocked() {
	if r.stateDir == "" {
		return
	}
	os.MkdirAll(r.stateDir, 0700)

	state := stateFile{
		NextPort: r.nextPort,
		Tenants:  make([]tenantState, 0, len(r.tenants)),
	}
	for _, t := range r.tenants {
		state.Tenants = append(state.Tenants, tenantState{
			TenantID:        t.TenantID,
			SecretHash:      t.SecretHash,
			SOCKSPort:       t.SOCKSPort,
			APIPort:         t.APIPort,
			FallbackEnabled: t.FallbackEnabled,
			DiskPath:        t.DiskPath,
			Status:          t.Status,
			CreatedAt:       t.CreatedAt,
		})
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		log.Printf("[TENANT] Failed to marshal state: %v", err)
		return
	}
	if err := os.WriteFile(r.stateFilePath(), data, 0600); err != nil {
		log.Printf("[TENANT] Failed to save state: %v", err)
	}
}

func (r *TenantRegistry) loadState() {
	if r.stateDir == "" {
		return
	}
	data, err := os.ReadFile(r.stateFilePath())
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("[TENANT] Failed to load state: %v", err)
		}
		return
	}

	var state stateFile
	if err := json.Unmarshal(data, &state); err != nil {
		log.Printf("[TENANT] Failed to parse state: %v", err)
		return
	}

	r.nextPort = state.NextPort
	for _, ts := range state.Tenants {
		tenant := &Tenant{
			TenantID:        ts.TenantID,
			SecretHash:      ts.SecretHash,
			SOCKSPort:       ts.SOCKSPort,
			APIPort:         ts.APIPort,
			FallbackEnabled: ts.FallbackEnabled,
			DiskPath:        ts.DiskPath,
			Status:          ts.Status,
			CreatedAt:       ts.CreatedAt,
			UpdatedAt:       ts.CreatedAt,
		}
		r.tenants[ts.TenantID] = tenant
		r.byHash[ts.SecretHash] = ts.TenantID
	}

	log.Printf("[TENANT] Loaded %d tenants from state (next port: %d)", len(r.tenants), r.nextPort)
}

// --- Bootstrap HTTP API ---

// RegisterBootstrapRoutes adds the tenant bootstrap API routes.
func (r *TenantRegistry) RegisterBootstrapRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/tenant/register", r.handleRegister)
	mux.HandleFunc("/tenant/oauth", r.handleOAuth)
	mux.HandleFunc("/tenant/config", r.handleConfig)
	mux.HandleFunc("/tenant/status", r.handleStatus)
}

// POST /tenant/register
func (r *TenantRegistry) handleRegister(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		r.jsonError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	var body struct {
		Secret string `json:"secret"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		r.jsonError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
		return
	}

	tenant, err := r.Register(body.Secret)
	if err != nil {
		r.jsonError(w, http.StatusBadRequest, err.Error())
		return
	}

	r.jsonResponse(w, http.StatusOK, map[string]interface{}{
		"status":           "registered",
		"tenant_id":        tenant.TenantID,
		"socks_port":       tenant.SOCKSPort,
		"api_port":         tenant.APIPort,
		"disk_path":        tenant.DiskPath,
		"fallback_enabled": tenant.FallbackEnabled,
	})
}

// POST /tenant/oauth
func (r *TenantRegistry) handleOAuth(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		r.jsonError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	var body struct {
		TenantID   string `json:"tenant_id"`
		Secret     string `json:"secret"`
		OAuthToken string `json:"oauth_token"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		r.jsonError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
		return
	}

	if err := r.AttachOAuth(body.TenantID, body.Secret, body.OAuthToken); err != nil {
		r.jsonError(w, http.StatusForbidden, err.Error())
		return
	}

	tenant := r.GetTenant(body.TenantID)
	r.jsonResponse(w, http.StatusOK, map[string]interface{}{
		"status":           "oauth_attached",
		"tenant_id":        body.TenantID,
		"fallback_enabled": tenant.FallbackEnabled,
	})
}

// POST /tenant/config — retrieve tenant runtime info (secret in body, not query string)
func (r *TenantRegistry) handleConfig(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		r.jsonError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	var body struct {
		TenantID string `json:"tenant_id"`
		Secret   string `json:"secret"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		r.jsonError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	var tenant *Tenant
	if body.TenantID != "" {
		tenant = r.GetTenant(body.TenantID)
	} else if body.Secret != "" {
		tenant = r.GetBySecret(body.Secret)
	}

	if tenant == nil {
		r.jsonError(w, http.StatusNotFound, "tenant not found")
		return
	}

	// Verify secret
	if body.Secret != "" && secretFingerprint(body.Secret) != tenant.SecretHash {
		r.jsonError(w, http.StatusForbidden, "secret mismatch")
		return
	}

	r.jsonResponse(w, http.StatusOK, map[string]interface{}{
		"tenant_id":        tenant.TenantID,
		"socks_port":       tenant.SOCKSPort,
		"api_port":         tenant.APIPort,
		"disk_path":        tenant.DiskPath,
		"fallback_enabled": tenant.FallbackEnabled,
		"status":           tenant.Status,
	})
}

// GET /tenant/status — list all tenants (admin)
func (r *TenantRegistry) handleStatus(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		r.jsonError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}

	tenants := r.AllTenants()
	list := make([]map[string]interface{}, 0, len(tenants))
	for _, t := range tenants {
		list = append(list, map[string]interface{}{
			"tenant_id":        t.TenantID,
			"socks_port":       t.SOCKSPort,
			"api_port":         t.APIPort,
			"fallback_enabled": t.FallbackEnabled,
			"status":           t.Status,
			"created_at":       t.CreatedAt.Format(time.RFC3339),
		})
	}

	r.jsonResponse(w, http.StatusOK, map[string]interface{}{
		"tenants": list,
		"count":   len(list),
	})
}

func (r *TenantRegistry) jsonResponse(w http.ResponseWriter, code int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(data)
}

func (r *TenantRegistry) jsonError(w http.ResponseWriter, code int, message string) {
	r.jsonResponse(w, code, map[string]interface{}{
		"status":  "error",
		"message": message,
	})
}

// Ensure unused import is valid
var _ = strings.TrimSpace
