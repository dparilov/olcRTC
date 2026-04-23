package telemost

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/openlibrecommunity/olcrtc/internal/protect"
	"github.com/pion/webrtc/v4"
)

func closeSignal(ch chan struct{}) {
	if ch == nil {
		return
	}
	select {
	case <-ch:
	default:
		close(ch)
	}
}

func (p *Peer) queueReconnect() {
	if p.closed.Load() || p.reconnecting.Load() || p.shuttingDown.Load() {
		return
	}
	select {
	case p.reconnectCh <- struct{}{}:
	default:
	}
}

func (p *Peer) stopSession() {
	p.stopTelemetry()

	p.sessionMu.Lock()
	closeSignal(p.keepAliveCh)
	closeSignal(p.sessionCloseCh)
	p.sessionMu.Unlock()
}

func (p *Peer) resetSession() (chan struct{}, chan struct{}) {
	p.sessionMu.Lock()
	defer p.sessionMu.Unlock()

	p.keepAliveCh = make(chan struct{})
	p.sessionCloseCh = make(chan struct{})
	p.dcReadyCh = make(chan struct{})
	return p.keepAliveCh, p.sessionCloseCh
}

func (p *Peer) drainReconnectQueue() {
	for {
		select {
		case <-p.reconnectCh:
		default:
			return
		}
	}
}

