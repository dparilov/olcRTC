package telemost

import (
	"fmt"
	"log"
	"math/rand/v2"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
)

// VP8 keepalive frames — required to keep Telemost SFU alive.
// Without a video track the SFU disconnects the peer within ~1 s.
// Source: github.com/kulikov0/whitelist-bypass relay/tunnel/vp8tunnel.go
var vp8Keyframe = []byte{
	16, 2, 0, 157, 1, 42, 2, 0, 2, 0, 2, 7, 8, 133, 133, 136,
	153, 132, 136, 11, 2, 0, 12, 13, 96, 0, 254, 252, 173, 16,
}

var vp8Interframe = []byte{
	177, 1, 0, 8, 17, 24, 0, 24, 0, 24, 88, 47, 244, 0, 8, 0, 0,
}

func (p *Peer) Send(data []byte) error {
	// Wait up to 5s for DC to become ready (subscriber DC may arrive after publisher DC closes)
	if p.dc == nil || p.dc.ReadyState() != webrtc.DataChannelStateOpen {
		select {
		case <-p.dcReadyCh:
			// DC is ready
		case <-time.After(5 * time.Second):
			if p.dc == nil || p.dc.ReadyState() != webrtc.DataChannelStateOpen {
				log.Printf("[SEND] DC not ready after 5s wait")
				return fmt.Errorf("datachannel not ready")
			}
		case <-p.closeCh:
			return fmt.Errorf("peer closed")
		}
		if p.dc != nil && p.dc.ReadyState() == webrtc.DataChannelStateOpen {
			log.Printf("[SEND] DC became ready after wait (label=%s)", p.dc.Label())
		}
	}

	if p.sendQueueClosed.Load() {
		return fmt.Errorf("send queue closed")
	}

	select {
	case p.sendQueue <- data:
		return nil
	case <-time.After(50 * time.Millisecond):
		queueLen := len(p.sendQueue)
		log.Printf("[SEND_QUEUE] Timeout! queue_len=%d, dropping packet size=%d", queueLen, len(data))
		return fmt.Errorf("send queue timeout")
	}
}

func (p *Peer) keepAlive(keepAliveCh <-chan struct{}) {
	wsPingTicker := time.NewTicker(30 * time.Second)
	defer wsPingTicker.Stop()

	appPingTicker := time.NewTicker(5 * time.Second)
	defer appPingTicker.Stop()

	for {
		select {
		case <-wsPingTicker.C:
			p.wsMu.Lock()
			if p.ws != nil {
				if err := p.ws.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(10*time.Second)); err != nil {
					log.Printf("WS Ping error: %v", err)
					p.wsMu.Unlock()
					p.queueReconnect()
					return
				}
			}
			p.wsMu.Unlock()
		case <-appPingTicker.C:
			p.wsMu.Lock()
			if p.ws != nil {
				if err := p.ws.WriteJSON(map[string]interface{}{
					"uid":  uuid.New().String(),
					"ping": map[string]interface{}{},
				}); err != nil {
					log.Printf("App Ping error: %v", err)
					p.wsMu.Unlock()
					p.queueReconnect()
					return
				}
			}
			p.wsMu.Unlock()
		case <-keepAliveCh:
			return
		case <-p.closeCh:
			return
		}
	}
}

func (p *Peer) monitorQueue(sessionCloseCh <-chan struct{}) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			queueLen := len(p.sendQueue)
			buffered := uint64(0)
			if p.dc != nil {
				buffered = p.dc.BufferedAmount()
			}
			if queueLen > 800 || buffered > 3*1024*1024 {
				log.Printf("[QUEUE_MONITOR] queue_len=%d dc_buffered=%d MB", queueLen, buffered/(1024*1024))
			}
		case <-sessionCloseCh:
			return
		case <-p.closeCh:
			return
		}
	}
}

func (p *Peer) CanSend() bool {
	queueLen := len(p.sendQueue)
	buffered := uint64(0)
	if p.dc != nil {
		buffered = p.dc.BufferedAmount()
	}
	return queueLen < 1000 && buffered < 3*1024*1024
}

func (p *Peer) nextSendDelay() time.Duration {
	minDelay := p.trafficShape.MinDelay
	maxDelay := p.trafficShape.MaxDelay
	if maxDelay <= 0 {
		return 0
	}
	if maxDelay <= minDelay {
		return maxDelay
	}
	return minDelay + time.Duration(rand.Int64N(int64(maxDelay-minDelay)))
}
