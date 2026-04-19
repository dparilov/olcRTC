// Package main provides the olcrtc multi-tenant bootstrap server.
// This is the shared bootstrap plane that manages tenant lifecycle.
// Each tenant gets its own runtime process (olcrtc binary).
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/openlibrecommunity/olcrtc/internal/rendezvous"
	"github.com/openlibrecommunity/olcrtc/internal/server"
)

func main() {
	var (
		bootstrapPort   int
		portStart       int
		portEnd         int
		stateDir        string
		binary          string
		dnsServer       string
		encryptKey      string
		oauthClientID   string
		oauthClientSec  string
	)

	flag.IntVar(&bootstrapPort, "port", 8080, "Bootstrap API listen port")
	flag.IntVar(&portStart, "port-start", 1080, "Start of tenant SOCKS port range")
	flag.IntVar(&portEnd, "port-end", 1180, "End of tenant SOCKS port range")
	flag.StringVar(&stateDir, "state-dir", "/opt/olcrtc/state", "Directory for tenant state persistence")
	flag.StringVar(&binary, "binary", "/opt/olcrtc", "Path to olcrtc runtime binary")
	flag.StringVar(&dnsServer, "dns", "1.1.1.1:53", "DNS server for tenant processes")
	flag.StringVar(&encryptKey, "encrypt-key", "", "Passphrase for at-rest encryption of tenant secrets")
	flag.StringVar(&oauthClientID, "oauth-client-id", "", "Yandex OAuth app client ID")
	flag.StringVar(&oauthClientSec, "oauth-client-secret", "", "Yandex OAuth app client secret")
	flag.Parse()

	// Allow env vars as fallback for sensitive params
	if v := os.Getenv("OLCRTC_ENCRYPT_KEY"); v != "" && encryptKey == "" {
		encryptKey = v
	}
	if v := os.Getenv("OLCRTC_OAUTH_CLIENT_ID"); v != "" && oauthClientID == "" {
		oauthClientID = v
	}
	if v := os.Getenv("OLCRTC_OAUTH_CLIENT_SECRET"); v != "" && oauthClientSec == "" {
		oauthClientSec = v
	}

	log.SetFlags(log.Ltime | log.Lshortfile)
	log.Println("[BOOTSTRAP] Starting multi-tenant bootstrap server...")
	log.Printf("[BOOTSTRAP] Port range: %d-%d", portStart, portEnd)
	log.Printf("[BOOTSTRAP] State dir: %s", stateDir)
	log.Printf("[BOOTSTRAP] Runtime binary: %s", binary)
	if encryptKey != "" {
		log.Println("[BOOTSTRAP] At-rest encryption: ENABLED")
	} else {
		log.Println("[BOOTSTRAP] At-rest encryption: DISABLED (secrets memory-only)")
	}
	if oauthClientID != "" {
		log.Println("[BOOTSTRAP] OAuth automation: ENABLED")
	} else {
		log.Println("[BOOTSTRAP] OAuth automation: DISABLED (manual token only)")
	}

	// Create tenant registry with port range and encryption key
	registry := server.NewTenantRegistry(portStart, portEnd, stateDir, encryptKey)
	registry.OAuthClientID = oauthClientID
	registry.OAuthClientSecret = oauthClientSec

	// Create process supervisor
	supervisor := server.NewSupervisor(registry, binary, dnsServer)

	// Create v2 session manager with dynamic port pool and 30min TTL
	sessionMgr := server.NewSessionManager(portStart, portEnd, stateDir+"-v2-sessions", supervisor, registry)

	// Set up HTTP mux with bootstrap API
	mux := http.NewServeMux()
	registry.RegisterBootstrapRoutes(mux)
	registry.RegisterV2Routes(mux)
	registry.RegisterV2SessionRoutes(mux, sessionMgr)

	// Health endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "ok")
	})

	// Shared frontdoor: POST /api/room-intent routes to correct tenant by signature
	mux.HandleFunc("/api/room-intent", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusMethodNotAllowed)
			fmt.Fprintf(w, `{"status":"error","message":"POST required"}`)
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, `{"status":"error","message":"failed to read body"}`)
			return
		}

		var record rendezvous.RoomRecord
		if err := json.Unmarshal(body, &record); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, `{"status":"error","message":"invalid JSON"}`)
			return
		}

		tenant := registry.FindBySignature(func(secret string) error {
			return rendezvous.VerifyRecord(&record, secret)
		})
		if tenant == nil {
			log.Printf("[BOOTSTRAP] room-intent: no tenant matched signature")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprintf(w, `{"status":"invalid_signature","message":"no tenant matched signature"}`)
			return
		}
		if tenant.APIPort <= 0 {
			log.Printf("[BOOTSTRAP] room-intent: tenant %s has no runtime", tenant.TenantID)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintf(w, `{"status":"error","message":"tenant runtime not available"}`)
			return
		}

		targetURL := fmt.Sprintf("http://127.0.0.1:%d/api/room-intent", tenant.APIPort)
		log.Printf("[BOOTSTRAP] room-intent: routing to tenant %s (port %d)", tenant.TenantID, tenant.APIPort)
		resp, err := http.Post(targetURL, "application/json", bytes.NewReader(body))
		if err != nil {
			log.Printf("[BOOTSTRAP] room-intent: forward failed: %v", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprintf(w, `{"status":"error","message":"tenant runtime unreachable"}`)
			return
		}
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		w.Write(respBody)
	})

	// GET /api/room-intent/{record_id} — status polling proxy
	mux.HandleFunc("/api/room-intent/", func(w http.ResponseWriter, r *http.Request) {
		tenants := registry.AllTenants()
		for _, t := range tenants {
			if t.APIPort > 0 {
				targetURL := fmt.Sprintf("http://127.0.0.1:%d%s", t.APIPort, r.URL.Path)
				resp, err := http.Get(targetURL)
				if err != nil || resp.StatusCode == http.StatusNotFound {
					if resp != nil {
						resp.Body.Close()
					}
					continue
				}
				defer resp.Body.Close()
				respBody, _ := io.ReadAll(resp.Body)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(resp.StatusCode)
				w.Write(respBody)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, `{"status":"unknown","message":"record not found in any tenant"}`)
	})

	// Start HTTP server
	addr := fmt.Sprintf("0.0.0.0:%d", bootstrapPort)
	srv := &http.Server{Addr: addr, Handler: mux}

	// Graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start session cleanup loop (30min idle TTL)
	sessionMgr.StartCleanupLoop(ctx)

	// Auto-start tenant runtime on registration
	registry.OnRegistered = func(tenant *server.Tenant) {
		log.Printf("[BOOTSTRAP] Auto-starting tenant %s (port %d)", tenant.TenantID, tenant.SOCKSPort)
		if err := supervisor.StartTenant(ctx, tenant); err != nil {
			log.Printf("[BOOTSTRAP] Failed to start tenant %s: %v", tenant.TenantID, err)
		}
	}

	// Restart tenant runtime when OAuth token is attached
	registry.OnOAuthAttached = func(tenant *server.Tenant) {
		log.Printf("[BOOTSTRAP] Restarting tenant %s with OAuth token", tenant.TenantID)
		if err := supervisor.RestartTenant(ctx, tenant); err != nil {
			log.Printf("[BOOTSTRAP] Failed to restart tenant %s: %v", tenant.TenantID, err)
		}
	}

	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		log.Println("[BOOTSTRAP] Shutting down...")
		supervisor.StopAll()
		srv.Close()
		cancel()
	}()

	// Auto-start existing tenants from state file
	for _, tenant := range registry.AllTenants() {
		if tenant.Secret != "" {
			log.Printf("[BOOTSTRAP] Auto-starting tenant %s (port %d)", tenant.TenantID, tenant.SOCKSPort)
			if err := supervisor.StartTenant(ctx, tenant); err != nil {
				log.Printf("[BOOTSTRAP] Failed to auto-start %s: %v", tenant.TenantID, err)
			}
		} else {
			log.Printf("[BOOTSTRAP] Tenant %s needs re-registration (secret not recovered)", tenant.TenantID)
		}
	}

	log.Printf("[BOOTSTRAP] Listening on %s", addr)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("[BOOTSTRAP] Server error: %v", err)
	}

	log.Println("[BOOTSTRAP] Stopped")
}
