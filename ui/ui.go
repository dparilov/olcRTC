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
	oauthToken.SetPlaceHolder("For publishing rooms to Yandex Disk")
	if p.Config.OAuthToken != "" {
		oauthToken.SetText(p.Config.OAuthToken)
	}

	yandexCookies := widget.NewPasswordEntry()
	yandexCookies.SetPlaceHolder("For creating rooms (from browser export)")
	if p.Config.YandexCookies != "" {
		yandexCookies.SetText(p.Config.YandexCookies)
	}

	secretStatus := widget.NewLabel("")
	if p.Config.MasterSecret != "" {
		secretStatus.SetText("Master secret: configured")
	} else {
		secretStatus.SetText("Master secret: NOT SET (required)")
	}

	cookieStatus := widget.NewLabel("")
	if p.Config.YandexCookies != "" {
		cookieStatus.SetText("Yandex cookies: configured")
	} else {
		cookieStatus.SetText("Yandex cookies: not set (needed for room creation)")
	}

	storageInfo := widget.NewLabel("Storage: " + secretStorageType())

	// --- Room Settings ---
	roomInput := widget.NewEntry()
	roomInput.SetPlaceHolder("https://telemost.yandex.ru/j/... or room ID")
	if p.Config.RoomURL != "" {
		roomInput.SetText(p.Config.RoomURL)
	} else if p.Config.ConferenceID != "" {
		roomInput.SetText(p.Config.ConferenceID)
	}

	// --- Connection Settings ---
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

	// --- Helper: save all settings ---
	saveAll := func() bool {
		if masterSecret.Text == "" {
			validationLabel.SetText("ERROR: Master secret is required")
			p.showError(fmt.Errorf("master secret is required for secure operation"))
			return false
		}
		if len(masterSecret.Text) < 8 {
			validationLabel.SetText("ERROR: Master secret too short (min 8 chars)")
			p.showError(fmt.Errorf("master secret must be at least 8 characters"))
			return false
		}
		p.saveConfig(dns.Text, "", socksPort.Text, roomInput.Text, oauthToken.Text, masterSecret.Text, yandexCookies.Text)
		roomID := parseRoomInput(roomInput.Text)
		if roomID != "" {
			p.buildRunString(roomID, socksPort.Text, dns.Text, masterSecret.Text)
		}
		secretStatus.SetText("Master secret: configured")
		if yandexCookies.Text != "" {
			cookieStatus.SetText("Yandex cookies: configured")
		}
		validationLabel.SetText("Settings saved")
		return true
	}

	// --- Buttons ---
	saveBtn := widget.NewButtonWithIcon("Save", theme.CheckButtonCheckedIcon(), func() {
		saveAll()
	})

	createRoomBtn := widget.NewButton("Create Room", func() {
		if yandexCookies.Text == "" {
			validationLabel.SetText("ERROR: Yandex cookies required for room creation")
			return
		}
		validationLabel.SetText("Creating room...")
		go func() {
			roomID, err := createRoomWithCookies(yandexCookies.Text)
			if err != nil {
				fyne.Do(func() {
					validationLabel.SetText("Room creation failed: " + err.Error())
					log("Room creation failed: %v", err)
				})
				return
			}
			fyne.Do(func() {
				roomURL := roomIDToURL(roomID)
				roomInput.SetText(roomURL)
				validationLabel.SetText("Room created: " + roomID)
				log("Room created: %s", roomURL)
			})
		}()
	})

	publishBtn := widget.NewButton("Publish to Disk", func() {
		if !saveAll() {
			return
		}
		roomID := parseRoomInput(roomInput.Text)
		if roomID == "" {
			validationLabel.SetText("ERROR: Room required - create or enter room first")
			return
		}
		if oauthToken.Text == "" {
			validationLabel.SetText("ERROR: OAuth token required for Disk publishing")
			return
		}
		validationLabel.SetText("Room " + roomID + " ready (will publish on launch)")
		log("Room %s configured for publishing", roomID)
	})

	createAndLaunchBtn := widget.NewButtonWithIcon("Create & Launch", theme.MediaPlayIcon(), func() {
		if masterSecret.Text == "" || len(masterSecret.Text) < 8 {
			validationLabel.SetText("ERROR: Master secret required (min 8 chars)")
			return
		}
		if yandexCookies.Text == "" {
			validationLabel.SetText("ERROR: Yandex cookies required - paste from browser")
			return
		}
		validationLabel.SetText("Creating room...")
		go func() {
			roomID, err := createRoomWithCookies(yandexCookies.Text)
			if err != nil {
				fyne.Do(func() {
					validationLabel.SetText("Failed: " + err.Error())
				})
				return
			}
			fyne.Do(func() {
				roomURL := roomIDToURL(roomID)
				roomInput.SetText(roomURL)
				validationLabel.SetText("Room created: " + roomID + " - launching...")
				p.saveConfig(dns.Text, "", socksPort.Text, roomURL, oauthToken.Text, masterSecret.Text, yandexCookies.Text)
				p.buildRunString(roomID, socksPort.Text, dns.Text, masterSecret.Text)
				log("Create & Launch: room %s ready, starting tunnel", roomID)
				p.RunCheck.SetChecked(true)
			})
		}()
	})

	content := container.NewVBox(
		widget.NewLabelWithStyle("Security", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		widget.NewLabel("Master Secret (required)"),
		masterSecret,
		secretStatus,
		widget.NewLabel("OAuth Token (for Disk publishing)"),
		oauthToken,
		widget.NewLabel("Yandex Cookies (for room creation)"),
		yandexCookies,
		cookieStatus,
		storageInfo,
		widget.NewSeparator(),
		widget.NewLabelWithStyle("Room", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		widget.NewLabel("Room URL or ID"),
		roomInput,
		container.NewGridWithColumns(3, createRoomBtn, publishBtn, createAndLaunchBtn),
		widget.NewSeparator(),
		widget.NewLabelWithStyle("Connection", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		widget.NewLabel("SOCKS Port"),
		socksPort,
		widget.NewLabel("DNS Server"),
		dns,
		widget.NewSeparator(),
		validationLabel,
		saveBtn,
	)
	dialog.ShowCustom("Settings", "Close", content, p.ParentWindow)
}

func (p *Program) buildRunString(roomID, socksPort, dns, masterSecret string) {
	log("Building run string...")
	log("  Room ID: %s", roomID)
	log("  Socks Port: %s", socksPort)
	log("  DNS Server: %s", dns)

	// Secrets passed via env vars, NOT argv
	switch p.Config.Os {
	case "windows":
		p.RunString = fmt.Sprintf("olcrtc.exe -mode cnc -id \"%s\" -socks-port %s -dns %s", roomID, socksPort, dns)
	case "linux", "darwin":
		p.RunString = fmt.Sprintf("./olcrtc -mode cnc -id \"%s\" -socks-port %s -dns %s", roomID, socksPort, dns)
	default:
		p.RunString = fmt.Sprintf("olcrtc -mode cnc -id \"%s\" -socks-port %s -dns %s", roomID, socksPort, dns)
	}
	log("Generated command: %s (secrets via env)", p.RunString)
}

func (p *Program) showError(err error) {
	dialog.ShowError(err, p.ParentWindow)
}

func (p *Program) MarkUncheck() {
	fyne.Do(func() { p.RunCheck.SetChecked(false) })
}
