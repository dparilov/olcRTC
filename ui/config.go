package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type Config struct {
	Os            string
	DNS           string `json:"dns"`
	EncryptionKey string `json:"-"` // not persisted in JSON (security)
	SocksPort     string `json:"socks_port"`
	ConferenceID  string `json:"conference_id"` // legacy: numeric room ID
	RoomURL       string `json:"room_url"`      // primary: Telemost room URL or ID
	OAuthToken    string `json:"-"`             // not persisted in JSON (security)
	MasterSecret  string `json:"-"`             // not persisted in JSON (security)
	YandexCookies string `json:"-"`             // not persisted in JSON (security)
}

// legacyConfig is used to read old configs that may contain secrets.
type legacyConfig struct {
	DNS           string `json:"dns"`
	EncryptionKey string `json:"encryption_key"`
	SocksPort     string `json:"socks_port"`
	ConferenceID  string `json:"conference_id"`
	OAuthToken    string `json:"oauth_token"`
	MasterSecret  string `json:"master_secret"`
}

func isValidPort(portStr string) bool {
	portStr = strings.TrimSpace(portStr)
	if portStr == "" {
		return false
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return false
	}
	return port > 0 && port <= 65535
}

func isValidConferenceID(conferenceID string) bool {
	conferenceID = strings.TrimSpace(conferenceID)
	if conferenceID == "" {
		return false
	}
	// Accept both numeric room ID and Telemost URL
	if parseRoomInput(conferenceID) != "" {
		return true
	}
	matched, err := regexp.MatchString(`^\d+$`, conferenceID)
	if err != nil {
		return false
	}
	return matched
}

func (p *Program) getConfigPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		log("WARNING: Could not get system config directory: %v", err)
		return "config.json"
	}
	configDir := filepath.Join(dir, "olcrtc")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		log("WARNING: Could not create config directory: %v", err)
	}
	return filepath.Join(configDir, "config.json")
}

func (p *Program) loadConfig() *Config {
	configPath := p.getConfigPath()
	log("Loading config from: %s", configPath)
	// default values
	cfg := &Config{
		DNS:           "1.1.1.1",
		EncryptionKey: "",
		SocksPort:     "1080",
		ConferenceID:  "",
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			log("Config file not found. Using default configuration.")
		} else {
			log("WARNING: Could not read config file: %v", err)
		}
		return cfg
	}

	// First pass: read with legacyConfig to extract any persisted secrets
	var legacy legacyConfig
	if err := json.Unmarshal(data, &legacy); err != nil {
		log("WARNING: Could not parse config file: %v", err)
		return cfg
	}

	// Second pass: read non-secret fields into Config (json:"-" fields skipped)
	if err := json.Unmarshal(data, cfg); err != nil {
		log("WARNING: Could not parse config file (pass 2): %v", err)
		return cfg
	}

	// Load secrets from platform-native secure storage (DPAPI on Windows, file on Linux)
	storedSecret, storedToken, storedCookies, secErr := loadSecrets()
	if secErr != nil {
		log("WARNING: Could not load from secure storage: %v", secErr)
	} else {
		if storedSecret != "" {
			cfg.MasterSecret = storedSecret
			log("Master secret loaded from secure storage (%s)", secretStorageType())
		}
		if storedToken != "" {
			cfg.OAuthToken = storedToken
			log("OAuth token loaded from secure storage (%s)", secretStorageType())
		}
		if storedCookies != "" {
			cfg.YandexCookies = storedCookies
			log("Yandex cookies loaded from secure storage (%s)", secretStorageType())
		}
	}

	// Migrate legacy secrets from config file to secure storage
	migrated := false
	if legacy.EncryptionKey != "" {
		cfg.EncryptionKey = legacy.EncryptionKey
		migrated = true
	}
	if legacy.OAuthToken != "" && cfg.OAuthToken == "" {
		cfg.OAuthToken = legacy.OAuthToken
		migrated = true
	}
	if legacy.MasterSecret != "" && cfg.MasterSecret == "" {
		cfg.MasterSecret = legacy.MasterSecret
		migrated = true
	}
	if migrated {
		log("SECURITY: Found legacy secrets in config - migrating to secure storage")
		if err := saveSecrets(cfg.MasterSecret, cfg.OAuthToken, cfg.YandexCookies); err != nil {
			log("WARNING: Could not migrate to secure storage: %v", err)
		} else {
			log("Legacy secrets migrated to secure storage (%s)", secretStorageType())
		}
		cleanData, mErr := json.MarshalIndent(cfg, "", "  ")
		if mErr == nil {
			if wErr := os.WriteFile(configPath, cleanData, 0600); wErr != nil {
				log("WARNING: Could not rewrite clean config: %v", wErr)
			} else {
				log("Config file rewritten without secrets")
			}
		}
	}

	cfg.ConferenceID = strings.ReplaceAll(cfg.ConferenceID, " ", "")
	if !isValidConferenceID(cfg.ConferenceID) {
		log("WARNING: Invalid conference ID in config (must be numbers only)")
		cfg.ConferenceID = ""
	}
	if !isValidPort(cfg.SocksPort) {
		log("WARNING: Invalid port in config, using default: 1080")
		cfg.SocksPort = "1080"
	}
	log("Config loaded successfully")
	return cfg
}

func (p *Program) saveConfig(dns, encryptionKey, socksPort, roomInput, oauthToken, masterSecret, yandexCookies string) {
	log("Saving configuration...")

	roomInput = strings.TrimSpace(roomInput)

	if !isValidPort(socksPort) {
		log("ERROR: Invalid port: %s", socksPort)
		p.showError(fmt.Errorf("invalid port: must be between 1 and 65535"))
		return
	}

	// Parse room input: accept URL or numeric ID
	var conferenceID string
	if roomInput != "" {
		conferenceID = parseRoomInput(roomInput)
		if conferenceID == "" {
			log("ERROR: Invalid room input: %s", roomInput)
			p.showError(fmt.Errorf("invalid room: enter Telemost URL or numeric room ID"))
			return
		}
	}

	p.Config = &Config{
		DNS:           dns,
		EncryptionKey: encryptionKey,
		SocksPort:     socksPort,
		ConferenceID:  conferenceID,
		RoomURL:       roomInput,
		OAuthToken:    oauthToken,
		MasterSecret:  masterSecret,
		YandexCookies: yandexCookies,
	}

	configPath := p.getConfigPath()
	data, err := json.MarshalIndent(p.Config, "", "  ")
	if err != nil {
		log("ERROR: Could not marshal config: %v", err)
		p.showError(err)
		return
	}

	if err := os.WriteFile(configPath, data, 0600); err != nil {
		log("ERROR: Could not write config file: %v", err)
		p.showError(err)
		return
	}

	log("Configuration saved to: %s", configPath)

	// Save secrets to platform-native secure storage
	if masterSecret != "" || oauthToken != "" || yandexCookies != "" {
		if err := saveSecrets(masterSecret, oauthToken, yandexCookies); err != nil {
			log("WARNING: Could not save secrets to secure storage: %v", err)
		} else {
			log("Secrets saved to secure storage (%s)", secretStorageType())
		}
	}
}
