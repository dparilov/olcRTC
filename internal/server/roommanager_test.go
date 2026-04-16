package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
