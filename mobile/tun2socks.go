package mobile

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"

	"github.com/xjasonlyu/tun2socks/v2/engine"
)

var (
	tunCtx    context.Context
	tunCancel context.CancelFunc
	tunMu     sync.Mutex
	tunActive bool
)

// StartTun starts tun2socks: routes all TUN traffic through SOCKS5 proxy.
// fd is the TUN file descriptor from Android VpnService.
// socksPort is the local SOCKS5 proxy port (typically 1080).
// Blocks until StopTun is called.
func StartTun(fd, socksPort int64) error {
	tunMu.Lock()
	if tunActive {
		tunMu.Unlock()
		return fmt.Errorf("tun2socks already running")
	}
	tunActive = true
	tunCtx, tunCancel = context.WithCancel(context.Background())
	tunMu.Unlock()

	defer func() {
		tunMu.Lock()
		tunActive = false
		tunMu.Unlock()
	}()

	proxy := fmt.Sprintf("socks5://127.0.0.1:%d", socksPort)
	log.Printf("[tun2socks] Starting: fd=%d proxy=%s", fd, proxy)

	// Create TUN device from fd
	tunFile := os.NewFile(uintptr(fd), "tun")
	if tunFile == nil {
		return fmt.Errorf("invalid TUN fd: %d", fd)
	}

	key := new(engine.Key)
	key.Proxy = proxy
	key.Device = fmt.Sprintf("fd://%d", fd)
	key.LogLevel = "info"
	key.Interface = ""
	key.TCPSendBufferSize = ""
	key.TCPReceiveBufferSize = ""
	key.UDPTimeout = 0

	log.Printf("[tun2socks] engine.Insert: proxy=%s device=%s", key.Proxy, key.Device)
	engine.Insert(key)

	log.Println("[tun2socks] engine.Start...")
	engine.Start()

	log.Println("[tun2socks] Running — waiting for stop signal")

	// Block until context cancelled
	<-tunCtx.Done()

	engine.Stop()
	log.Println("[tun2socks] Stopped")
	return nil
}

// StopTun stops the tun2socks engine.
func StopTun() {
	tunMu.Lock()
	defer tunMu.Unlock()
	if tunCancel != nil {
		log.Println("[tun2socks] Stopping...")
		tunCancel()
	}
}
