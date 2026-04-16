// Package rendezvous implements room discovery via Yandex Disk.
//
// This follows the "passive rendezvous" pattern described in CloudTransport
// (Cornell, PETS 2014): cloud storage acts as a shared bulletin board where
// the server publishes the current room and the client reads it.
//
// References:
//   - CloudTransport: https://www.cs.cornell.edu/~shmat/shmat_pets14.pdf
//   - Stegozoa (WebRTC covert channels): AsiaCCS 2022
package rendezvous

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	diskAPI      = "https://cloud-api.yandex.net/v1/disk"
	roomFilePath = "app:/olcrtc/active-room.json"
)

// RoomRecord is the contract for the rendezvous file on Yandex Disk.
type RoomRecord struct {
	RoomID    string `json:"room_id"`
	RoomURL   string `json:"room_url"`
	CreatedAt string `json:"created_at"` // ISO 8601
	ExpiresAt string `json:"expires_at"` // ISO 8601
	Version   int    `json:"version"`    // schema version, currently 1
}

// DeriveKey computes a deterministic encryption key from a master secret and room ID.
// key = HMAC-SHA256(masterSecret, roomID), returned as 64-char hex string.
func DeriveKey(masterSecret, roomID string) string {
	mac := hmac.New(sha256.New, []byte(masterSecret))
	mac.Write([]byte(roomID))
	return hex.EncodeToString(mac.Sum(nil))
}

// PublishRoom writes (or overwrites) the active room record to Yandex Disk.
func PublishRoom(oauthToken string, record *RoomRecord) error {
	// Ensure parent directory exists
	if err := mkdirIfNeeded(oauthToken, "app:/olcrtc"); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	// Marshal record
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}

	// Get upload URL
	uploadURL, err := getUploadURL(oauthToken, roomFilePath, true)
	if err != nil {
		return fmt.Errorf("get upload url: %w", err)
	}

	// Upload
	req, err := http.NewRequest("PUT", uploadURL, strings.NewReader(string(data)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upload failed %d: %s", resp.StatusCode, body)
	}

	return nil
}

// FetchRoom reads the active room record from Yandex Disk.
// Returns nil, nil if the file doesn't exist yet.
func FetchRoom(oauthToken string) (*RoomRecord, error) {
	// Get download URL
	dlURL, err := getDownloadURL(oauthToken, roomFilePath)
	if err != nil {
		if strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "NotFoundError") {
			return nil, nil // not published yet
		}
		return nil, fmt.Errorf("get download url: %w", err)
	}

	// Download
	resp, err := http.Get(dlURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download failed %d", resp.StatusCode)
	}

	var record RoomRecord
	if err := json.NewDecoder(resp.Body).Decode(&record); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}

	return &record, nil
}

// IsExpired checks if a room record has passed its expiration time.
func IsExpired(record *RoomRecord) bool {
	if record == nil || record.ExpiresAt == "" {
		return true
	}
	expires, err := time.Parse(time.RFC3339, record.ExpiresAt)
	if err != nil {
		return true
	}
	return time.Now().After(expires)
}

// DeleteRoom removes the active room file from Yandex Disk.
func DeleteRoom(oauthToken string) error {
	u := fmt.Sprintf("%s/resources?path=%s&permanently=true",
		diskAPI, url.QueryEscape(roomFilePath))

	req, err := http.NewRequest("DELETE", u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "OAuth "+oauthToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// 204 = deleted, 404 = already gone — both OK
	if resp.StatusCode != 204 && resp.StatusCode != 404 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delete failed %d: %s", resp.StatusCode, body)
	}

	return nil
}

// --- helpers ---

func mkdirIfNeeded(token, path string) error {
	u := fmt.Sprintf("%s/resources?path=%s", diskAPI, url.QueryEscape(path))
	req, err := http.NewRequest("PUT", u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "OAuth "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// 201 = created, 409 = already exists — both OK
	if resp.StatusCode != 201 && resp.StatusCode != 409 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("mkdir %d: %s", resp.StatusCode, body)
	}

	return nil
}

func getUploadURL(token, path string, overwrite bool) (string, error) {
	u := fmt.Sprintf("%s/resources/upload?path=%s&overwrite=%v",
		diskAPI, url.QueryEscape(path), overwrite)

	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "OAuth "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("upload url %d: %s", resp.StatusCode, body)
	}

	var result struct {
		Href string `json:"href"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	return result.Href, nil
}

func getDownloadURL(token, path string) (string, error) {
	u := fmt.Sprintf("%s/resources/download?path=%s",
		diskAPI, url.QueryEscape(path))

	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "OAuth "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("%d: %s", resp.StatusCode, body)
	}

	var result struct {
		Href string `json:"href"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	return result.Href, nil
}