func (p *Peer) Connect(ctx context.Context) error {
	p.closed.Store(false)

	// Step 1: Connect WebSocket and send hello FIRST.
	// We need serverHello (which contains TURN credentials) before creating PeerConnections.
	wsDialer := websocket.Dialer{
		NetDialContext:   protect.DialContext,
		HandshakeTimeout: 15 * time.Second,
	}
	ws, _, err := wsDialer.Dial(p.conn.ClientConfig.MediaServerURL, nil)
	if err != nil {
		return fmt.Errorf("WS dial: %w", err)
	}
	p.ws = ws
	ws.SetPongHandler(func(string) error {
		ws.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})
	ws.SetReadDeadline(time.Now().Add(60 * time.Second))

	if err := p.sendHello(); err != nil {
		return fmt.Errorf("sendHello: %w", err)
	}

	// Step 2: Wait for serverHello synchronously — get TURN servers.
	iceServers, err := p.waitForServerHello()
	if err != nil {
		return fmt.Errorf("waitForServerHello: %w", err)
	}

	// Use TURN servers from serverHello, fall back to STUN.
	if len(iceServers) == 0 {
		iceServers = []webrtc.ICEServer{
			{URLs: []string{"stun:stun.rtc.yandex.net:3478"}},
		}
		log.Println("[ICE] No servers from serverHello, fallback to STUN only")
	} else {
		log.Printf("[ICE] Using %d servers from serverHello (includes TURN)", len(iceServers))
	}

	// Step 3: Create PeerConnections WITH TURN servers.
	config := webrtc.Configuration{
		ICEServers:   iceServers,
		SDPSemantics: webrtc.SDPSemanticsUnifiedPlan,
	}

	settingEngine := webrtc.SettingEngine{}
	if protect.Protector != nil {
		settingEngine.SetNet(protect.NewProtectedNet())
	}
	webrtcAPI := webrtc.NewAPI(webrtc.WithSettingEngine(settingEngine))

	p.pcSub, err = webrtcAPI.NewPeerConnection(config)
	if err != nil {
		return err
	}

	p.pcSub.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		log.Printf("Subscriber PeerConnection state: %s", state.String())
		if !p.closed.Load() && (state == webrtc.PeerConnectionStateFailed || state == webrtc.PeerConnectionStateDisconnected) {
			p.queueReconnect()
		}
	})

	p.pcPub, err = webrtcAPI.NewPeerConnection(config)
	if err != nil {
		return err
	}

	p.pcPub.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		log.Printf("Publisher PeerConnection state: %s", state.String())
		if !p.closed.Load() && (state == webrtc.PeerConnectionStateFailed || state == webrtc.PeerConnectionStateDisconnected) {
			p.queueReconnect()
		}
	})

	// Add audio track (Opus sendonly) — Telemost SFU requires media tracks on the publisher.
	audioTrack, audioErr := webrtc.NewTrackLocalStaticRTP(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus},
		"audio", "tunnel-audio",
	)
	if audioErr != nil {
		return fmt.Errorf("create audio track: %w", audioErr)
	}
	if _, err = p.pcPub.AddTransceiverFromTrack(audioTrack, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionSendonly}); err != nil {
		return fmt.Errorf("add audio track: %w", err)
	}

	// Add VP8 video track (sendonly) — keepalive frames prevent SFU from kicking us.
	sampleTrack, sampleErr := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8},
		"video", "tunnel-video",
	)
	if sampleErr != nil {
		return fmt.Errorf("create video track: %w", sampleErr)
	}
	p.sampleTrack = sampleTrack
	p.vp8Sender = NewVP8Sender(sampleTrack, 25)
	if _, err = p.pcPub.AddTransceiverFromTrack(sampleTrack, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionSendonly}); err != nil {
		return fmt.Errorf("add video track: %w", err)
	}

	// DataChannel labelled "sharing" — matches Telemost screen-sharing traffic;
	// arbitrary labels are rejected by the SFU.
	ordered := true
	p.dc, err = p.pcPub.CreateDataChannel("sharing", &webrtc.DataChannelInit{Ordered: &ordered})
	if err != nil {
		return err
	}

	dcReady := make(chan struct{})
	keepAliveCh, sessionCloseCh := p.resetSession()
	p.dc.OnOpen(func() {
		log.Println("DataChannel opened")
		// Signal DC readiness for Send() waiters
		select {
		case <-p.dcReadyCh:
		default:
			close(p.dcReadyCh)
		}

		// Forward sendQueue -> VP8 data tunnel (replaces DC-based workers).
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			log.Println("[VP8-FWD] Started sendQueue -> VP8 forwarder")
			for {
				select {
				case data, ok := <-p.sendQueue:
					if !ok {
						return
					}
					if p.vp8Sender != nil {
						p.vp8Sender.SendData(data)
					}
				case <-sessionCloseCh:
					return
				case <-p.closeCh:
					return
				}
			}
		}()

		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			p.monitorQueue(sessionCloseCh)
		}()

		// Start VP8 data tunnel — sends data + keepalive frames.
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			p.vp8Sender.Run(sessionCloseCh, p.closeCh)
		}()

		close(dcReady)
	})

	pubDC := p.dc
	pubDC.OnClose(func() {
		log.Println("[DC] Publisher olcrtc DC closed")
		if p.shuttingDown.Load() || p.closed.Load() {
			return
		}
		p.dcMu.Lock()
		stillPub := (p.dc == pubDC)
		p.dcMu.Unlock()
		if stillPub {
			log.Println("[DC] Publisher DC closed, waiting for subscriber DC (not reconnecting yet)")
		} else {
			log.Println("[DC] Publisher DC closed, forwarded DC active - OK")
		}
	})

	p.dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		if p.onData != nil && len(msg.Data) > 0 {
			p.onData(msg.Data)
		}
	})

	// Receive remote VP8 video track — extract embedded data.
	p.pcSub.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		log.Printf("[VP8RX] Got remote track: id=%s codec=%s", track.ID(), track.Codec().MimeType)
		if strings.EqualFold(track.Codec().MimeType, webrtc.MimeTypeVP8) {
			p.hasVP8Track.Store(true)
			go ReadVP8Track(track, p.onData, p.closeCh)
		} else {
			log.Printf("[VP8RX] Ignoring non-VP8 track: %s", track.Codec().MimeType)
			go func() {
				buf := make([]byte, 1500)
				for {
					if _, _, err := track.Read(buf); err != nil {
						return
					}
				}
			}()
		}
	})

	p.pcSub.OnDataChannel(func(dc *webrtc.DataChannel) {
		log.Printf("[DC] Subscriber PC got DC: %s (state=%v)", dc.Label(), dc.ReadyState())
		dc.OnMessage(func(msg webrtc.DataChannelMessage) {
			if p.onData != nil && len(msg.Data) > 0 {
				p.onData(msg.Data)
			}
		})
		dc.OnClose(func() {
			p.dcMu.Lock()
			stillThis := (p.dc == dc)
			p.dcMu.Unlock()
			log.Printf("[DC] Subscriber DC closed (wasActive=%v) - NOT reconnecting (SFU behavior)", stillThis)
		})
		switchFn := func() {
			log.Printf("[DC] Switching p.dc to subscriber DC: %s", dc.Label())
			p.dcMu.Lock()
			p.dc = dc
			p.dcMu.Unlock()
			p.drainReconnectQueue()
		}
		if dc.ReadyState() == webrtc.DataChannelStateOpen {
			switchFn()
		} else {
			dc.OnOpen(func() { switchFn() })
		}
	})

	// Receive remote tracks on publisher PC too — Telemost may forward VP8 here.
	p.pcPub.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		log.Printf("[VP8RX] PUB got remote track: id=%s codec=%s", track.ID(), track.Codec().MimeType)
		if strings.EqualFold(track.Codec().MimeType, webrtc.MimeTypeVP8) {
			p.hasVP8Track.Store(true)
			go ReadVP8Track(track, p.onData, p.closeCh)
		} else {
			go func() {
				buf := make([]byte, 1500)
				for {
					if _, _, err := track.Read(buf); err != nil {
						return
					}
				}
			}()
		}
	})

	p.pcPub.OnDataChannel(func(dc *webrtc.DataChannel) {
		if dc.Label() != "sharing" {
			log.Printf("[DC] Publisher PC got unexpected DC: %s (ignoring)", dc.Label())
			return
		}
		log.Printf("[DC] Publisher PC got forwarded DC: %s (state=%v)", dc.Label(), dc.ReadyState())
		dc.OnMessage(func(msg webrtc.DataChannelMessage) {
			if p.onData != nil && len(msg.Data) > 0 {
				p.onData(msg.Data)
			}
		})
		dc.OnClose(func() {
			p.dcMu.Lock()
			stillThis := (p.dc == dc)
			p.dcMu.Unlock()
			if stillThis && !p.closed.Load() && !p.shuttingDown.Load() {
				log.Println("[DC] Forwarded DC closed - reconnecting")
				p.queueReconnect()
			}
		})
		switchFn := func() {
			log.Println("[DC] Switching p.dc to forwarded publisher DC")
			p.dcMu.Lock()
			p.dc = dc
			p.dcMu.Unlock()
			p.drainReconnectQueue()
		}
		if dc.ReadyState() == webrtc.DataChannelStateOpen {
			switchFn()
		} else {
			dc.OnOpen(func() { switchFn() })
		}
	})

	// Step 4: Start keepalive + signaling (WS already connected, hello already sent).
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		p.keepAlive(keepAliveCh)
	}()

	p.setupICEHandlers()

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		p.handleSignaling()
	}()

	select {
	case <-dcReady:
		return nil
	case <-time.After(15 * time.Second):
		return fmt.Errorf("datachannel timeout")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *Peer) gracefulLeave(timeout time.Duration) {
	log.Println("Sending leave message...")
	leaveUID := uuid.New().String()
	leaveAck := p.registerAckWaiter(leaveUID)
	if p.sendLeave(leaveUID) {
		if p.waitForAck(leaveUID, leaveAck, timeout) {
			log.Println("Leave acknowledged")
		} else {
			log.Println("Leave ack timeout")
		}
	} else {
		p.removeAckWaiter(leaveUID)
	}
}

