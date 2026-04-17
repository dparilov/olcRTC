package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/openlibrecommunity/olcrtc/internal/rendezvous"
	"github.com/openlibrecommunity/olcrtc/internal/telemost"
)

// RoomManager creates and rotates Telemost rooms automatically.
// Room discovery uses Yandex Disk as a passive rendezvous (CloudTransport pattern).
// Optionally also serves a local HTTP API for direct-access scenarios.
type RoomManager struct {
	oauthToken     string
	masterSecret   string
	rotateInterval time.Duration
	apiPort        int // 0 = no HTTP API

	mu        sync.RWMutex
	roomID    string
	roomURL   string
	keyHex    string
	expiresAt string // real expiry from room record

	onNewRoom func(roomURL, keyHex string) // callback when room changes

	intentAPI *IntentAPI // room intent API handler
}

// RoomInfo is returned by the HTTP API.
// NOTE: key_hex is intentionally NOT included — keys are derived
// from the shared master secret, never transmitted over the network.
type RoomInfo struct {
	RoomID  string `json:"room_id"`
	RoomURL string `json:"room_url"`
	Expires string `json:"expires"` // ISO 8601
}

// NewRoomManager creates a room manager.
// oauthToken: Yandex OAuth token with telemost + disk scopes
// masterSecret: shared secret for deterministic key derivation
// rotateInterval: how often to create a new room (e.g. 3h)
// apiPort: HTTP port for /api/room (0 = disabled)
func NewRoomManager(oauthToken, masterSecret string, rotateInterval time.Duration, apiPort int) *RoomManager {
	return &RoomManager{
		oauthToken:     oauthToken,
		masterSecret:   masterSecret,
		rotateInterval: rotateInterval,
		apiPort:        apiPort,
	}
}

// SetNewRoomCallback sets a function to call when a new room is created.
func (rm *RoomManager) SetNewRoomCallback(fn func(roomURL, keyHex string)) {
	rm.onNewRoom = fn
}

// CurrentRoom returns the current room URL and key.
func (rm *RoomManager) CurrentRoom() (string, string) {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	return rm.roomURL, rm.keyHex
}

// Run starts the room manager: creates first room, publishes to Disk, rotates.
func (rm *RoomManager) Run(ctx context.Context) error {
	// Create first room
	if err := rm.createAndPublish(); err != nil {
		return fmt.Errorf("create initial room: %w", err)
	}

	// Optionally start HTTP API (for direct-access scenarios)
	if rm.apiPort > 0 {
		go rm.serveHTTP(ctx)
	}

	// Room rotation loop
	ticker := time.NewTicker(rm.rotateInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Cleanup: delete room file on shutdown
			log.Println("[ROOM-MGR] Shutting down, cleaning up Yandex Disk...")
			if err := rendezvous.DeleteRoom(rm.oauthToken); err != nil {
				log.Printf("[ROOM-MGR] Cleanup warning: %v", err)
			}
			return nil
		case <-ticker.C:
			if err := rm.createAndPublish(); err != nil {
				log.Printf("[ROOM-MGR] Failed to rotate room: %v", err)
				continue // keep using current room
			}
		}
	}
}

func (rm *RoomManager) createAndPublish() error {
	log.Println("[ROOM-MGR] Creating new Telemost room...")

	conf, err := telemost.CreateConference(rm.oauthToken)
	if err != nil {
		return fmt.Errorf("create conference: %w", err)
	}

	// Deterministic key: HMAC(masterSecret, roomID)
	keyHex := rendezvous.DeriveKey(rm.masterSecret, conf.RoomID)
	roomURL := "https://telemost.yandex.ru/j/" + conf.RoomID
	now := time.Now()

	// Publish signed room record to Yandex Disk (passive rendezvous)
	record := &rendezvous.RoomRecord{
		RoomID:    conf.RoomID,
		RoomURL:   roomURL,
		CreatedAt: now.Format(time.RFC3339),
		ExpiresAt: now.Add(rm.rotateInterval).Format(time.RFC3339),
	}
	if err := rendezvous.SignRecord(record, rm.masterSecret, 1); err != nil {
		return fmt.Errorf("sign record: %w", err)
	}
	if err := rendezvous.PublishRoom(rm.oauthToken, record); err != nil {
		return fmt.Errorf("publish to disk: %w", err)
	}

	rm.mu.Lock()
	rm.roomID = conf.RoomID
	rm.roomURL = roomURL
	rm.keyHex = keyHex
	rm.expiresAt = record.ExpiresAt
	rm.mu.Unlock()

	log.Printf("[ROOM-MGR] Published room %s to Yandex Disk (signed v%d, rotates in %v)", conf.RoomID, record.Version, rm.rotateInterval)

	if rm.onNewRoom != nil {
		rm.onNewRoom(roomURL, keyHex)
	}

	return nil
}

// --- Optional HTTP API (for direct-access scenarios) ---

// IntentAPI returns the intent API handler for state updates.
func (rm *RoomManager) IntentAPI() *IntentAPI {
	return rm.intentAPI
}

func (rm *RoomManager) serveHTTP(ctx context.Context) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/room", rm.handleRoom)
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "ok")
	})

	// Register room intent API
	previousSecret := ""
	if v := os.Getenv("OLCRTC_PREVIOUS_SECRET"); v != "" {
		previousSecret = v
	}
	rm.intentAPI = NewIntentAPI(rm.masterSecret, previousSecret)
	rm.intentAPI.SetAcceptedCallback(func(record *rendezvous.RoomRecord, keyHex string) {
		rm.intentAPI.UpdateState(record.RecordID, IntentStarting, "")
		log.Printf("[ROOM-MGR] Intent accepted — switching to room %s", record.RoomID)

		roomURL := record.RoomURL
		if roomURL == "" {
			roomURL = "https://telemost.yandex.ru/j/" + record.RoomID
		}

		// Update current room state
		rm.mu.Lock()
		rm.roomID = record.RoomID
		rm.roomURL = roomURL
		rm.keyHex = keyHex
		rm.expiresAt = record.ExpiresAt
		rm.mu.Unlock()

		// Notify server to switch to new room
		if rm.onNewRoom != nil {
			rm.onNewRoom(roomURL, keyHex)
		}

		rm.intentAPI.UpdateState(record.RecordID, IntentReady, "")
		log.Printf("[ROOM-MGR] Intent ready — room %s active", record.RoomID)
	})
	rm.intentAPI.RegisterRoutes(mux)

	// Periodic cleanup of stale intents (every 10 min, 30 min retention)
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				rm.intentAPI.CleanupStale(30 * time.Minute)
			}
		}
	}()

	// Bind to all interfaces for remote client access
	addr := fmt.Sprintf("0.0.0.0:%d", rm.apiPort)
	srv := &http.Server{Addr: addr, Handler: mux}

	go func() {
		<-ctx.Done()
		srv.Close()
	}()

	log.Printf("[ROOM-MGR] HTTP API listening on %s", addr)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Printf("[ROOM-MGR] HTTP API error: %v", err)
	}
}

func (rm *RoomManager) handleRoom(w http.ResponseWriter, r *http.Request) {
	rm.mu.RLock()
	info := RoomInfo{
		RoomID:  rm.roomID,
		RoomURL: rm.roomURL,
		Expires: rm.expiresAt, // real expiry from room record, not synthetic
	}
	rm.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	// No CORS — API is loopback-only by default
	json.NewEncoder(w).Encode(info)
}
