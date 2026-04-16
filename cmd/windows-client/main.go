package main

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"github.com/openlibrecommunity/olcrtc/internal/diagnostics"
	"github.com/openlibrecommunity/olcrtc/mobile"
)

const (
	defaultSOCKSHost       = "127.0.0.1"
	defaultSOCKSPort       = 10808
	defaultReadyWait       = 60_000
	defaultDiagnosticsWait = 35 * time.Second
	// defaultTunnelKey removed — keys must come from master-secret derivation or explicit user input
	desktopWindowName      = "olcRTC Windows Client"
)

var (
	telemostURLPattern = regexp.MustCompile(`https://telemost\.yandex(?:\.ru|\.com)/j/([A-Za-z0-9_-]+)`)
	roomIDPattern      = regexp.MustCompile(`^[A-Za-z0-9_-]{6,}$`)
)

type desktopApp struct {
	window           fyne.Window
	roomEntry        *widget.Entry
	meetingLabel     *widget.Label
	statusLabel      *widget.Label
	runtimeLabel     *widget.Label
	socksLabel       *widget.Label
	diagnosticsLabel *widget.Label
	logEntry         *widget.Entry
	launchButton     *widget.Button
	stopButton       *widget.Button
	diagButton       *widget.Button
	copyButton       *widget.Button

	logMu     sync.Mutex
	logBuffer strings.Builder

	stateMu        sync.Mutex
	lifecycleToken uint64
	activeRoomID   string
	diagActive     bool
	diagCancel     context.CancelFunc
}

func main() {
	a := app.NewWithID("github.com.openlibrecommunity.olcrtc.windowsclient")
	w := a.NewWindow(desktopWindowName)
	w.Resize(fyne.NewSize(900, 680))

	ui := newDesktopApp(w)
	w.SetContent(ui.content())
	w.SetOnClosed(ui.shutdown)

	mobile.SetDebug(true)
	mobile.SetLogWriter(ui)
	ui.setMeeting("No meeting selected")
	ui.setStatus("Idle")
	ui.setRuntime("No active tunnel session")
	ui.setDiagnostics("Diagnostics have not run yet")
	ui.appendLog("Windows client UI initialized")
	ui.appendLog(fmt.Sprintf("Local SOCKS endpoint fixed at %s:%d", defaultSOCKSHost, defaultSOCKSPort))

	w.ShowAndRun()
}

func newDesktopApp(window fyne.Window) *desktopApp {
	roomEntry := widget.NewEntry()
	roomEntry.SetPlaceHolder("Telemost link or room ID")

	logEntry := widget.NewMultiLineEntry()
	logEntry.SetMinRowsVisible(16)
	logEntry.Wrapping = fyne.TextWrapWord
	logEntry.Disable()

	ui := &desktopApp{
		window:           window,
		roomEntry:        roomEntry,
		meetingLabel:     widget.NewLabel("No meeting selected"),
		statusLabel:      widget.NewLabel("Idle"),
		runtimeLabel:     widget.NewLabel("No active tunnel session"),
		socksLabel:       widget.NewLabel(fmt.Sprintf("%s:%d", defaultSOCKSHost, defaultSOCKSPort)),
		diagnosticsLabel: widget.NewLabel("Diagnostics have not run yet"),
		logEntry:         logEntry,
	}

	ui.launchButton = widget.NewButton("Launch tunnel", ui.launchTunnel)
	ui.stopButton = widget.NewButton("Stop", ui.stopTunnel)
	ui.diagButton = widget.NewButton("Run diagnostics", ui.runDiagnosticsManually)
	ui.copyButton = widget.NewButton("Copy log", ui.copyLog)
	ui.stopButton.Disable()
	ui.diagButton.Disable()

	return ui
}

