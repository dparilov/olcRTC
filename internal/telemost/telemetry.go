package telemost

import (
	"bytes"
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/openlibrecommunity/olcrtc/internal/logger"
	"github.com/openlibrecommunity/olcrtc/internal/protect"
)

func (p *Peer) startTelemetry(serverHello map[string]interface{}) {
	cfg, ok := serverHello["telemetryConfiguration"].(map[string]interface{})
	if !ok {
		return
	}

	endpoint, _ := cfg["logEndpoint"].(string)
	if endpoint == "" {
		endpoint, _ = cfg["endpoint"].(string)
	}
	if endpoint == "" {
		endpoint, _ = cfg["url"].(string)
	}
	if endpoint == "" {
		logger.Verbose("Telemetry configuration has no endpoint; skipping XHR simulation")
		return
	}

	interval := defaultTelemetryInterval
	if raw, ok := cfg["sendingInterval"].(float64); ok && raw > 0 {
		interval = time.Duration(raw) * time.Millisecond
	}

	if !p.telemetryActive.CompareAndSwap(false, true) {
		return
	}

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		defer p.telemetryActive.Store(false)

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		p.sendTelemetry(endpoint, "join")
		for {
			select {
			case <-ticker.C:
				p.sendTelemetry(endpoint, "stats")
			case <-p.telemetryCh:
				p.sendTelemetry(endpoint, "leave")
				return
			case <-p.closeCh:
				p.sendTelemetry(endpoint, "leave")
				return
			}
		}
	}()
}

func (p *Peer) stopTelemetry() {
	if p.telemetryActive.Load() {
		select {
		case p.telemetryCh <- struct{}{}:
		default:
		}
	}
}

func (p *Peer) sendTelemetry(endpoint, event string) {
	body, err := json.Marshal(map[string]interface{}{
		"event":          event,
		"timestamp":      time.Now().UnixMilli(),
		"peerId":         p.conn.PeerID,
		"roomId":         p.conn.RoomID,
		"displayName":    p.name,
		"implementation": "olcrtc-go",
		"dataChannel": map[string]interface{}{
			"bufferedAmount": p.GetBufferedAmount(),
			"sendQueue":      len(p.sendQueue),
		},
	})
	if err != nil {
		return
	}

	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		logger.Verbose("Telemetry request skipped: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64; rv:149.0) Gecko/20100101 Firefox/149.0")
	req.Header.Set("Origin", "https://telemost.yandex.ru")
	req.Header.Set("Referer", p.roomURL)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Client-Instance-Id", uuid.New().String())
	req.Header.Set("X-Telemost-Client-Version", "187.1.0")
	req.Header.Set("Idempotency-Key", uuid.New().String())

	client := protect.NewHTTPClient()
	resp, err := client.Do(req)
	if err != nil {
		logger.Verbose("Telemetry send failed: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		logger.Verbose("Telemetry endpoint returned %s", resp.Status)
	}
}
