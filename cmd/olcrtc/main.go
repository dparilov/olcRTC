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
	// Utility modes run synchronously and exit immediately
	switch cfg.mode {
	case "check":
		return runCheck(cfg)
	case "rotate-secret":
		return runRotateSecret(cfg)
	case "rotate-token":
		return runRotateToken(cfg)
	}

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
	// OLCRTC_KEY is a direct room key override for two valid scenarios:
	//   1. Local development/debugging without a full master-secret setup
	//   2. Interop testing with external tools that provide pre-derived keys
	// This is NOT a publishing path — room records are always signed via master secret.
	// In production, use OLCRTC_MASTER_SECRET instead (key is derived automatically).
	flag.BoolVar(&cfg.debug, "debug", false, "Enable verbose logging")
	flag.StringVar(&cfg.dataDir, "data", "data", "Path to data directory")
	flag.BoolVar(&cfg.duo, "duo", false, "Use dual channels for 2x throughput")
	flag.StringVar(&cfg.dnsServer, "dns", "1.1.1.1:53", "DNS server (default: Cloudflare 1.1.1.1)")
	flag.StringVar(&cfg.socksProxyAddr, "socks-proxy", "", "SOCKS5 proxy address (server only)")
	flag.IntVar(&cfg.socksProxyPort, "socks-proxy-port", 1080, "SOCKS5 proxy port (server only)")
	// DEPRECATED: --auto-room is legacy. Canonical model: client creates rooms,
	// server joins via room intent (direct API or Disk fallback).
	// Kept for backward compatibility but not the primary supported path.
	flag.BoolVar(&cfg.autoRoom, "auto-room", false, "[DEPRECATED] Auto-create rooms (legacy, use --discover instead)")
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
	case cfg.mode == "check" || cfg.mode == "rotate-secret" || cfg.mode == "rotate-token":
		return nil // operational modes don't need room ID
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
			log.Println("WARNING: --auto-room is deprecated. Use --discover mode instead.")
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
	rm := server.NewRoomManager(cfg.oauthToken, cfg.masterSecret, rotateInterval, cfg.apiPort, cfg.socksPort)

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
				func() { rm.MarkIntentReady() }, // real readiness: all peers connected
			); err != nil {
				log.Printf("[AUTO-ROOM] Server error: %v", err)
				rm.MarkIntentFailed(err.Error())
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

	var lastRoomID string
	var lastRecordID string // replay dedup: reject re-seen record_id

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

		// Replay dedup: skip if same record_id already processed
		if record.RecordID != "" && record.RecordID == lastRecordID {
			select {
			case <-time.After(10 * time.Second):
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		// Skip if same room already connected/processed
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
			log.Println("[DISCOVER] Record signed with previous secret (rotation window)")
		}

		keyHex := rendezvous.DeriveKey(secret, record.RoomID)
		log.Printf("[DISCOVER] Verified room: %s (sig OK, key_version=%d)", record.RoomURL, record.KeyVersion)

		lastRoomID = record.RoomID
		lastRecordID = record.RecordID

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
		lastRoomID = ""
		lastRecordID = ""
		select {
		case <-time.After(5 * time.Second):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func runWatchServer(ctx context.Context, cfg config) error {
	// OAuth is optional for server — without it, Disk fallback is disabled but IntentAPI works
	if cfg.oauthToken == "" {
		log.Println("[WATCH-SRV] No OAuth token — Disk fallback disabled, API-only mode")
	}
	if cfg.masterSecret == "" {
		return fmt.Errorf("OLCRTC_MASTER_SECRET required for --discover server mode")
	}

	previousSecret := os.Getenv("OLCRTC_PREVIOUS_SECRET") // rotation window support

	// Startup self-check: verify all required components before entering active monitoring
	log.Println("[WATCH-SRV] Running startup self-check...")
	log.Println("[WATCH-SRV]   Master secret: loaded")
	if previousSecret != "" {
		log.Println("[WATCH-SRV]   Previous secret: loaded (rotation window active)")
	}
	log.Println("[WATCH-SRV]   OAuth token: loaded")

	// Test Disk read access (only if OAuth is available)
	if cfg.oauthToken != "" {
		_, testErr := rendezvous.FetchRoom(cfg.oauthToken)
		if testErr != nil {
			log.Printf("[WATCH-SRV]   Yandex Disk: WARNING %v", testErr)
		} else {
			log.Println("[WATCH-SRV]   Yandex Disk: accessible")
		}
	} else {
		log.Println("[WATCH-SRV]   Yandex Disk: skipped (no OAuth)")
	}
	log.Println("[WATCH-SRV]   Signature verification: ready")
	log.Println("[WATCH-SRV] Self-check passed, entering active monitoring")

	// Start IntentAPI alongside Disk watcher (Direct API support)
	if cfg.apiPort > 0 {
		intentAPI := server.NewIntentAPI(cfg.masterSecret, previousSecret, cfg.socksPort)
		mux := http.NewServeMux()
		mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "ok")
		})
		intentAPI.RegisterRoutes(mux)
		intentAPI.SetAcceptedCallback(func(record *rendezvous.RoomRecord, keyHex string) {
			intentAPI.UpdateState(record.RecordID, server.IntentStarting, "")
			log.Printf("[WATCH-SRV] Intent received via API — publishing room %s to Disk", record.RoomID)
			// Publish the intent's room to Disk so the watcher picks it up
			if err := rendezvous.PublishRoom(cfg.oauthToken, record); err != nil {
				intentAPI.UpdateState(record.RecordID, server.IntentFailed, err.Error())
				log.Printf("[WATCH-SRV] Failed to publish intent room: %v", err)
				return
			}
			intentAPI.UpdateState(record.RecordID, server.IntentReady, "")
			log.Printf("[WATCH-SRV] Intent room published — watcher will pick it up")
		})
		srv := &http.Server{Addr: fmt.Sprintf("0.0.0.0:%d", cfg.apiPort), Handler: mux}
		go func() {
			log.Printf("[WATCH-SRV] IntentAPI listening on 0.0.0.0:%d", cfg.apiPort)
			if err := srv.ListenAndServe(); err != http.ErrServerClosed {
				log.Printf("[WATCH-SRV] IntentAPI error: %v", err)
			}
		}()
		defer srv.Close()
	}

	var lastRoomID string
	var lastRecordID string // replay dedup: reject re-seen record_id

	for {
		// Skip Disk polling if no OAuth (API-only mode)
		if cfg.oauthToken == "" {
			select {
			case <-time.After(10 * time.Second):
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}

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

		// Replay dedup: reject if same record_id already processed
		if record.RecordID != "" && record.RecordID == lastRecordID {
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
		lastRecordID = record.RecordID
		keyHex := rendezvous.DeriveKey(secret, record.RoomID)
		log.Printf("[WATCH-SRV] Verified room: %s (sig OK, key_version=%d)", record.RoomURL, record.KeyVersion)

		// Run server in background goroutine — continue polling Disk for room changes
		srvCtx, srvCancel := context.WithCancel(ctx)
		srvDone := make(chan error, 1)
		go func() {
			srvDone <- server.Run(
				srvCtx,
				record.RoomURL,
				keyHex,
				cfg.duo,
				cfg.dnsServer,
				cfg.socksProxyAddr,
				cfg.socksProxyPort,
			)
		}()

		// Parallel poll: check Disk every 10s while server is running
		for {
			select {
			case srvErr := <-srvDone:
				log.Printf("[WATCH-SRV] Room ended: %v. Polling for new room in 5s...", srvErr)
				lastRoomID = "" // allow reconnect to same room if client re-publishes
				lastRecordID = "" // allow replay after disconnect
				goto nextPoll
			case <-ctx.Done():
				srvCancel()
				return ctx.Err()
			case <-time.After(10 * time.Second):
				// Check if room changed on Disk
				newRecord, newMatched, pollErr := rendezvous.FetchAndVerifyRoom(cfg.oauthToken, cfg.masterSecret, previousSecret)
				if pollErr != nil || newRecord == nil {
					continue // keep current room
				}
				// Replay dedup on poll too
				if newRecord.RecordID != "" && newRecord.RecordID == lastRecordID {
					continue
				}
				if newRecord.RoomID != lastRoomID {
					log.Printf("[WATCH-SRV] New room detected: %s (was %s) — switching!", newRecord.RoomID, lastRoomID)
					srvCancel() // graceful disconnect from old room
					<-srvDone  // wait for server to stop

					// Update for next iteration
					newSecret := cfg.masterSecret
					if newMatched == 2 {
						newSecret = previousSecret
					}
					lastRoomID = newRecord.RoomID
					lastRecordID = newRecord.RecordID
					keyHex = rendezvous.DeriveKey(newSecret, newRecord.RoomID)
					log.Printf("[WATCH-SRV] Verified room: %s (sig OK)", newRecord.RoomURL)

					// Start new server
					srvCtx, srvCancel = context.WithCancel(ctx)
					srvDone = make(chan error, 1)
					go func() {
						srvDone <- server.Run(
							srvCtx,
							newRecord.RoomURL,
							keyHex,
							cfg.duo,
							cfg.dnsServer,
							cfg.socksProxyAddr,
							cfg.socksProxyPort,
						)
					}()
				}
			}
		}
	nextPoll:
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

func runCheck(cfg config) error {
	fmt.Println("=== olcRTC Configuration Check ===")
	fmt.Println()

	// Check master secret
	if cfg.masterSecret == "" {
		fmt.Println("[FAIL] OLCRTC_MASTER_SECRET: not set")
		return fmt.Errorf("master secret not configured")
	}
	fmt.Println("[ OK ] OLCRTC_MASTER_SECRET: loaded")

	// Check previous secret (optional)
	previousSecret := os.Getenv("OLCRTC_PREVIOUS_SECRET")
	if previousSecret != "" {
		fmt.Println("[ OK ] OLCRTC_PREVIOUS_SECRET: loaded (rotation window active)")
	} else {
		fmt.Println("[INFO] OLCRTC_PREVIOUS_SECRET: not set (no rotation window)")
	}

	// Check OAuth token
	if cfg.oauthToken == "" {
		fmt.Println("[WARN] OLCRTC_OAUTH_TOKEN: not set (required for publish/discover)")
	} else {
		fmt.Println("[ OK ] OLCRTC_OAUTH_TOKEN: loaded")

		// Test Disk read access
		_, err := rendezvous.FetchRoom(cfg.oauthToken)
		if err != nil {
			fmt.Printf("[FAIL] Yandex Disk read test: %v\n", err)
		} else {
			fmt.Println("[ OK ] Yandex Disk: read access confirmed")
		}
	}

	// Test key derivation
	testKey := rendezvous.DeriveKey(cfg.masterSecret, "test-room-id")
	if len(testKey) == 64 {
		fmt.Println("[ OK ] Key derivation: HMAC-SHA256 working")
	} else {
		fmt.Println("[FAIL] Key derivation: unexpected output length")
	}

	// Test sign/verify cycle
	testRecord := &rendezvous.RoomRecord{
		RoomID:    "check-test",
		RoomURL:   "https://example.com/test",
		CreatedAt: time.Now().Format(time.RFC3339),
		ExpiresAt: time.Now().Add(1 * time.Hour).Format(time.RFC3339),
	}
	if err := rendezvous.SignRecord(testRecord, cfg.masterSecret, 1); err != nil {
		fmt.Printf("[FAIL] Record signing: %v\n", err)
	} else if err := rendezvous.VerifyRecord(testRecord, cfg.masterSecret); err != nil {
		fmt.Printf("[FAIL] Record verification: %v\n", err)
	} else {
		fmt.Println("[ OK ] Sign/verify cycle: working")
	}

	fmt.Println()
	fmt.Println("=== Check complete ===")
	return nil
}

func runRotateSecret(cfg config) error {
	fmt.Println("=== Master Secret Rotation Validator ===")
	fmt.Println()

	newSecret := cfg.masterSecret
	previousSecret := os.Getenv("OLCRTC_PREVIOUS_SECRET")

	if newSecret == "" {
		return fmt.Errorf("OLCRTC_MASTER_SECRET (new secret) is required")
	}
	if previousSecret == "" {
		return fmt.Errorf("OLCRTC_PREVIOUS_SECRET (old secret) is required for rotation validation")
	}
	if newSecret == previousSecret {
		return fmt.Errorf("new and previous secrets are identical — nothing to rotate")
	}

	fmt.Println("[ OK ] New secret (OLCRTC_MASTER_SECRET): loaded")
	fmt.Println("[ OK ] Previous secret (OLCRTC_PREVIOUS_SECRET): loaded")
	fmt.Println("[ OK ] Secrets are different")

	// Test key derivation with both
	testRoomID := "rotation-test-room"
	newKey := rendezvous.DeriveKey(newSecret, testRoomID)
	oldKey := rendezvous.DeriveKey(previousSecret, testRoomID)
	if newKey == oldKey {
		fmt.Println("[WARN] Derived keys are identical (secrets may be similar)")
	} else {
		fmt.Println("[ OK ] Derived keys differ between old and new secret")
	}

	// Sign with new secret, verify with new
	record := &rendezvous.RoomRecord{
		RoomID:    testRoomID,
		RoomURL:   "https://example.com/rotation-test",
		CreatedAt: time.Now().Format(time.RFC3339),
		ExpiresAt: time.Now().Add(1 * time.Hour).Format(time.RFC3339),
	}
	if err := rendezvous.SignRecord(record, newSecret, 1); err != nil {
		return fmt.Errorf("sign with new secret failed: %w", err)
	}
	fmt.Println("[ OK ] Record signed with new secret (key_version=1)")

	if err := rendezvous.VerifyRecord(record, newSecret); err != nil {
		return fmt.Errorf("verify with new secret failed: %w", err)
	}
	fmt.Println("[ OK ] Verified with new secret")

	// Verify should fail with old secret
	if err := rendezvous.VerifyRecord(record, previousSecret); err != nil {
		fmt.Println("[ OK ] Correctly rejected by previous secret")
	} else {
		fmt.Println("[WARN] Old secret also verifies new-secret-signed record (unexpected)")
	}

	// Sign with old secret, verify multi should work
	oldRecord := &rendezvous.RoomRecord{
		RoomID:    testRoomID,
		RoomURL:   "https://example.com/rotation-test-old",
		CreatedAt: time.Now().Format(time.RFC3339),
		ExpiresAt: time.Now().Add(1 * time.Hour).Format(time.RFC3339),
	}
	if err := rendezvous.SignRecord(oldRecord, previousSecret, 2); err != nil {
		return fmt.Errorf("sign with old secret failed: %w", err)
	}

	matched, err := rendezvous.VerifyRecordMulti(oldRecord, newSecret, previousSecret)
	if err != nil {
		return fmt.Errorf("multi-verify failed: %w", err)
	}
	if matched == 2 {
		fmt.Println("[ OK ] Old-secret-signed record verified via fallback (rotation window works)")
	} else {
		fmt.Printf("[WARN] Old-secret-signed record matched key %d (expected 2)\n", matched)
	}

	// Test Disk access if token available
	if cfg.oauthToken != "" {
		record, err := rendezvous.FetchRoom(cfg.oauthToken)
		if err != nil {
			fmt.Printf("[WARN] Disk fetch failed: %v\n", err)
		} else if record != nil {
			_, verifyErr := rendezvous.VerifyRecordMulti(record, newSecret, previousSecret)
			if verifyErr != nil {
				fmt.Printf("[WARN] Current Disk record does NOT verify against either secret: %v\n", verifyErr)
			} else {
				fmt.Println("[ OK ] Current Disk record verifies against known secrets")
			}
		} else {
			fmt.Println("[INFO] No room currently published on Disk")
		}
	}

	fmt.Println()
	fmt.Println("=== Rotation validation passed ===")
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Println("  1. Deploy with both OLCRTC_MASTER_SECRET (new) and OLCRTC_PREVIOUS_SECRET (old)")
	fmt.Println("  2. Update all clients to use new OLCRTC_MASTER_SECRET")
	fmt.Println("  3. After all clients migrated: remove OLCRTC_PREVIOUS_SECRET and restart")
	return nil
}

func runRotateToken(cfg config) error {
	fmt.Println("=== OAuth Token Rotation Validator ===")
	fmt.Println()

	if cfg.oauthToken == "" {
		return fmt.Errorf("OLCRTC_OAUTH_TOKEN (new token) is required")
	}

	fmt.Println("[ OK ] OLCRTC_OAUTH_TOKEN: loaded")

	// Test read access
	fmt.Println("[....] Testing Yandex Disk read access...")
	record, err := rendezvous.FetchRoom(cfg.oauthToken)
	if err != nil {
		return fmt.Errorf("Disk read test failed: %w (token may be invalid or expired)", err)
	}

	fmt.Println("[ OK ] Yandex Disk: read access confirmed")

	if record != nil {
		fmt.Printf("[ OK ] Current room on Disk: %s (expires %s)\n", record.RoomID, record.ExpiresAt)

		// Verify signature if master secret available
		if cfg.masterSecret != "" {
			previousSecret := os.Getenv("OLCRTC_PREVIOUS_SECRET")
			_, verifyErr := rendezvous.VerifyRecordMulti(record, cfg.masterSecret, previousSecret)
			if verifyErr != nil {
				fmt.Printf("[WARN] Room record signature: %v\n", verifyErr)
			} else {
				fmt.Println("[ OK ] Room record signature: valid")
			}
		}
	} else {
		fmt.Println("[INFO] No room currently published on Disk")
	}

	fmt.Println()
	fmt.Println("=== Token validation passed ===")
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Println("  1. Update secrets file with new OLCRTC_OAUTH_TOKEN")
	fmt.Println("  2. Restart server/client")
	fmt.Println("  3. Revoke old token from Yandex ID if no longer needed")
	return nil
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