func (p *Peer) Close() error {
	log.Println("Closing peer connection...")

	p.shuttingDown.Store(true)
	defer p.shuttingDown.Store(false)

	alreadyClosing := p.closed.Swap(true)
	p.sendQueueClosed.Store(true)

	if !alreadyClosing {
		p.gracefulLeave(1500 * time.Millisecond)
		p.stopTelemetry()
	}

	log.Println("Closing channels...")
	if p.closeCh != nil {
		select {
		case <-p.closeCh:
		default:
			close(p.closeCh)
		}
	}

	log.Println("Waiting for goroutines...")
	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		log.Println("All goroutines finished")
	case <-time.After(2 * time.Second):
		log.Println("Goroutine wait timeout")
	}

	if p.dc != nil {
		log.Println("Closing DataChannel...")
		p.dc.Close()
	}

	if p.pcPub != nil {
		log.Println("Closing Publisher PeerConnection...")
		p.pcPub.Close()
	}

	if p.pcSub != nil {
		log.Println("Closing Subscriber PeerConnection...")
		p.pcSub.Close()
	}

	if p.ws != nil {
		log.Println("Closing WebSocket...")
		p.wsMu.Lock()
		p.ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
		p.ws.Close()
		p.wsMu.Unlock()
	}

	log.Println("Peer closed")
	return nil
}

