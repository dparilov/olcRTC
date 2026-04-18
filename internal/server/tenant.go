package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Tenant represents a registered tenant in the multi-tenant server.
type Tenant struct {
	TenantID        string    `json:"tenant_id"`
	SecretHash      string    `json:"secret_hash"`      // SHA256(secret)[:32] for uniqueness
	Secret          string    `json:"-"`                 // raw secret, decrypted in memory
	OAuthToken      string    `json:"-"`                 // raw OAuth token, decrypted in memory
	SOCKSPort       int       `json:"socks_port"`        // server-assigned stable SOCKS port
	APIPort         int       `json:"api_port"`          // tenant runtime API port
	FallbackEnabled bool      `json:"fallback_enabled"`
	DiskPath        string    `json:"disk_path"`         // tenant-specific Disk path
	Status          string    `json:"status"`            // registered / active / idle / disabled
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// TenantRegistry manages tenant lifecycle and port allocation.
type TenantRegistry struct {
	mu       sync.RWMutex
	tenants  map[string]*Tenant // keyed by tenant_id
	byHash   map[string]string  // secret_hash -> tenant_id (uniqueness index)
	nextPort int                // next available SOCKS port
	portEnd  int                // end of port range

	stateDir   string // directory for state persistence
	encryptKey string // passphrase for at-rest encryption of secrets

	// OAuth app credentials for automated token flow
	OAuthClientID     string
	OAuthClientSecret string

	// OnRegistered is called after a new tenant is registered.
	OnRegistered func(tenant *Tenant)

	// OnOAuthAttached is called after OAuth token is attached to a tenant.
	OnOAuthAttached func(tenant *Tenant)
}

// NewTenantRegistry creates a new registry with the given port range.
// encryptKey is used for at-rest encryption of secrets in the state file.
func NewTenantRegistry(portStart, portEnd int, stateDir, encryptKey string) *TenantRegistry {
	r := &TenantRegistry{
		tenants:    make(map[string]*Tenant),
		byHash:     make(map[string]string),
		nextPort:   portStart,
		portEnd:    portEnd,
		stateDir:   stateDir,
		encryptKey: encryptKey,
	}
	r.loadState()
	return r
}

// secretFingerprint returns the first 32 hex chars of SHA256(secret).
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

	if existingID, ok := r.byHash[hash]; ok {
		tenant := r.tenants[existingID]
		tenant.Secret = secret
		tenant.UpdatedAt = time.Now()
		log.Printf("[TENANT] Re-registration for tenant %s (port %d)", tenant.TenantID, tenant.SOCKSPort)
		r.saveStateLocked()
		return tenant, nil
	}

	port := r.nextPort
	if port > r.portEnd {
		return nil, fmt.Errorf("no available ports (range %d-%d exhausted)", r.nextPort, r.portEnd)
	}
	r.nextPort = port + 1

	now := time.Now()
	tenantID := fmt.Sprintf("t-%s", hash[:8])
	tenant := &Tenant{
		TenantID:        tenantID,
		SecretHash:      hash,
		Secret:          secret,
		SOCKSPort:       port,
		APIPort:         port + 7001,
		FallbackEnabled: false,
		DiskPath:        fmt.Sprintf("app:/olcrtc/tenants/%s/active-room.json", tenantID),
		Status:          "registered",
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	r.tenants[tenantID] = tenant
	r.byHash[hash] = tenantID
	log.Printf("[TENANT] Registered: id=%s port=%d hash=%s", tenantID, port, hash[:8])
	r.saveStateLocked()

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
	if secretFingerprint(secret) != tenant.SecretHash {
		return fmt.Errorf("secret mismatch")
	}

	tenant.OAuthToken = oauthToken
	tenant.FallbackEnabled = oauthToken != ""
	tenant.UpdatedAt = time.Now()
	log.Printf("[TENANT] OAuth attached: id=%s fallback=%v", tenantID, tenant.FallbackEnabled)
	r.saveStateLocked()

	if r.OnOAuthAttached != nil {
		go r.OnOAuthAttached(tenant)
	}
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

// AllTenants returns a copy of all tenants.
func (r *TenantRegistry) AllTenants() []*Tenant {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]*Tenant, 0, len(r.tenants))
	for _, t := range r.tenants {
		result = append(result, t)
	}
	return result
}

// --- State persistence with encrypted secrets ---

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
	EncSecret       string    `json:"enc_secret,omitempty"`
	EncOAuthToken   string    `json:"enc_oauth_token,omitempty"`
}

func (r *TenantRegistry) stateFilePath() string {
	return filepath.Join(r.stateDir, "tenants.json")
}

