// Package main provides the olcrtc multi-tenant bootstrap server.
// This is the shared bootstrap plane that manages tenant lifecycle.
// Each tenant gets its own runtime process (olcrtc binary).
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/openlibrecommunity/olcrtc/internal/server"
)

func main() {
	var (
		bootstrapPort int
		portStart     int
		portEnd       int
		stateDir      string
		binary        string
		dnsServer     string
	)

	flag.IntVar(&bootstrapPort, "port", 8080, "Bootstrap API listen port")
	flag.IntVar(&portStart, "port-start", 1080, "Start of tenant SOCKS port range")
	flag.IntVar(&portEnd, "port-end", 1180, "End of tenant SOCKS port range")
	flag.StringVar(&stateDir, "state-dir", "/opt/olcrtc/state", "Directory for tenant state persistence")
	flag.StringVar(&binary, "binary", "/opt/olcrtc", "Path to olcrtc runtime binary")
	flag.StringVar(&dnsServer, "dns", "1.1.1.1:53", "DNS server for tenant processes")
	flag.Parse()

	log.SetFlags(log.Ltime | log.Lshortfile)
	log.Println("[BOOTSTRAP] Starting multi-tenant bootstrap server...")
	log.Printf("[BOOTSTRAP] Port range: %d-%d", portStart, portEnd)
	log.Printf("[BOOTSTRAP] State dir: %s", stateDir)
	log.Printf("[BOOTSTRAP] Runtime binary: %s", binary)

	// Create tenant registry with port range
	registry := server.NewTenantRegistry(portStart, portEnd, stateDir)

	// Create process supervisor
	supervisor := server.NewSupervisor(registry, binary, dnsServer)

	// Set up HTTP mux with bootstrap API
	mux := http.NewServeMux()
	registry.RegisterBootstrapRoutes(mux)

	// Health endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "ok")
	})

	// Start HTTP server
	addr := fmt.Sprintf("0.0.0.0:%d", bootstrapPort)
	srv := &http.Server{Addr: addr, Handler: mux}

	// Graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

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
			log.Printf("[BOOTSTRAP] Tenant %s needs re-registration (secret in memory only)", tenant.TenantID)
		}
	}

	log.Printf("[BOOTSTRAP] Listening on %s", addr)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("[BOOTSTRAP] Server error: %v", err)
	}

	log.Println("[BOOTSTRAP] Stopped")
}