func (p *Peer) reconnect(ctx context.Context) error {
	log.Println("Reconnecting...")
	p.reconnecting.Store(true)
	p.shuttingDown.Store(true)
	defer p.reconnecting.Store(false)
	defer p.shuttingDown.Store(false)

	p.stopTelemetry()
	p.gracefulLeave(2 * time.Second)

	// Context-aware cooldown instead of blind sleep
	select {
	case <-time.After(1500 * time.Millisecond):
	case <-ctx.Done():
		return ctx.Err()
	}

	p.stopSession()

	if p.dc != nil {
		p.dc.Close()
	}

	if p.pcPub != nil {
		p.pcPub.Close()
	}

	if p.pcSub != nil {
		p.pcSub.Close()
	}

	if p.ws != nil {
		p.wsMu.Lock()
		p.ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
		p.ws.Close()
		p.wsMu.Unlock()
	}

	// Context-aware cooldown before re-connecting
	select {
	case <-time.After(3 * time.Second):
	case <-ctx.Done():
		return ctx.Err()
	}

	conn, err := GetConnectionInfo(p.roomURL, p.name)
	if err != nil {
		return err
	}
	p.conn = conn

	if err := p.Connect(ctx); err != nil {
		return err
	}

	if p.onReconnect != nil {
		p.onReconnect(p.dc)
	}

	p.drainReconnectQueue()

	return nil
}

func (p *Peer) WatchConnection(ctx context.Context) {
	const maxReconnects = 10
	const reconnectWindow = 5 * time.Minute

	for {
		select {
		case <-p.reconnectCh:
			p.reconnectMu.Lock()
			now := time.Now()
			if now.Sub(p.lastReconnect) > reconnectWindow {
				p.reconnectCount = 0
			}

			if p.reconnectCount >= maxReconnects {
				log.Printf("Max reconnect attempts (%d) reached, stopping", maxReconnects)
				p.reconnectMu.Unlock()
				return
			}

			p.reconnectCount++
			p.lastReconnect = now
			p.reconnectMu.Unlock()

			backoff := time.Duration(p.reconnectCount) * 2 * time.Second
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}

			for {
				if err := p.reconnect(ctx); err != nil {
					if ctx.Err() != nil {
						return
					}
					log.Printf("Reconnect failed: %v, retrying in %v...", err, backoff)
					select {
					case <-time.After(backoff):
					case <-ctx.Done():
						return
					}
					continue
				}
				p.reconnectMu.Lock()
				p.reconnectCount = 0
				p.reconnectMu.Unlock()
				log.Println("Reconnected successfully")
				break
			}
		case <-p.closeCh:
			return
		case <-ctx.Done():
			return
		}
	}
}