func (r *TenantRegistry) saveStateLocked() {
	if r.stateDir == "" {
		return
	}
	os.MkdirAll(r.stateDir, 0700)

	var aesKey []byte
	if r.encryptKey != "" {
		aesKey = deriveAESKey(r.encryptKey)
	}

	state := stateFile{
		NextPort: r.nextPort,
		Tenants:  make([]tenantState, 0, len(r.tenants)),
	}
	for _, t := range r.tenants {
		ts := tenantState{
			TenantID:        t.TenantID,
			SecretHash:      t.SecretHash,
			SOCKSPort:       t.SOCKSPort,
			APIPort:         t.APIPort,
			FallbackEnabled: t.FallbackEnabled,
			DiskPath:        t.DiskPath,
			Status:          t.Status,
			CreatedAt:       t.CreatedAt,
		}
		if aesKey != nil && t.Secret != "" {
			if enc, err := encryptAESGCM(t.Secret, aesKey); err == nil {
				ts.EncSecret = enc
			} else {
				log.Printf("[TENANT] encrypt secret %s: %v", t.TenantID, err)
			}
		}
		if aesKey != nil && t.OAuthToken != "" {
			if enc, err := encryptAESGCM(t.OAuthToken, aesKey); err == nil {
				ts.EncOAuthToken = enc
			} else {
				log.Printf("[TENANT] encrypt oauth %s: %v", t.TenantID, err)
			}
		}
		state.Tenants = append(state.Tenants, ts)
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

	var aesKey []byte
	if r.encryptKey != "" {
		aesKey = deriveAESKey(r.encryptKey)
	}

	r.nextPort = state.NextPort
	recovered := 0
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
		if aesKey != nil && ts.EncSecret != "" {
			if secret, err := decryptAESGCM(ts.EncSecret, aesKey); err == nil {
				tenant.Secret = secret
				recovered++
			} else {
				log.Printf("[TENANT] decrypt secret %s: %v (re-registration needed)", ts.TenantID, err)
			}
		}
		if aesKey != nil && ts.EncOAuthToken != "" {
			if token, err := decryptAESGCM(ts.EncOAuthToken, aesKey); err == nil {
				tenant.OAuthToken = token
				tenant.FallbackEnabled = true
			} else {
				log.Printf("[TENANT] decrypt oauth %s: %v", ts.TenantID, err)
			}
		}
		r.tenants[ts.TenantID] = tenant
		r.byHash[ts.SecretHash] = ts.TenantID
	}

	log.Printf("[TENANT] Loaded %d tenants (%d recovered secrets, next port: %d)",
		len(r.tenants), recovered, r.nextPort)
}

// --- Bootstrap HTTP API ---

// RegisterBootstrapRoutes adds the tenant bootstrap API routes.
func (r *TenantRegistry) RegisterBootstrapRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/tenant/register", r.handleRegister)
	mux.HandleFunc("/tenant/oauth", r.handleOAuth)
	mux.HandleFunc("/tenant/oauth/start", r.handleOAuthStart)
	mux.HandleFunc("/tenant/oauth/callback", r.handleOAuthCallback)
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

// POST /tenant/oauth — manual OAuth token attachment
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

// POST /tenant/oauth/start — initiate Yandex OAuth consent flow
func (r *TenantRegistry) handleOAuthStart(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		r.jsonError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	if r.OAuthClientID == "" {
		r.jsonError(w, http.StatusServiceUnavailable, "OAuth not configured on server")
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
	tenant := r.GetTenant(body.TenantID)
	if tenant == nil {
		r.jsonError(w, http.StatusNotFound, "tenant not found")
		return
	}
	if secretFingerprint(body.Secret) != tenant.SecretHash {
		r.jsonError(w, http.StatusForbidden, "secret mismatch")
		return
	}
	authURL := fmt.Sprintf("https://oauth.yandex.ru/authorize?response_type=code&client_id=%s&state=%s",
		url.QueryEscape(r.OAuthClientID),
		url.QueryEscape(body.TenantID),
	)
	r.jsonResponse(w, http.StatusOK, map[string]interface{}{
		"status":   "oauth_redirect",
		"auth_url": authURL,
	})
}

// GET /tenant/oauth/callback — Yandex OAuth redirect callback
func (r *TenantRegistry) handleOAuthCallback(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		r.jsonError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	code := req.URL.Query().Get("code")
	tenantID := req.URL.Query().Get("state")
	errParam := req.URL.Query().Get("error")

	if errParam != "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "<h2>OAuth Error</h2><p>%s</p><p>You can close this window.</p>", errParam)
		return
	}
	if code == "" || tenantID == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "<h2>Missing parameters</h2><p>code and state required</p>")
		return
	}
	tenant := r.GetTenant(tenantID)
	if tenant == nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, "<h2>Unknown tenant</h2><p>Tenant %s not found</p>", tenantID)
		return
	}

	token, err := r.exchangeOAuthCode(code)
	if err != nil {
		log.Printf("[TENANT] OAuth exchange failed for %s: %v", tenantID, err)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "<h2>Token Exchange Failed</h2><p>%v</p>", err)
		return
	}

	r.mu.Lock()
	tenant.OAuthToken = token
	tenant.FallbackEnabled = true
	tenant.UpdatedAt = time.Now()
	r.saveStateLocked()
	r.mu.Unlock()

	log.Printf("[TENANT] OAuth automated: id=%s fallback=true", tenantID)

	if r.OnOAuthAttached != nil {
		go r.OnOAuthAttached(tenant)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "<html><body style=\"font-family:sans-serif;text-align:center;padding:60px\">"+
		"<h2>Authorization Successful</h2>"+
		"<p>Yandex Disk access granted for tenant <b>%s</b>.</p>"+
		"<p>Fallback rendezvous is now enabled.</p>"+
		"<p style=\"color:#888\">You can close this window.</p>"+
		"</body></html>", tenantID)
}

// exchangeOAuthCode exchanges an authorization code for an OAuth token.
func (r *TenantRegistry) exchangeOAuthCode(code string) (string, error) {
	data := url.Values{
		"grant_type": {"authorization_code"},
		"code":       {code},
	}
	req, err := http.NewRequest("POST", "https://oauth.yandex.ru/token", strings.NewReader(data.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(r.OAuthClientID, r.OAuthClientSecret)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token exchange HTTP %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", fmt.Errorf("parse token: %w", err)
	}
	if tokenResp.Error != "" {
		return "", fmt.Errorf("oauth: %s (%s)", tokenResp.Error, tokenResp.ErrorDesc)
	}
	if tokenResp.AccessToken == "" {
		return "", fmt.Errorf("empty access_token")
	}
	return tokenResp.AccessToken, nil
}

// POST /tenant/config
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

// GET /tenant/status
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
