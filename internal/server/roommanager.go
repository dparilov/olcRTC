package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
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

	mu      sync.RWMutex
	roomID  string
	roomURL string
	keyHex  string

	onNewRoom func(roomURL, keyHex string) // callback when room changes
}

// RoomInfo is returned by the HTTP API.
type RoomInfo struct {
	RoomID  string `json:"room_id"`
	RoomURL string `json:"room_url"`
	KeyHex  string `json:"key_hex"`
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

	// Publish to Yandex Disk (passive rendezvous)
	record := &rendezvous.RoomRecord{
		RoomID:    conf.RoomID,
		RoomURL:   roomURL,
		CreatedAt: now.Format(time.RFC3339),
		ExpiresAt: now.Add(rm.rotateInterval).Format(time.RFC3339),
		Version:   1,
	}
	if err := rendezvous.PublishRoom(rm.oauthToken, record); err != nil {
		return fmt.Errorf("publish to disk: %w", err)
	}

	rm.mu.Lock()
	rm.roomID = conf.RoomID
	rm.roomURL = roomURL
	rm.keyHex = keyHex
	rm.mu.Unlock()

	log.Printf("[ROOM-MGR] Published room %s to Yandex Disk (rotates in %v)", conf.RoomID, rm.rotateInterval)

	if rm.onNewRoom != nil {
		rm.onNewRoom(roomURL, keyHex)
	}

	return nil
}

// --- Optional HTTP API (for direct-access scenarios) ---

func (rm *RoomManager) serveHTTP(ctx context.Context) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/room", rm.handleRoom)
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "ok")
	})

	addr := fmt.Sprintf(":%d", rm.apiPort)
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
		KeyHex:  rm.keyHex,
		Expires: time.Now().Add(rm.rotateInterval).Format(time.RFC3339),
	}
	rm.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(info)
}
