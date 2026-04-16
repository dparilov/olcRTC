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
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	diskAPI      = "https://cloud-api.yandex.net/v1/disk"
	roomFilePath = "app:/olcrtc/active-room.json"
)

// RoomRecord is the contract for the rendezvous file on Yandex Disk.
// Version 2 adds KeyVersion, RecordID and Sig for authenticated records.
type RoomRecord struct {
	RoomID     string `json:"room_id"`
	RoomURL    string `json:"room_url"`
	CreatedAt  string `json:"created_at"`            // ISO 8601
	ExpiresAt  string `json:"expires_at"`            // ISO 8601
	Version    int    `json:"version"`               // schema version: 2
	KeyVersion int    `json:"key_version,omitempty"` // which master secret signed this (1=current, 0=legacy)
	RecordID   string `json:"record_id,omitempty"`   // random nonce for replay prevention
	Sig        string `json:"sig,omitempty"`         // HMAC-SHA256 over canonical JSON (without sig)
}

// DeriveKey computes a deterministic encryption key from a master secret and room ID.
// key = HMAC-SHA256(masterSecret, roomID), returned as 64-char hex string.
func DeriveKey(masterSecret, roomID string) string {
	mac := hmac.New(sha256.New, []byte(masterSecret))
	mac.Write([]byte(roomID))
	return hex.EncodeToString(mac.Sum(nil))
}

// generateRecordID creates a random 16-byte hex nonce for replay prevention.
func generateRecordID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// canonicalJSON produces a deterministic JSON representation of the record
// with the "sig" field removed, suitable for signing/verification.
func canonicalJSON(record *RoomRecord) ([]byte, error) {
	data, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	delete(m, "sig")

	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var buf strings.Builder
	buf.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		keyJSON, _ := json.Marshal(k)
		valJSON, _ := json.Marshal(m[k])
		buf.Write(keyJSON)
		buf.WriteByte(':')
		buf.Write(valJSON)
	}
	buf.WriteByte('}')
	return []byte(buf.String()), nil
}

// SignRecord signs a room record using the master secret.
// Sets KeyVersion, RecordID, Version and Sig fields on the record.
func SignRecord(record *RoomRecord, masterSecret string, keyVersion int) error {
	record.Version = 2
	record.KeyVersion = keyVersion
	record.RecordID = generateRecordID()
	record.Sig = ""

	canonical, err := canonicalJSON(record)
	if err != nil {
		return fmt.Errorf("canonical json: %w", err)
	}

	mac := hmac.New(sha256.New, []byte(masterSecret))
	mac.Write(canonical)
	record.Sig = hex.EncodeToString(mac.Sum(nil))
	return nil
}

// VerifyRecord checks the HMAC signature on a room record.
func VerifyRecord(record *RoomRecord, masterSecret string) error {
	if record.Sig == "" {
		return fmt.Errorf("unsigned record (no sig field)")
	}
	if record.Version < 2 {
		return fmt.Errorf("legacy record version %d (signing requires v2+)", record.Version)
	}

	expectedSig := record.Sig
	record.Sig = ""
	defer func() { record.Sig = expectedSig }()

	canonical, err := canonicalJSON(record)
	if err != nil {
		return fmt.Errorf("canonical json: %w", err)
	}

	mac := hmac.New(sha256.New, []byte(masterSecret))
	mac.Write(canonical)
	computedSig := hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(computedSig), []byte(expectedSig)) {
		return fmt.Errorf("signature mismatch")
	}
	return nil
}

// VerifyRecordMulti tries current then previous secret. Returns which matched (1=current, 2=previous).
func VerifyRecordMulti(record *RoomRecord, currentSecret, previousSecret string) (int, error) {
	if err := VerifyRecord(record, currentSecret); err == nil {
		return 1, nil
	}
	if previousSecret != "" {
		if err := VerifyRecord(record, previousSecret); err == nil {
			return 2, nil
		}
	}
	return 0, fmt.Errorf("signature verification failed against all known secrets")
}

// FetchAndVerifyRoom reads the room record and verifies its signature.
// Returns the record and which secret matched (1=current, 2=previous).
func FetchAndVerifyRoom(oauthToken, currentSecret, previousSecret string) (*RoomRecord, int, error) {
	record, err := FetchRoom(oauthToken)
	if err != nil {
		return nil, 0, err
	}
	if record == nil {
		return nil, 0, nil
	}
	if IsExpired(record) {
		return nil, 0, fmt.Errorf("room %s expired at %s", record.RoomID, record.ExpiresAt)
	}
	if record.Version < 2 || record.Sig == "" {
		return nil, 0, fmt.Errorf("unsigned/legacy record (version=%d), rejecting", record.Version)
	}
	matched, err := VerifyRecordMulti(record, currentSecret, previousSecret)
	if err != nil {
		return nil, 0, fmt.Errorf("signature verification: %w", err)
	}
	return record, matched, nil
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
