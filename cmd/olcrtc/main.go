// Package main provides the olcrtc CLI entrypoint.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/openlibrecommunity/olcrtc/internal/client"
	"github.com/openlibrecommunity/olcrtc/internal/logger"
	"github.com/openlibrecommunity/olcrtc/internal/names"
	"github.com/openlibrecommunity/olcrtc/internal/rendezvous"
	"github.com/openlibrecommunity/olcrtc/internal/server"
)

type config struct {
	mode           string
	roomID         string
	provider       string
	socksPort      int
	socksHost      string
	keyHex         string
	debug          bool
	dataDir        string
	duo            bool
	dnsServer      string
	socksProxyAddr string
	socksProxyPort int
	// Auto-room management (server)
	autoRoom       bool
	oauthToken     string
	masterSecret   string
	rotateHours    int
	apiPort        int
	// Room discovery (client)
	apiURL         string  // direct HTTP API
	discover       bool    // Yandex Disk rendezvous
}

var (
	errUnsupportedProvider = errors.New("only telemost provider supported")
	errRoomIDRequired      = errors.New("room ID required")
	errModeRequired        = errors.New("specify -mode srv or -mode cnc")
)

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run() error {
	cfg := parseFlags()
	configureLogging(cfg.debug)

	if err := validateConfig(cfg); err != nil {
		return err
	}

	dataDir, err := resolveDataDir(cfg.dataDir)
	if err != nil {
		return err
	}

	if err := loadNames(dataDir); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	errCh := make(chan error, 1)
	go runMode(ctx, cfg, errCh)

	select {
	case <-sigCh:
		log.Println("Shutting down gracefully...")
		cancel()
		return waitForShutdown(errCh)
	case err := <-errCh:
		return err
	}
}

func parseFlags() config {
	cfg := config{}

	flag.StringVar(&cfg.mode, "mode", "", "Mode: srv or cnc")
	flag.StringVar(&cfg.roomID, "id", "", "Telemost room ID")
	flag.StringVar(&cfg.provider, "provider", "telemost", "Provider (telemost only)")
	flag.IntVar(&cfg.socksPort, "socks-port", 1080, "SOCKS5 port (client only)")
	flag.StringVar(&cfg.socksHost, "socks-host", "127.0.0.1", "SOCKS5 listen host (client only)")
	// NOTE: -key flag removed for security (secrets must use env vars only)
	// cfg.keyHex is set from OLCRTC_KEY env var below
	flag.BoolVar(&cfg.debug, "debug", false, "Enable verbose logging")
	flag.StringVar(&cfg.dataDir, "data", "data", "Path to data directory")
	flag.BoolVar(&cfg.duo, "duo", false, "Use dual channels for 2x throughput")
	flag.StringVar(&cfg.dnsServer, "dns", "1.1.1.1:53", "DNS server (default: Cloudflare 1.1.1.1)")
	flag.StringVar(&cfg.socksProxyAddr, "socks-proxy", "", "SOCKS5 proxy address (server only)")
	flag.IntVar(&cfg.socksProxyPort, "socks-proxy-port", 1080, "SOCKS5 proxy port (server only)")
	flag.BoolVar(&cfg.autoRoom, "auto-room", false, "Auto-create and rotate Telemost rooms (server only)")
	// NOTE: --oauth-token and --master-secret flags removed for security.
	// Secrets must be passed via env vars: OLCRTC_OAUTH_TOKEN, OLCRTC_MASTER_SECRET

	// Secrets loaded exclusively from env vars (never argv)
	cfg.oauthToken = os.Getenv("OLCRTC_OAUTH_TOKEN")
	cfg.masterSecret = os.Getenv("OLCRTC_MASTER_SECRET")
	cfg.keyHex = os.Getenv("OLCRTC_KEY")
	flag.IntVar(&cfg.rotateHours, "rotate-hours", 3, "Room rotation interval in hours")
	flag.IntVar(&cfg.apiPort, "api-port", 8080, "HTTP API port for room discovery (0=disabled)")
	flag.StringVar(&cfg.apiURL, "api-url", "", "Server API URL for room discovery (direct HTTP)")
	flag.BoolVar(&cfg.discover, "discover", false, "Discover room via Yandex Disk rendezvous (client only)")
	flag.Parse()

	return cfg
}

func configureLogging(debug bool) {
	if debug {
		log.SetFlags(log.Ltime | log.Lshortfile)
		logger.SetVerbose(true)
		return
	}

	log.SetFlags(log.Ltime)
}

func validateConfig(cfg config) error {
	switch {
	case cfg.provider != "telemost":
		return errUnsupportedProvider
	case cfg.roomID == "" && !cfg.autoRoom && cfg.apiURL == "" && !cfg.discover:
		return errRoomIDRequired
	case cfg.mode != "srv" && cfg.mode != "cnc":
		return errModeRequired
	default:
		return nil
	}
}

func resolveDataDir(dataDir string) (string, error) {
	if filepath.IsAbs(dataDir) {
		return dataDir, nil
	}

	exePath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable path: %w", err)
	}

	return filepath.Join(filepath.Dir(exePath), dataDir), nil
}

