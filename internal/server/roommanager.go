package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/openlibrecommunity/olcrtc/internal/telemost"
)

// RoomManager creates and rotates Telemost rooms automatically.
// It also serves an HTTP API for clients to discover the current room.
type RoomManager struct {
	oauthToken     string
	rotateInterval time.Duration
	apiPort        int

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
// oauthToken: Yandex OAuth token with telemost:write scope
// rotateInterval: how often to create a new room (e.g. 3h)
// apiPort: HTTP port for the /api/room endpoint
func NewRoomManager(oauthToken string, rotateInterval time.Duration, apiPort int) *RoomManager {
	return &RoomManager{
		oauthToken:     oauthToken,
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

// Run starts the room manager: creates first room, starts HTTP API, rotates.
func (rm *RoomManager) Run(ctx context.Context) error {
	// Create first room
	if err := rm.createNewRoom(); err != nil {
		return fmt.Errorf("create initial room: %w", err)
	}

	// Start HTTP API
	go rm.serveHTTP(ctx)

	// Room rotation loop
	ticker := time.NewTicker(rm.rotateInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := rm.createNewRoom(); err != nil {
				log.Printf("[ROOM-MGR] Failed to rotate room: %v", err)
				// Don't fail — keep using current room
				continue
			}
		}
	}
}

func (rm *RoomManager) createNewRoom() error {
	log.Println("[ROOM-MGR] Creating new Telemost room...")

	conf, err := telemost.CreateConference(rm.oauthToken)
	if err != nil {
		return fmt.Errorf("create conference: %w", err)
	}

	// Generate new encryption key
	keyBytes := make([]byte, 32)
	if _, err := rand.Read(keyBytes); err != nil {
		return fmt.Errorf("generate key: %w", err)
	}
	keyHex := hex.EncodeToString(keyBytes)

	roomURL := "https://telemost.yandex.ru/j/" + conf.RoomID

	rm.mu.Lock()
	rm.roomID = conf.RoomID
	rm.roomURL = roomURL
	rm.keyHex = keyHex
	rm.mu.Unlock()

	log.Printf("[ROOM-MGR] New room: %s (rotates in %v)", conf.RoomID, rm.rotateInterval)

	if rm.onNewRoom != nil {
		rm.onNewRoom(roomURL, keyHex)
	}

	return nil
}

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
