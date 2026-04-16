// Package mobile provides a gomobile-compatible API for olcRTC.
// Build with: gomobile bind -target=android ./mobile
package mobile

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/openlibrecommunity/olcrtc/internal/client"
	"github.com/openlibrecommunity/olcrtc/internal/logger"
	"github.com/openlibrecommunity/olcrtc/internal/protect"
	"github.com/openlibrecommunity/olcrtc/internal/rendezvous"
)

// SocketProtector protects sockets from VPN routing on Android.
// Implement this interface in Kotlin/Java and pass to SetProtector.
type SocketProtector interface {
	Protect(fd int) bool
}

// LogWriter receives log messages from olcRTC.
type LogWriter interface {
	WriteLog(msg string)
}

var (
	errAlreadyRunning     = errors.New("olcRTC already running")
	errRoomIDRequired     = errors.New("roomID is required")
	errKeyHexRequired     = errors.New("keyHex is required")
	errNotRunning         = errors.New("olcRTC is not running")
	errStoppedBeforeReady = errors.New("olcRTC stopped before becoming ready")
	errStartTimedOut      = errors.New("olcRTC start timed out")
)

//nolint:gochecknoglobals // Mobile bindings expose a singleton runtime controlled by the embedding app.
var (
	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
	ready  chan struct{}
	errRun error
)

// SetProtector sets the Android VPN socket protector.
// Must be called before Start.
func SetProtector(p SocketProtector) {
	if p == nil {
		protect.Protector = nil
		return
	}
	protect.Protector = func(fd int) bool {
		return p.Protect(fd)
	}
}

// SetLogWriter sets a custom log writer for olcRTC output.
func SetLogWriter(w LogWriter) {
	if w != nil {
		log.SetOutput(&logBridge{w: w})
	}
}

// SetDebug enables or disables verbose logging.
func SetDebug(enabled bool) {
	logger.SetVerbose(enabled)
	if enabled {
		log.SetFlags(log.Ltime | log.Lshortfile)
		return
	}

	log.SetFlags(log.Ltime)
}

// Start launches the olcRTC client in background.
// roomID: Telemost room ID (e.g. "xxx-xxx-xxx")
// keyHex: 64-char hex encryption key
// socksPort: local SOCKS5 proxy port (e.g. 10808)
// duo: use dual channels for higher throughput
// socksUser/socksPass: SOCKS5 credentials (empty = no auth).
func Start(roomID, keyHex string, socksPort int, duo bool, socksUser, socksPass string) error {
	mu.Lock()
	defer mu.Unlock()

	switch {
	case cancel != nil:
		return errAlreadyRunning
	case roomID == "":
		return errRoomIDRequired
	case keyHex == "":
		return errKeyHexRequired
	}

	roomURL := "https://telemost.yandex.ru/j/" + roomID

	ctx, cancelFunc := context.WithCancel(context.Background())
	cancel = cancelFunc
	done = make(chan struct{})
	ready = make(chan struct{})
	localReady := ready
	errRun = nil

	var readyOnce sync.Once
	go func() {
		defer cancelFunc()

		err := client.RunWithReady(
			ctx,
			roomURL,
			keyHex,
			socksPort,
			duo,
			"",
			socksUser,
			socksPass,
			func() {
				readyOnce.Do(func() {
					close(localReady)
				})
			},
		)

		mu.Lock()
		cancel = nil
		errRun = err
		mu.Unlock()
		close(done)
	}()

	return nil
}

// WaitReady blocks until the Telemost peers are connected and the local SOCKS5 listener is ready.
//
//nolint:cyclop // The control flow is intentionally linear so mobile callers can observe each startup state clearly.
func WaitReady(timeoutMillis int) error {
	mu.Lock()
	r := ready
	d := done
	runErr := errRun
	running := cancel != nil
	mu.Unlock()

	if r == nil {
		if runErr != nil {
			return runErr
		}

		return errNotRunning
	}

	select {
	case <-r:
		return nil
	default:
	}

	if !running {
		if runErr != nil {
			return runErr
		}

		return errStoppedBeforeReady
	}

	timer := time.NewTimer(time.Duration(timeoutMillis) * time.Millisecond)
	defer timer.Stop()

	select {
	case <-r:
		return nil
	case <-d:
		mu.Lock()
		runErr = errRun
		mu.Unlock()
		if runErr != nil {
			return runErr
		}

		return errStoppedBeforeReady
	case <-timer.C:
		return errStartTimedOut
	}
}

// Stop gracefully stops the olcRTC client.
func Stop() {
	mu.Lock()
	cancelFunc := cancel
	doneCh := done
	mu.Unlock()

	if cancelFunc == nil {
		return
	}

	cancelFunc()

	if doneCh != nil {
		<-doneCh
	}
}

// IsRunning returns true if the olcRTC client is active.
func IsRunning() bool {
	mu.Lock()
	defer mu.Unlock()
	return cancel != nil
}

// PublishRoomToDisk publishes a signed v2 room record to Yandex Disk for server discovery.
// oauthToken: Yandex OAuth token with disk:app_folder scope
// masterSecret: shared setup secret used to sign the room record (MUST NOT be empty)
// roomID: Telemost room ID
// expireHours: how long the room record is valid
func PublishRoomToDisk(oauthToken, masterSecret, roomID string, expireHours int) error {
	if oauthToken == "" || roomID == "" {
		return errors.New("oauthToken and roomID are required")
	}
	if masterSecret == "" {
		return errors.New("masterSecret is required for signing room records")
	}

	record := &rendezvous.RoomRecord{
		RoomID:    roomID,
		RoomURL:   "https://telemost.yandex.ru/j/" + roomID,
		CreatedAt: time.Now().Format(time.RFC3339),
		ExpiresAt: time.Now().Add(time.Duration(expireHours) * time.Hour).Format(time.RFC3339),
	}

	// Sign the record with master secret (sets Version=2, KeyVersion, RecordID, Sig)
	if err := rendezvous.SignRecord(record, masterSecret, 1); err != nil {
		return errors.New("failed to sign room record: " + err.Error())
	}

	return rendezvous.PublishRoom(oauthToken, record)
}

// FetchRoomFromDisk reads and verifies the active room record from Yandex Disk.
// Returns room ID string, or empty string if no room published.
// masterSecret: current master secret for signature verification
// previousSecret: previous master secret (empty string if no rotation window)
func FetchRoomFromDisk(oauthToken, masterSecret, previousSecret string) (string, error) {
	if oauthToken == "" {
		return "", errors.New("oauthToken is required")
	}
	if masterSecret == "" {
		return "", errors.New("masterSecret is required for record verification")
	}

	record, _, err := rendezvous.FetchAndVerifyRoom(oauthToken, masterSecret, previousSecret)
	if err != nil {
		return "", err
	}
	if record == nil {
		return "", nil
	}

	return record.RoomID, nil
}

// DeriveKeyFromSecret computes deterministic encryption key from master secret + room ID.
// Returns 64-char hex string. Both client and server compute the same key.
func DeriveKeyFromSecret(masterSecret, roomID string) string {
	return rendezvous.DeriveKey(masterSecret, roomID)
}

// logBridge adapts LogWriter to io.Writer for log package.
type logBridge struct {
	w LogWriter
}

func (b *logBridge) Write(p []byte) (int, error) {
	b.w.WriteLog(string(p))
	return len(p), nil
}