func loadNames(dataDir string) error {
	namesPath := filepath.Join(dataDir, "names")
	surnamesPath := filepath.Join(dataDir, "surnames")
	if err := names.LoadNameFiles(namesPath, surnamesPath); err != nil {
		return fmt.Errorf("load embedded names override: %w", err)
	}

	return nil
}

func runMode(ctx context.Context, cfg config, errCh chan<- error) {
	switch cfg.mode {
	case "srv":
		if cfg.discover {
			errCh <- runWatchServer(ctx, cfg)
		} else if cfg.autoRoom {
			errCh <- runAutoRoomServer(ctx, cfg)
		} else {
			roomURL := "https://telemost.yandex.ru/j/" + cfg.roomID
			errCh <- server.Run(
				ctx,
				roomURL,
				cfg.keyHex,
				cfg.duo,
				cfg.dnsServer,
				cfg.socksProxyAddr,
				cfg.socksProxyPort,
			)
		}
	case "cnc":
		if cfg.discover {
			errCh <- runDiscoverClient(ctx, cfg)
		} else if cfg.apiURL != "" {
			errCh <- runAPIClient(ctx, cfg)
		} else {
			roomURL := "https://telemost.yandex.ru/j/" + cfg.roomID

			// If --oauth-token and --master-secret provided, publish room to Disk
			if cfg.oauthToken != "" && cfg.masterSecret != "" {
				publishRoomToDisk(cfg.oauthToken, cfg.masterSecret, cfg.roomID, roomURL, cfg.rotateHours)
			}

			keyHex := cfg.keyHex
			if keyHex == "" && cfg.masterSecret != "" {
				keyHex = rendezvous.DeriveKey(cfg.masterSecret, cfg.roomID)
				log.Printf("[CLIENT] Key derived from master secret")
			}

			errCh <- client.Run(
				ctx,
				roomURL,
				keyHex,
				cfg.socksPort,
				cfg.duo,
				cfg.socksHost,
				"",
				"",
			)
		}
	}
}

func runAutoRoomServer(ctx context.Context, cfg config) error {
	if cfg.oauthToken == "" {
		return fmt.Errorf("OLCRTC_OAUTH_TOKEN required for --auto-room mode")
	}
	if cfg.masterSecret == "" {
		return fmt.Errorf("OLCRTC_MASTER_SECRET required for --auto-room mode")
	}

	rotateInterval := time.Duration(cfg.rotateHours) * time.Hour
	rm := server.NewRoomManager(cfg.oauthToken, cfg.masterSecret, rotateInterval, cfg.apiPort)

	var serverCancel context.CancelFunc
	var serverCtx context.Context
	var wg sync.WaitGroup

	rm.SetNewRoomCallback(func(roomURL, keyHex string) {
		// Cancel previous server instance
		if serverCancel != nil {
			log.Println("[AUTO-ROOM] Stopping previous server for room rotation...")
			serverCancel()
			wg.Wait()
		}

		// Start new server instance
		serverCtx, serverCancel = context.WithCancel(ctx)
		wg.Add(1)
		go func() {
			defer wg.Done()
			log.Printf("[AUTO-ROOM] Starting server for room: %s", roomURL)
			if err := server.Run(
				serverCtx,
				roomURL,
				keyHex,
				cfg.duo,
				cfg.dnsServer,
				cfg.socksProxyAddr,
				cfg.socksProxyPort,
			); err != nil {
				log.Printf("[AUTO-ROOM] Server error: %v", err)
			}
		}()
	})

	return rm.Run(ctx)
}

func runAPIClient(ctx context.Context, cfg config) error {
	log.Printf("[API-CLIENT] Fetching room from %s...", cfg.apiURL)

	roomURL, keyHex, err := fetchRoomFromAPI(cfg.apiURL)
	if err != nil {
		return fmt.Errorf("fetch room from API: %w", err)
	}

	log.Printf("[API-CLIENT] Got room: %s", roomURL)

	return client.Run(
		ctx,
		roomURL,
		keyHex,
		cfg.socksPort,
		cfg.duo,
		cfg.socksHost,
		"",
		"",
	)
}

