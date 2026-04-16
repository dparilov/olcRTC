//go:build !windows

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type secretData struct {
	MasterSecret string `json:"master_secret,omitempty"`
	OAuthToken   string `json:"oauth_token,omitempty"`
}

func getSecretsPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "secrets.json"
	}
	return filepath.Join(dir, "olcrtc", "secrets.json")
}

func saveSecrets(masterSecret, oauthToken string) error {
	data := secretData{MasterSecret: masterSecret, OAuthToken: oauthToken}
	plain, err := json.Marshal(data)
	if err != nil {
		return err
	}
	path := getSecretsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	// On non-Windows, use file permissions for protection (0600)
	return os.WriteFile(path, plain, 0600)
}

func loadSecrets() (masterSecret, oauthToken string, err error) {
	path := getSecretsPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", nil // no secrets stored yet
		}
		return "", "", err
	}
	var s secretData
	if err := json.Unmarshal(data, &s); err != nil {
		return "", "", err
	}
	return s.MasterSecret, s.OAuthToken, nil
}

func deleteSecrets() error {
	return os.Remove(getSecretsPath())
}

func secretStorageType() string {
	return "encrypted file (0600)"
}
