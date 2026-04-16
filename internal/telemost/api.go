package telemost

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/google/uuid"
	"github.com/openlibrecommunity/olcrtc/internal/protect"
)

const (
	// Frontend API (used for joining rooms without auth)
	frontendAPI = "https://cloud-api.yandex.ru/telemost_front/v2/telemost"
	// Official Yandex 360 API (used for creating rooms with OAuth)
	officialAPI = "https://cloud-api.yandex.net/v1/telemost-api"
)

// ConferenceInfo holds the result of creating a new Telemost conference.
type ConferenceInfo struct {
	ID        string `json:"id"`
	JoinURL   string `json:"join_url"`
	ShortURL  string `json:"short_url"`
	RoomID    string `json:"-"` // extracted from join_url
}

// CreateConference creates a new Telemost room using an OAuth token.
// The token must have telemost:write scope (Yandex ID OAuth).
func CreateConference(oauthToken string) (*ConferenceInfo, error) {
	req, err := http.NewRequest("POST", officialAPI+"/conferences", nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "OAuth "+oauthToken)
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64; rv:149.0) Gecko/20100101 Firefox/149.0")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Telemost-Client-Version", "187.1.0")
	req.Header.Set("Idempotency-Key", uuid.New().String())
	req.Header.Set("Origin", "https://telemost.yandex.ru")
	req.Header.Set("Referer", "https://telemost.yandex.ru/")

	client := protect.NewHTTPClient()
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("create conference error %d: %s", resp.StatusCode, body)
	}

	var info ConferenceInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, err
	}

	// Extract room ID from join URL: https://telemost.yandex.ru/j/XXXX
	if info.JoinURL != "" {
		if parts := extractRoomID(info.JoinURL); parts != "" {
			info.RoomID = parts
		}
	}

	return &info, nil
}

// extractRoomID pulls the room ID from a Telemost join URL.
func extractRoomID(joinURL string) string {
	parsed, err := url.Parse(joinURL)
	if err != nil {
		return ""
	}
	path := parsed.Path
	// /j/XXXX
	if len(path) > 3 && path[:3] == "/j/" {
		return path[3:]
	}
	return ""
}

type ConnectionInfo struct {
	RoomID       string `json:"room_id"`
	PeerID       string `json:"peer_id"`
	Credentials  string `json:"credentials"`
	ClientConfig struct {
		MediaServerURL string `json:"media_server_url"`
	} `json:"client_configuration"`
}

func GetConnectionInfo(roomURL, displayName string) (*ConnectionInfo, error) {
	u := fmt.Sprintf("%s/conferences/%s/connection", frontendAPI, url.QueryEscape(roomURL))

	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}

	q := req.URL.Query()
	q.Add("next_gen_media_platform_allowed", "true")
	q.Add("display_name", displayName)
	q.Add("waiting_room_supported", "true")
	req.URL.RawQuery = q.Encode()

	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64; rv:149.0) Gecko/20100101 Firefox/149.0")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Client-Instance-Id", uuid.New().String())
	req.Header.Set("X-Telemost-Client-Version", "187.1.0")
	req.Header.Set("Idempotency-Key", uuid.New().String())
	req.Header.Set("Origin", "https://telemost.yandex.ru")
	req.Header.Set("Referer", "https://telemost.yandex.ru/")

	client := protect.NewHTTPClient()
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, body)
	}

	var info ConnectionInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, err
	}

	return &info, nil
}
