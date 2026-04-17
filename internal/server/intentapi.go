package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/openlibrecommunity/olcrtc/internal/rendezvous"
)

// IntentState represents the lifecycle of a room intent.
type IntentState string

const (
	IntentAccepted IntentState = "accepted"
	IntentStarting IntentState = "starting"
	IntentReady    IntentState = "ready"
	IntentFailed   IntentState = "failed"
	IntentDuplicate IntentState = "duplicate"
	IntentExpired  IntentState = "expired"
)

// IntentEntry tracks the state of a submitted room intent.
type IntentEntry struct {
	Record    *rendezvous.RoomRecord `json:"record"`
	State     IntentState            `json:"state"`
	Error     string                 `json:"error,omitempty"`
	CreatedAt time.Time              `json:"created_at"`
	UpdatedAt time.Time              `json:"updated_at"`
}

// IntentAPI handles room intent submission and status polling.
type IntentAPI struct {
	masterSecret   string
	previousSecret string

	mu      sync.RWMutex
	intents map[string]*IntentEntry // keyed by record_id

	// Callback invoked when a new intent is accepted.
	// The server should start the room join process.
	onAccepted func(record *rendezvous.RoomRecord, keyHex string)
}

// NewIntentAPI creates a new room intent API handler.
func NewIntentAPI(masterSecret, previousSecret string) *IntentAPI {
	return &IntentAPI{
		masterSecret:   masterSecret,
		previousSecret: previousSecret,
		intents:        make(map[string]*IntentEntry),
	}
}

// SetAcceptedCallback sets the function called when a new intent is accepted.
func (api *IntentAPI) SetAcceptedCallback(fn func(record *rendezvous.RoomRecord, keyHex string)) {
	api.onAccepted = fn
}

// UpdateState updates the state of an intent by record_id.
func (api *IntentAPI) UpdateState(recordID string, state IntentState, errMsg string) {
	api.mu.Lock()
	defer api.mu.Unlock()
	if entry, ok := api.intents[recordID]; ok {
		entry.State = state
		entry.UpdatedAt = time.Now()
		if errMsg != "" {
			entry.Error = errMsg
		}
		log.Printf("[INTENT-API] %s -> %s", recordID[:8], state)
	}
}

// RegisterRoutes adds the intent API routes to a ServeMux.
func (api *IntentAPI) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/room-intent", api.handleRoomIntent)
	mux.HandleFunc("/api/room-intent/", api.handleRoomIntentStatus)
}

// POST /api/room-intent
func (api *IntentAPI) handleRoomIntent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.jsonError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required")
		return
	}

	var record rendezvous.RoomRecord
	if err := json.NewDecoder(r.Body).Decode(&record); err != nil {
		api.jsonError(w, http.StatusBadRequest, "bad_request", fmt.Sprintf("invalid JSON: %v", err))
		return
	}

	// Validate required fields
	if record.RoomID == "" || record.RoomURL == "" {
		api.jsonError(w, http.StatusBadRequest, "bad_request", "room_id and room_url required")
		return
	}
	if record.RecordID == "" {
		api.jsonError(w, http.StatusBadRequest, "bad_request", "record_id required")
		return
	}
	if record.Version < 2 || record.Sig == "" {
		api.jsonError(w, http.StatusBadRequest, "bad_request", "signed v2+ record required")
		return
	}

	// Validate room ID format
	if err := rendezvous.ValidateRoomID(record.RoomID); err != nil {
		api.jsonError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	// Check expiry
	if rendezvous.IsExpired(&record) {
		api.jsonError(w, http.StatusBadRequest, "expired", "room intent has expired")
		return
	}

	// Verify signature
	matched, err := rendezvous.VerifyRecordMulti(&record, api.masterSecret, api.previousSecret)
	if err != nil {
		api.jsonError(w, http.StatusForbidden, "invalid_signature", "signature verification failed")
		return
	}

	// Dedup by record_id
	api.mu.Lock()
	if existing, ok := api.intents[record.RecordID]; ok {
		api.mu.Unlock()
		api.jsonResponse(w, http.StatusOK, map[string]interface{}{
			"status":    string(existing.State),
			"record_id": record.RecordID,
			"message":   "duplicate intent",
		})
		return
	}

	// Accept the intent
	now := time.Now()
	entry := &IntentEntry{
		Record:    &record,
		State:     IntentAccepted,
		CreatedAt: now,
		UpdatedAt: now,
	}
	api.intents[record.RecordID] = entry
	api.mu.Unlock()

	secret := api.masterSecret
	if matched == 2 {
		secret = api.previousSecret
		log.Printf("[INTENT-API] Record signed with previous secret (rotation window)")
	}

	keyHex := rendezvous.DeriveKey(secret, record.RoomID)
	log.Printf("[INTENT-API] Accepted room intent: room=%s record_id=%s key_version=%d",
		record.RoomID, record.RecordID[:8], record.KeyVersion)

	// Notify server to start room join
	if api.onAccepted != nil {
		go api.onAccepted(&record, keyHex)
	}

	api.jsonResponse(w, http.StatusAccepted, map[string]interface{}{
		"status":    "accepted",
		"record_id": record.RecordID,
	})
}

// GET /api/room-intent/{record_id}
func (api *IntentAPI) handleRoomIntentStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.jsonError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}

	// Extract record_id from path: /api/room-intent/{record_id}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/room-intent/"), "/")
	recordID := parts[0]
	if recordID == "" {
		api.jsonError(w, http.StatusBadRequest, "bad_request", "record_id required in path")
		return
	}

	api.mu.RLock()
	entry, ok := api.intents[recordID]
	api.mu.RUnlock()

	if !ok {
		api.jsonResponse(w, http.StatusNotFound, map[string]interface{}{
			"status":    "unknown",
			"record_id": recordID,
		})
		return
	}

	resp := map[string]interface{}{
		"status":    string(entry.State),
		"record_id": recordID,
		"room_id":   entry.Record.RoomID,
		"room_url":  entry.Record.RoomURL,
	}
	if entry.Error != "" {
		resp["error"] = entry.Error
	}

	api.jsonResponse(w, http.StatusOK, resp)
}

func (api *IntentAPI) jsonResponse(w http.ResponseWriter, code int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(data)
}

func (api *IntentAPI) jsonError(w http.ResponseWriter, code int, status, message string) {
	api.jsonResponse(w, code, map[string]interface{}{
		"status":  status,
		"message": message,
	})
}