func (d *desktopApp) content() fyne.CanvasObject {
	form := container.NewVBox(
		widget.NewLabel("Room or invite link"),
		d.roomEntry,
		widget.NewLabel("Meeting"),
		d.meetingLabel,
		widget.NewLabel("Status"),
		d.statusLabel,
		widget.NewLabel("Runtime"),
		d.runtimeLabel,
		widget.NewLabel("SOCKS endpoint"),
		d.socksLabel,
		widget.NewLabel("Diagnostics"),
		d.diagnosticsLabel,
		container.NewHBox(
			d.launchButton,
			d.stopButton,
			d.diagButton,
			layout.NewSpacer(),
			d.copyButton,
		),
		widget.NewSeparator(),
		widget.NewLabel("Log"),
		d.logEntry,
	)

	return container.NewPadded(form)
}

func (d *desktopApp) WriteLog(msg string) {
	line := strings.TrimSpace(msg)
	d.appendLog(strings.TrimRight(msg, "\n"))
	if line == "" {
		return
	}

	switch {
	case strings.Contains(line, "Reconnecting..."):
		d.setStatus("Reconnecting")
		d.setRuntime("Telemost data channel dropped; waiting for reconnect while keeping the local SOCKS endpoint")
		d.setDiagnostics("Diagnostics paused during reconnect")
	case strings.Contains(line, "Reconnected successfully"):
		d.setStatus("SOCKS ready")
		d.setRuntime(d.readyRuntimeDescription())
		d.setDiagnostics("Diagnostics available after reconnect")
	case strings.Contains(line, "SOCKS5 proxy listening on"):
		d.setRuntime("Local SOCKS listener is accepting connections while Telemost session finishes handshaking")
	}
}

func (d *desktopApp) launchTunnel() {
	roomID := parseRoomID(d.roomEntry.Text)
	if roomID == "" {
		d.setStatus("Invalid meeting link")
		d.setRuntime("Paste a Telemost invite link or a raw room ID")
		d.appendLog("Launch rejected: input does not contain a valid Telemost room ID")
		return
	}

	if mobile.IsRunning() {
		d.appendLog("Launch rejected: runtime already running")
		d.setStatus("Already running")
		d.setRuntime(d.readyRuntimeDescription())
		return
	}

	token := d.beginLaunch(roomID)
	d.appendLog("Launch requested for room=" + roomID)

	go d.startTunnel(token, roomID)
}

func (d *desktopApp) startTunnel(token uint64, roomID string) {
	keyHex := d.resolveKey(roomID)
	if keyHex == "" {
		d.finishLaunchWithError(token, "No encryption key: configure Master Secret or Encryption Key in settings")
		return
	}
	if err := mobile.Start(roomID, keyHex, defaultSOCKSPort, false, "", ""); err != nil {
		d.finishLaunchWithError(token, "Start failed: "+err.Error())
		return
	}

	if !d.isCurrentToken(token) {
		if mobile.IsRunning() {
			mobile.Stop()
		}
		return
	}

	d.setStatus("Connecting to Telemost")
	d.setRuntime("Runtime started; waiting for Telemost peers and local SOCKS readiness")

	if err := mobile.WaitReady(defaultReadyWait); err != nil {
		d.finishLaunchWithError(token, "WaitReady failed: "+err.Error())
		return
	}

	if !d.isCurrentToken(token) {
		if mobile.IsRunning() {
			mobile.Stop()
		}
		return
	}

	d.setStatus("SOCKS ready")
	d.setRuntime(d.readyRuntimeDescription())
	d.setDiagnostics("Automatic diagnostics queued")
	d.updateButtons(true, false)
	d.appendLog(fmt.Sprintf("Tunnel ready on %s:%d", defaultSOCKSHost, defaultSOCKSPort))
	d.startDiagnostics("startup validation")
}

func (d *desktopApp) finishLaunchWithError(token uint64, line string) {
	if !d.isCurrentToken(token) {
		return
	}

	status := "Error"
	runtime := "Tunnel did not reach SOCKS ready"
	if !mobile.IsRunning() {
		status = "Stopped"
		runtime = "Tunnel stopped before startup completed"
	}

	d.setStatus(status)
	d.setRuntime(runtime)
	d.setDiagnostics("Diagnostics unavailable")
	d.updateButtons(false, false)
	d.appendLog(line)
}

