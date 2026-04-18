package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/openlibrecommunity/olcrtc/internal/rendezvous"
)

func TestIntentStateProgression_Ready(t *testing.T) {
	api := NewIntentAPI("test-secret", "", 1080)

	// Simulate accepted intent
	api.mu.Lock()
	api.intents["test-ready-001"] = &IntentEntry{
		Record:    &rendezvous.RoomRecord{RecordID: "test-ready-001", RoomID: "123"},
		State:     IntentAccepted,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	api.mu.Unlock()

	// accepted -> starting
	api.UpdateState("test-ready-001", IntentStarting, "")
	api.mu.RLock()
	if api.intents["test-ready-001"].State != IntentStarting {
		t.Fatal("expected starting")
	}
	api.mu.RUnlock()

	// starting -> ready
	api.UpdateState("test-ready-001", IntentReady, "")
	api.mu.RLock()
	if api.intents["test-ready-001"].State != IntentReady {
		t.Fatal("expected ready")
	}
	api.mu.RUnlock()
}

func TestIntentStateProgression_Failed(t *testing.T) {
	api := NewIntentAPI("test-secret", "", 1080)

	api.mu.Lock()
	api.intents["test-fail-001"] = &IntentEntry{
		Record:    &rendezvous.RoomRecord{RecordID: "test-fail-001", RoomID: "456"},
		State:     IntentStarting,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	api.mu.Unlock()

	// starting -> failed
	api.UpdateState("test-fail-001", IntentFailed, "connection refused")
	api.mu.RLock()
	entry := api.intents["test-fail-001"]
	api.mu.RUnlock()

	if entry.State != IntentFailed {
		t.Fatal("expected failed")
	}
	if entry.Error != "connection refused" {
		t.Fatalf("expected error msg, got %q", entry.Error)
	}
}

func TestIntentCleanupStale(t *testing.T) {
	api := NewIntentAPI("test-secret", "", 1080)

	// Add a ready intent from 1 hour ago
	api.mu.Lock()
	api.intents["old-ready"] = &IntentEntry{
		Record:    &rendezvous.RoomRecord{RecordID: "old-ready"},
		State:     IntentReady,
		CreatedAt: time.Now().Add(-1 * time.Hour),
		UpdatedAt: time.Now().Add(-1 * time.Hour),
	}
	api.intents["fresh-starting"] = &IntentEntry{
		Record:    &rendezvous.RoomRecord{RecordID: "fresh-starting"},
		State:     IntentStarting,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	api.mu.Unlock()

	api.CleanupStale(30 * time.Minute)

	api.mu.RLock()
	defer api.mu.RUnlock()

	if _, ok := api.intents["old-ready"]; ok {
		t.Fatal("old ready intent should have been cleaned")
	}
	if _, ok := api.intents["fresh-starting"]; !ok {
		t.Fatal("fresh starting intent should NOT be cleaned")
	}
}

func TestHandleRoom_NoKeyHexExposed(t *testing.T) {
	rm := &RoomManager{
		roomID:  "test-room-123",
		roomURL: "https://telemost.yandex.ru/j/test-room-123",
		keyHex:  "supersecretkey1234567890abcdef1234567890abcdef1234567890abcdef1234",
	}

	req := httptest.NewRequest("GET", "/api/room", nil)
	w := httptest.NewRecorder()

	rm.handleRoom(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Verify key_hex is NOT in response body
	body := w.Body.String()
	if strings.Contains(body, "key_hex") {
		t.Fatal("SECURITY: key_hex must not appear in API response")
	}
	if strings.Contains(body, rm.keyHex) {
		t.Fatal("SECURITY: actual key value must not appear in API response")
	}
	if strings.Contains(body, "supersecret") {
		t.Fatal("SECURITY: key material leaked in API response")
	}

	// Verify expected fields are present
	var info RoomInfo
	if err := json.Unmarshal(w.Body.Bytes(), &info); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if info.RoomID != "test-room-123" {
		t.Fatalf("expected room_id test-room-123, got %s", info.RoomID)
	}

	// Verify no wildcard CORS
	cors := resp.Header.Get("Access-Control-Allow-Origin")
	if cors == "*" {
		t.Fatal("SECURITY: wildcard CORS must not be present")
	}
}