func runDiscoverClient(ctx context.Context, cfg config) error {
	if cfg.oauthToken == "" {
		return fmt.Errorf("OLCRTC_OAUTH_TOKEN required for --discover mode")
	}
	if cfg.masterSecret == "" {
		return fmt.Errorf("OLCRTC_MASTER_SECRET required for --discover mode")
	}

	previousSecret := os.Getenv("OLCRTC_PREVIOUS_SECRET") // rotation window support

	// Retry loop: on conference end, re-fetch room from Disk and reconnect
	for attempt := 1; ; attempt++ {
		log.Printf("[DISCOVER] Attempt %d: fetching room from Yandex Disk...", attempt)

		record, matched, err := rendezvous.FetchAndVerifyRoom(cfg.oauthToken, cfg.masterSecret, previousSecret)
		if err != nil {
			log.Printf("[DISCOVER] Fetch/verify error: %v, retrying in 10s...", err)
			select {
			case <-time.After(10 * time.Second):
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if record == nil {
			log.Printf("[DISCOVER] No room published yet, retrying in 10s...")
			select {
			case <-time.After(10 * time.Second):
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		// Use the secret that matched for key derivation
		secret := cfg.masterSecret
		if matched == 2 {
			secret = previousSecret
			log.Println("[DISCOVER] Record signed with previous secret (rotation window)")
		}

		keyHex := rendezvous.DeriveKey(secret, record.RoomID)
		log.Printf("[DISCOVER] Verified room: %s (sig OK, key_version=%d)", record.RoomURL, record.KeyVersion)

		err = client.Run(
			ctx,
			record.RoomURL,
			keyHex,
			cfg.socksPort,
			cfg.duo,
			cfg.socksHost,
			"",
			"",
		)

		if ctx.Err() != nil {
			return ctx.Err()
		}

		log.Printf("[DISCOVER] Connection ended: %v. Re-fetching room in 5s...", err)
		select {
		case <-time.After(5 * time.Second):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func runWatchServer(ctx context.Context, cfg config) error {
	if cfg.oauthToken == "" {
		return fmt.Errorf("OLCRTC_OAUTH_TOKEN required for --discover server mode")
	}
	if cfg.masterSecret == "" {
		return fmt.Errorf("OLCRTC_MASTER_SECRET required for --discover server mode")
	}

	previousSecret := os.Getenv("OLCRTC_PREVIOUS_SECRET") // rotation window support
	var lastRoomID string

	for {
		log.Println("[WATCH-SRV] Polling Yandex Disk for room...")

		record, matched, err := rendezvous.FetchAndVerifyRoom(cfg.oauthToken, cfg.masterSecret, previousSecret)
		if err != nil {
			log.Printf("[WATCH-SRV] Fetch/verify error: %v, retrying in 10s...", err)
			select {
			case <-time.After(10 * time.Second):
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if record == nil {
			log.Println("[WATCH-SRV] No active room yet, polling in 10s...")
			select {
			case <-time.After(10 * time.Second):
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		// Skip if same room already connected
		if record.RoomID == lastRoomID {
			select {
			case <-time.After(10 * time.Second):
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		// Use the secret that matched for key derivation
		secret := cfg.masterSecret
		if matched == 2 {
			secret = previousSecret
			log.Println("[WATCH-SRV] Record signed with previous secret (rotation window)")
		}

		lastRoomID = record.RoomID
		keyHex := rendezvous.DeriveKey(secret, record.RoomID)
		log.Printf("[WATCH-SRV] Verified room: %s (sig OK, key_version=%d)", record.RoomURL, record.KeyVersion)

		err = server.Run(
			ctx,
			record.RoomURL,
			keyHex,
			cfg.duo,
			cfg.dnsServer,
			cfg.socksProxyAddr,
			cfg.socksProxyPort,
		)

		if ctx.Err() != nil {
			return ctx.Err()
		}

		log.Printf("[WATCH-SRV] Room ended: %v. Polling for new room in 10s...", err)
		lastRoomID = "" // allow reconnect to same room if client re-publishes
		select {
		case <-time.After(10 * time.Second):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func publishRoomToDisk(oauthToken, masterSecret, roomID, roomURL string, rotateHours int) {
	record := &rendezvous.RoomRecord{
		RoomID:    roomID,
		RoomURL:   roomURL,
		CreatedAt: time.Now().Format(time.RFC3339),
		ExpiresAt: time.Now().Add(time.Duration(rotateHours) * time.Hour).Format(time.RFC3339),
	}
	// Sign the record with current master secret (key_version=1)
	if err := rendezvous.SignRecord(record, masterSecret, 1); err != nil {
		log.Printf("[CLIENT] Failed to sign room record: %v", err)
		return
	}
	if err := rendezvous.PublishRoom(oauthToken, record); err != nil {
		log.Printf("[CLIENT] Failed to publish room to Yandex Disk: %v", err)
	} else {
		log.Printf("[CLIENT] Room %s published to Yandex Disk (signed v%d, expires in %dh)", roomID, record.Version, rotateHours)
	}
}

func fetchRoomFromAPI(apiURL string) (string, string, error) {
	resp, err := http.Get(apiURL)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("API returned %d", resp.StatusCode)
	}

	var info struct {
		RoomURL string `json:"room_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return "", "", err
	}

	if info.RoomURL == "" {
		return "", "", fmt.Errorf("API returned empty room_url")
	}

	// Key is NOT returned by API — must be derived from master secret
	return info.RoomURL, "", nil
}

func waitForShutdown(errCh <-chan error) error {
	done := make(chan error, 1)
	go func() {
		done <- <-errCh
	}()

	select {
	case err := <-done:
		if err == nil {
			log.Println("Shutdown complete")
		}
		return err
	case <-time.After(5 * time.Second):
		log.Println("Shutdown timeout, forcing exit")
		return nil
	}
}