func (d *desktopApp) stopTunnel() {
	roomID := d.currentRoomID()
	token := d.bumpLifecycleToken(roomID)
	d.cancelDiagnostics("Diagnostics interrupted: tunnel stopping", true)
	d.setStatus("Stopping")

	if roomID == "" {
		d.setRuntime("Stopping current tunnel session")
	} else {
		d.setRuntime("Stopping tunnel for room " + roomID)
	}

	d.setDiagnostics("Diagnostics stopped")
	d.updateButtons(false, true)
	d.appendLog("Stop requested")

	go func(stopToken uint64) {
		mobile.Stop()
		if !d.isCurrentToken(stopToken) {
			return
		}

		d.setStatus("Stopped")
		d.setRuntime("Tunnel stopped cleanly")
		d.updateButtons(false, false)
		d.appendLog("Tunnel stopped")
	}(token)
}

func (d *desktopApp) runDiagnosticsManually() {
	d.startDiagnostics("manual rerun")
}

func (d *desktopApp) startDiagnostics(reason string) {
	if !mobile.IsRunning() {
		d.setDiagnostics("Diagnostics skipped: tunnel not ready")
		d.appendLog("Diagnostics skipped: runtime is not running")
		return
	}

	ctx, token, ok := d.beginDiagnostics()
	if !ok {
		d.appendLog("Diagnostics request ignored: run already in progress")
		return
	}

	d.setDiagnostics("Diagnostics running")
	d.diagButton.Disable()
	d.appendLog("Diagnostics started (" + reason + ")")

	go func(diagToken uint64, diagCtx context.Context) {
		defer d.finishDiagnostics(diagToken)

		report := diagnostics.RunAll(diagCtx, defaultSOCKSHost, defaultSOCKSPort)
		if diagCtx.Err() != nil {
			d.setDiagnostics("Diagnostics interrupted")
			d.appendLog("Diagnostics interrupted: " + diagCtx.Err().Error())
			return
		}

		if !mobile.IsRunning() {
			d.setDiagnostics("Diagnostics discarded: tunnel stopped")
			d.appendLog("Diagnostics results discarded: runtime stopped before completion")
			return
		}

		d.setDiagnostics("Diagnostics finished: " + summarizeDiagnostics(report))
		d.appendLog(report)
	}(token, ctx)
}

func (d *desktopApp) beginDiagnostics() (context.Context, uint64, bool) {
	d.stateMu.Lock()
	defer d.stateMu.Unlock()

	if d.diagActive {
		return nil, 0, false
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultDiagnosticsWait)
	d.diagActive = true
	d.diagCancel = cancel
	return ctx, d.lifecycleToken, true
}

func (d *desktopApp) finishDiagnostics(token uint64) {
	d.stateMu.Lock()
	defer d.stateMu.Unlock()

	if token != d.lifecycleToken {
		return
	}

	d.diagActive = false
	d.diagCancel = nil
	fyne.Do(func() {
		if mobile.IsRunning() {
			d.diagButton.Enable()
		}
	})
}

func (d *desktopApp) cancelDiagnostics(reason string, logReason bool) {
	d.stateMu.Lock()
	cancel := d.diagCancel
	active := d.diagActive
	d.stateMu.Unlock()

	if cancel != nil {
		cancel()
	}

	if active && logReason {
		d.appendLog(reason)
	}
}

func (d *desktopApp) copyLog() {
	d.logMu.Lock()
	content := d.logBuffer.String()
	d.logMu.Unlock()

	d.window.Clipboard().SetContent(content)
	d.appendLog("Log copied to clipboard")
}

func (d *desktopApp) appendLog(line string) {
	if strings.TrimSpace(line) == "" {
		return
	}

	d.logMu.Lock()
	if d.logBuffer.Len() > 0 {
		d.logBuffer.WriteString("\n")
	}
	d.logBuffer.WriteString(line)
	text := d.logBuffer.String()
	d.logMu.Unlock()

	fyne.Do(func() {
		d.logEntry.SetText(text)
	})
}

func (d *desktopApp) setMeeting(text string) {
	fyne.Do(func() {
		d.meetingLabel.SetText(text)
	})
}

