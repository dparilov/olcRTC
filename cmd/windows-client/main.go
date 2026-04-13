package main

import (
	"context"
	"fmt"
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
	defaultSOCKSHost  = "127.0.0.1"
	defaultSOCKSPort  = 1080
	defaultReadyWait  = 30_000
	defaultTunnelKey  = "d9d528926ca69ef9d422fcdd010cc27c8cd2c3ae37aa21927e2b3f8c59a921f3"
	desktopWindowName = "olcRTC Windows Client"
)

var (
	telemostURLPattern = regexp.MustCompile(`https://telemost\.yandex(?:\.ru|\.com)/j/([A-Za-z0-9_-]+)`)
	roomIDPattern      = regexp.MustCompile(`^[A-Za-z0-9_-]{6,}$`)
)

type desktopApp struct {
	window           fyne.Window
	roomEntry        *widget.Entry
	statusLabel      *widget.Label
	socksLabel       *widget.Label
	diagnosticsLabel *widget.Label
	logEntry         *widget.Entry
	launchButton     *widget.Button
	stopButton       *widget.Button
	diagButton       *widget.Button
	copyButton       *widget.Button

	logMu      sync.Mutex
	logBuffer  strings.Builder
	statusMu   sync.Mutex
	diagMu     sync.Mutex
	diagActive bool
}

func main() {
	a := app.NewWithID("github.com.openlibrecommunity.olcrtc.windowsclient")
	w := a.NewWindow(desktopWindowName)
	w.Resize(fyne.NewSize(860, 620))

	ui := newDesktopApp(w)
	w.SetContent(ui.content())
	w.SetOnClosed(func() {
		mobile.Stop()
	})

	mobile.SetDebug(true)
	mobile.SetLogWriter(ui)
	ui.setStatus("Idle")
	ui.setDiagnostics("Diagnostics have not run yet")
	ui.appendLog("Windows client UI initialized")

	w.ShowAndRun()
}

func newDesktopApp(window fyne.Window) *desktopApp {
	roomEntry := widget.NewEntry()
	roomEntry.SetPlaceHolder("Telemost link or room ID")

	logEntry := widget.NewMultiLineEntry()
	logEntry.SetMinRowsVisible(14)
	logEntry.Wrapping = fyne.TextWrapWord
	logEntry.Disable()

	ui := &desktopApp{
		window:           window,
		roomEntry:        roomEntry,
		statusLabel:      widget.NewLabel("Idle"),
		socksLabel:       widget.NewLabel(fmt.Sprintf("%s:%d", defaultSOCKSHost, defaultSOCKSPort)),
		diagnosticsLabel: widget.NewLabel("Diagnostics have not run yet"),
		logEntry:         logEntry,
	}

	ui.launchButton = widget.NewButton("Launch tunnel", ui.launchTunnel)
	ui.stopButton = widget.NewButton("Stop", ui.stopTunnel)
	ui.diagButton = widget.NewButton("Run diagnostics again", ui.runDiagnostics)
	ui.copyButton = widget.NewButton("Copy log", ui.copyLog)
	ui.stopButton.Disable()
	ui.diagButton.Disable()

	return ui
}

func (d *desktopApp) content() fyne.CanvasObject {
	form := container.NewVBox(
		widget.NewLabel("Room or invite link"),
		d.roomEntry,
		widget.NewLabel("Status"),
		d.statusLabel,
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
	d.appendLog(strings.TrimRight(msg, "\n"))

	switch {
	case strings.Contains(msg, "Reconnecting..."):
		d.setStatus("Reconnecting")
		d.setDiagnostics("Diagnostics paused during reconnect")
	case strings.Contains(msg, "Reconnected successfully"):
		d.setStatus("SOCKS ready")
		d.setDiagnostics("Diagnostics available after reconnect")
	}
}

func (d *desktopApp) launchTunnel() {
	roomID := parseRoomID(d.roomEntry.Text)
	if roomID == "" {
		d.setStatus("Invalid meeting link")
		d.appendLog("Launch rejected: input does not contain a valid Telemost room ID")
		return
	}

	if mobile.IsRunning() {
		d.appendLog("Launch rejected: runtime already running")
		d.setStatus("Already running")
		return
	}

	d.roomEntry.SetText(roomID)
	d.setStatus("Starting tunnel")
	d.setDiagnostics("Diagnostics available after SOCKS becomes ready")
	d.updateButtons(false, true)
	d.appendLog("Launch requested for room=" + roomID)

	go func() {
		if err := mobile.Start(roomID, defaultTunnelKey, defaultSOCKSPort, false, "", ""); err != nil {
			d.setStatus("Error")
			d.updateButtons(false, false)
			d.appendLog("Start failed: " + err.Error())
			return
		}

		d.setStatus("Connecting to Telemost")
		d.appendLog("Tunnel started, waiting for local SOCKS readiness")

		if err := mobile.WaitReady(defaultReadyWait); err != nil {
			d.setStatus("Error")
			d.updateButtons(false, false)
			d.appendLog("WaitReady failed: " + err.Error())
			return
		}

		d.setStatus("SOCKS ready")
		d.setDiagnostics("Diagnostics available")
		d.updateButtons(true, false)
		d.appendLog(fmt.Sprintf("Tunnel ready on %s:%d", defaultSOCKSHost, defaultSOCKSPort))
	}()
}

func (d *desktopApp) stopTunnel() {
	d.setStatus("Stopping")
	d.appendLog("Stop requested")
	d.updateButtons(true, true)

	go func() {
		mobile.Stop()
		d.setStatus("Stopped")
		d.setDiagnostics("Diagnostics stopped")
		d.updateButtons(false, false)
		d.appendLog("Tunnel stopped")
	}()
}

func (d *desktopApp) runDiagnostics() {
	if !mobile.IsRunning() {
		d.setDiagnostics("Diagnostics skipped: tunnel not ready")
		d.appendLog("Diagnostics skipped: runtime is not running")
		return
	}

	d.diagMu.Lock()
	if d.diagActive {
		d.diagMu.Unlock()
		d.appendLog("Diagnostics request ignored: run already in progress")
		return
	}
	d.diagActive = true
	d.diagMu.Unlock()

	d.setDiagnostics("Diagnostics running")
	d.diagButton.Disable()
	d.appendLog("Diagnostics started")

	go func() {
		defer func() {
			d.diagMu.Lock()
			d.diagActive = false
			d.diagMu.Unlock()
			fyne.Do(func() {
				if mobile.IsRunning() {
					d.diagButton.Enable()
				}
			})
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()

		report := diagnostics.RunAll(ctx, defaultSOCKSHost, defaultSOCKSPort)
		if !mobile.IsRunning() {
			d.setDiagnostics("Diagnostics interrupted")
			d.appendLog("Diagnostics results discarded: runtime stopped before completion")
			return
		}

		d.setDiagnostics("Diagnostics finished")
		d.appendLog(report)
	}()
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

func (d *desktopApp) setStatus(status string) {
	d.statusMu.Lock()
	defer d.statusMu.Unlock()

	fyne.Do(func() {
		d.statusLabel.SetText(status)
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
