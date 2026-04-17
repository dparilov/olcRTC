package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

const (
	frontendAPI = "https://cloud-api.yandex.ru/telemost_front/v2/telemost"
)

// parseRoomInput accepts a Telemost URL, invite link, or raw numeric room ID.
// Returns the numeric room ID or empty string if invalid.
func parseRoomInput(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}

	// Try as URL first
	if strings.Contains(input, "telemost.yandex") {
		parsed, err := url.Parse(input)
		if err == nil && parsed.Path != "" {
			// /j/XXXX
			if idx := strings.LastIndex(parsed.Path, "/"); idx >= 0 {
				candidate := parsed.Path[idx+1:]
				if isNumeric(candidate) {
					return candidate
				}
			}
		}
	}

	// Raw numeric ID
	if isNumeric(input) {
		return input
	}

	return ""
}

// roomIDToURL converts a numeric room ID to a Telemost join URL.
func roomIDToURL(roomID string) string {
	if roomID == "" {
		return ""
	}
	return "https://telemost.yandex.ru/j/" + roomID
}

var numericRe = regexp.MustCompile(`^\d+$`)

func isNumeric(s string) bool {
	return numericRe.MatchString(s)
}

// createRoomWithCookies creates a new Telemost room via Frontend API.
// Returns the room URL (e.g. "https://telemost.yandex.ru/j/XXXX") or error.
func createRoomWithCookies(cookies string) (string, error) {
	if cookies == "" {
		return "", fmt.Errorf("Yandex cookies required for room creation")
	}

	req, err := http.NewRequest("POST",
		frontendAPI+"/conferences?next_gen_media_platform_allowed=true",
		strings.NewReader("{}"))
	if err != nil {
		return "", err
	}

	req.Header.Set("Cookie", cookies)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/125.0 Safari/537.36")
	req.Header.Set("X-Telemost-Client-Version", "187.1.0")
	req.Header.Set("Origin", "https://telemost.yandex.ru")
	req.Header.Set("Referer", "https://telemost.yandex.ru/")
	req.Header.Set("Client-Instance-Id", uuid.New().String())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("create room request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("create room error %d: %s", resp.StatusCode, body)
	}

	var result struct {
		URI string `json:"uri"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}
	if result.URI == "" {
		return "", fmt.Errorf("empty conference URI in response")
	}

	// The URI might be a full URL or just a path — extract room ID
	roomID := parseRoomInput(result.URI)
	if roomID == "" {
		roomID = result.URI
	}

	log("Room created: %s (ID: %s)", result.URI, roomID)
	return roomID, nil
}