func (d *desktopApp) setStatus(status string) {
	fyne.Do(func() {
		d.statusLabel.SetText(status)
	})
}

func (d *desktopApp) setRuntime(text string) {
	fyne.Do(func() {
		d.runtimeLabel.SetText(text)
	})
}

func (d *desktopApp) setDiagnostics(status string) {
	fyne.Do(func() {
		d.diagnosticsLabel.SetText(status)
	})
}

func (d *desktopApp) updateButtons(running bool, busy bool) {
	fyne.Do(func() {
		if running || busy {
			d.launchButton.Disable()
		} else {
			d.launchButton.Enable()
		}

		if busy {
			d.stopButton.Disable()
			d.diagButton.Disable()
			return
		}

		if running {
			d.stopButton.Enable()
			d.diagButton.Enable()
			return
		}

		d.stopButton.Disable()
		d.diagButton.Disable()
	})
}

func (d *desktopApp) beginLaunch(roomID string) uint64 {
	token := d.bumpLifecycleToken(roomID)
	d.cancelDiagnostics("Diagnostics interrupted: new launch requested", false)
	d.roomEntry.SetText(roomID)
	d.setMeeting(roomID)
	d.setStatus("Starting tunnel")
	d.setRuntime("Initializing Telemost client runtime for room " + roomID)
	d.setDiagnostics("Diagnostics available after SOCKS becomes ready")
	d.updateButtons(false, true)
	return token
}

func (d *desktopApp) bumpLifecycleToken(roomID string) uint64 {
	d.stateMu.Lock()
	defer d.stateMu.Unlock()

	d.lifecycleToken++
	d.activeRoomID = roomID
	return d.lifecycleToken
}

func (d *desktopApp) isCurrentToken(token uint64) bool {
	d.stateMu.Lock()
	defer d.stateMu.Unlock()
	return token == d.lifecycleToken
}

func (d *desktopApp) currentRoomID() string {
	d.stateMu.Lock()
	defer d.stateMu.Unlock()
	return d.activeRoomID
}

func (d *desktopApp) readyRuntimeDescription() string {
	roomID := d.currentRoomID()
	if roomID == "" {
		return fmt.Sprintf("Tunnel active and SOCKS endpoint ready at %s:%d", defaultSOCKSHost, defaultSOCKSPort)
	}

	return fmt.Sprintf("Room %s connected; SOCKS endpoint ready at %s:%d", roomID, defaultSOCKSHost, defaultSOCKSPort)
}

// resolveKey returns the tunnel encryption key.
// Priority: 1) HMAC(master_secret, roomID) if master secret set
//           2) explicit encryption key from config
//           3) empty string (fail-closed)
func (d *desktopApp) resolveKey(roomID string) string {
	// TODO: read master secret and encryption key from desktopApp config/prefs
	// For now, check environment variables as a secure fallback
	if ms := os.Getenv("OLCRTC_MASTER_SECRET"); ms != "" {
		return mobile.DeriveKeyFromSecret(ms, roomID)
	}
	if key := os.Getenv("OLCRTC_KEY"); key != "" {
		return key
	}
	return "" // fail-closed: no key available
}

func (d *desktopApp) shutdown() {
	d.cancelDiagnostics("Diagnostics interrupted: application closing", false)
	mobile.Stop()
}

func summarizeDiagnostics(report string) string {
	lines := strings.Split(report, "\n")
	total := 0
	failed := 0

	for _, line := range lines {
		if !strings.Contains(line, "->") {
			continue
		}

		total++
		if strings.Contains(line, "FAILED:") {
			failed++
		}
	}

	if total == 0 {
		return "no probes executed"
	}

	if failed == 0 {
		return fmt.Sprintf("%d/%d probes passed", total, total)
	}

	return fmt.Sprintf("%d/%d probes passed", total-failed, total)
}

func parseRoomID(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}

	if roomIDPattern.MatchString(value) {
		return value
	}

	match := telemostURLPattern.FindStringSubmatch(value)
	if len(match) == 2 {
		return match[1]
	}

	return ""
}
