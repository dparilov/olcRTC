package main

import (
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

func (p *Program) settingsWindow() {
	log("Opening settings dialog...")

	// --- Security Settings ---
	masterSecret := widget.NewPasswordEntry()
	masterSecret.SetPlaceHolder("Required - shared setup secret")
	if p.Config.MasterSecret != "" {
		masterSecret.SetText(p.Config.MasterSecret)
	}

	oauthToken := widget.NewPasswordEntry()
	oauthToken.SetPlaceHolder("Optional - for publishing rooms to Yandex Disk")
	if p.Config.OAuthToken != "" {
		oauthToken.SetText(p.Config.OAuthToken)
	}

	secretStatus := widget.NewLabel("")
	if p.Config.MasterSecret != "" {
		secretStatus.SetText("Master secret: configured")
	} else {
		secretStatus.SetText("Master secret: NOT SET (required)")
	}

	storageInfo := widget.NewLabel("Storage: " + secretStorageType())

	// --- Connection Settings ---
	conferenceId := widget.NewEntry()
	conferenceId.SetPlaceHolder("Room ID (numbers only)")
	if p.Config.ConferenceID != "" {
		conferenceId.SetText(p.Config.ConferenceID)
	}

	socksPort := widget.NewEntry()
	socksPort.SetPlaceHolder("1080")
	if p.Config.SocksPort != "" {
		socksPort.SetText(p.Config.SocksPort)
	}

	dns := widget.NewEntry()
	dns.SetPlaceHolder("1.1.1.1")
	if p.Config.DNS != "" {
		dns.SetText(p.Config.DNS)
	}

	validationLabel := widget.NewLabel("")

	applyBtn := widget.NewButtonWithIcon("Save & Apply", theme.CheckButtonCheckedIcon(), func() {
		log("Applying settings...")

		// Validate master secret (required for secure operation)
		if masterSecret.Text == "" {
			validationLabel.SetText("ERROR: Master secret is required")
			p.showError(fmt.Errorf("master secret is required for secure operation"))
			return
		}
		if len(masterSecret.Text) < 8 {
			validationLabel.SetText("ERROR: Master secret too short (min 8 chars)")
			p.showError(fmt.Errorf("master secret must be at least 8 characters"))
			return
		}

		validationLabel.SetText("Settings validated and saved")
		p.buildRunString(conferenceId.Text, "", socksPort.Text, dns.Text, masterSecret.Text)
		p.saveConfig(dns.Text, "", socksPort.Text, conferenceId.Text, oauthToken.Text, masterSecret.Text)
		secretStatus.SetText("Master secret: configured")
	})

	content := container.NewVBox(
		widget.NewLabelWithStyle("Security", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		widget.NewLabel("Master Secret"),
		masterSecret,
		secretStatus,
		widget.NewLabel("OAuth Token (optional, for room publishing)"),
		oauthToken,
		storageInfo,
		widget.NewSeparator(),
		widget.NewLabelWithStyle("Connection", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		widget.NewLabel("Conference ID"),
		conferenceId,
		widget.NewLabel("SOCKS Port"),
		socksPort,
		widget.NewLabel("DNS Server"),
		dns,
		widget.NewSeparator(),
		validationLabel,
		applyBtn,
	)
	dialog.ShowCustom("Settings", "Close", content, p.ParentWindow)
}

func (p *Program) buildRunString(conferenceId, encryptionKey, socksPort, dns, masterSecret string) {
	log("Building run string...")
	log("  Conference ID: %s", conferenceId)
	log("  Encryption Key: [REDACTED]")
	log("  Socks Port: %s", socksPort)
	log("  DNS Server: %s", dns)

	// Secrets passed via env vars, NOT argv (security: prevents process list / shell history leaks)
	switch p.Config.Os {
	case "windows":
		p.RunString = fmt.Sprintf("olcrtc.exe -mode cnc -id \"%s\" -socks-port %s -dns %s", conferenceId, socksPort, dns)
	case "linux", "darwin":
		p.RunString = fmt.Sprintf("./olcrtc -mode cnc -id \"%s\" -socks-port %s -dns %s", conferenceId, socksPort, dns)
	default:
		p.RunString = fmt.Sprintf("olcrtc -mode cnc -id \"%s\" -socks-port %s -dns %s", conferenceId, socksPort, dns)
	}
	log("Generated command: %s (secrets via env)", p.RunString)
}

func (p *Program) showError(err error) {
	dialog.ShowError(err, p.ParentWindow)
}

// fyne.Do used here to execute function in the main context frame
// we can just paste p.RunCheck.SetChecked(false) and that'll work. but if so
// there'll be a bunch of warnings(thread safety)
func (p *Program) MarkUncheck() {
	fyne.Do(func() { p.RunCheck.SetChecked(false) })
}
