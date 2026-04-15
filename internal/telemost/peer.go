package telemost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/openlibrecommunity/olcrtc/internal/logger"
	"github.com/openlibrecommunity/olcrtc/internal/protect"
	"github.com/pion/webrtc/v4"
)

const (
	realDataChannelMessageLimit = 8192
	defaultSendDelayMin         = 2 * time.Millisecond
	defaultSendDelayMax         = 12 * time.Millisecond
	defaultTelemetryInterval    = 20 * time.Second
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

type TrafficShape struct {
	MaxMessageSize int
	MinDelay       time.Duration
	MaxDelay       time.Duration
}

type Peer struct {
	roomURL         string
	name            string
	conn            *ConnectionInfo
	ws              *websocket.Conn
	wsMu            sync.Mutex
	dcMu             sync.Mutex
	pcSub           *webrtc.PeerConnection
	pcPub           *webrtc.PeerConnection
	dc              *webrtc.DataChannel
	sampleTrack     *webrtc.TrackLocalStaticSample
	vp8Sender       *VP8Sender
	onData          func([]byte)
	onReconnect     func(*webrtc.DataChannel)
	reconnectCh     chan struct{}
	closeCh         chan struct{}
	keepAliveCh     chan struct{}
	telemetryCh     chan struct{}
	lastReconnect   time.Time
	reconnectCount  int
	reconnectMu     sync.Mutex
	sessionMu       sync.Mutex
	sendQueue       chan []byte
	sendQueueClosed  atomic.Bool
	closed           atomic.Bool
	reconnecting     atomic.Bool
	shuttingDown     atomic.Bool
	telemetryActive  atomic.Bool
	ackMu           sync.Mutex
	ackWaiters      map[string]chan struct{}
	onEnded         func(string)
	trafficShape    TrafficShape
	sessionCloseCh  chan struct{}
	wg              sync.WaitGroup
}

func (p *Peer) GetSendQueue() chan []byte {
	return p.sendQueue
}

func (p *Peer) GetBufferedAmount() uint64 {
	if p.dc != nil {
		return p.dc.BufferedAmount()
	}
	return 0
}

func (p *Peer) SetEndedCallback(cb func(string)) {
	p.onEnded = cb
}

func (p *Peer) SetTrafficShape(shape TrafficShape) {
	if shape.MaxMessageSize <= 0 {
		shape.MaxMessageSize = realDataChannelMessageLimit
	}
	if shape.MaxDelay < shape.MinDelay {
		shape.MaxDelay = shape.MinDelay
	}
	p.trafficShape = shape
}

func NewPeer(roomURL, name string, onData func([]byte)) (*Peer, error) {
	conn, err := GetConnectionInfo(roomURL, name)
	if err != nil {
		return nil, err
	}

	return &Peer{
		roomURL:        roomURL,
		name:           name,
		conn:           conn,
		onData:         onData,
		reconnectCh:    make(chan struct{}, 1),
		closeCh:        make(chan struct{}),
		keepAliveCh:    make(chan struct{}),
		sessionCloseCh: make(chan struct{}),
		telemetryCh:    make(chan struct{}, 1),
		sendQueue:      make(chan []byte, 5000),
		ackWaiters:     make(map[string]chan struct{}),
		trafficShape: TrafficShape{
			MaxMessageSize: realDataChannelMessageLimit,
			MinDelay:       defaultSendDelayMin,
			MaxDelay:       defaultSendDelayMax,
		},
	}, nil
}

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

	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{URLs: []string{"stun:stun.rtc.yandex.net:3478"}},
		},
		SDPSemantics: webrtc.SDPSemanticsUnifiedPlan,
	}

	settingEngine := webrtc.SettingEngine{}
	if protect.Protector != nil {
		settingEngine.SetICEProxyDialer(protect.NewProxyDialer())
	}
	api := webrtc.NewAPI(webrtc.WithSettingEngine(settingEngine))

	var err error
	p.pcSub, err = api.NewPeerConnection(config)
	if err != nil {
		return err
	}


	p.pcSub.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		log.Printf("Subscriber PeerConnection state: %s", state.String())
		if !p.closed.Load() && (state == webrtc.PeerConnectionStateFailed || state == webrtc.PeerConnectionStateDisconnected) {
			p.queueReconnect()
		}
	})

	p.pcPub, err = api.NewPeerConnection(config)
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
	if _, err = p.pcPub.AddTrack(audioTrack); err != nil {
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
	if _, err = p.pcPub.AddTrack(sampleTrack); err != nil {
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

		// Forward sendQueue → VP8 data tunnel (replaces DC-based workers).
		// Data is embedded in VP8 video frames because Telemost SFU does
		// not relay DataChannel messages between peers.
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			log.Println("[VP8-FWD] Started sendQueue → VP8 forwarder")
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
		// Must begin after DC opens to avoid sending before DTLS handshake.
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
			go ReadVP8Track(track, p.onData, p.closeCh)
		} else {
			log.Printf("[VP8RX] Ignoring non-VP8 track: %s", track.Codec().MimeType)
			// Drain the track to avoid blocking
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
			go ReadVP8Track(track, p.onData, p.closeCh)
		} else {
			// Drain non-VP8 tracks to prevent buffer buildup
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

	wsDialer := websocket.Dialer{
		NetDialContext:   protect.DialContext,
		HandshakeTimeout: 15 * time.Second,
	}
	ws, _, err := wsDialer.Dial(p.conn.ClientConfig.MediaServerURL, nil)
	if err != nil {
		return err
	}
	p.ws = ws

	ws.SetPongHandler(func(string) error {
		ws.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	ws.SetReadDeadline(time.Now().Add(60 * time.Second))

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		p.keepAlive(keepAliveCh)
	}()

	if err := p.sendHello(); err != nil {
		return err
	}

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

func (p *Peer) Send(data []byte) error {
	// Wait up to 5s for DC to become ready (subscriber DC may arrive after publisher DC closes)
	if p.dc == nil || p.dc.ReadyState() != webrtc.DataChannelStateOpen {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
			if p.dc != nil && p.dc.ReadyState() == webrtc.DataChannelStateOpen {
				break
			}
		}
		if p.dc == nil || p.dc.ReadyState() != webrtc.DataChannelStateOpen {
			log.Printf("[SEND] DC not ready after 5s wait")
			return fmt.Errorf("datachannel not ready")
		}
		log.Printf("[SEND] DC became ready after wait (label=%s)", p.dc.Label())
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

func (p *Peer) sendHello() error {
	hello := map[string]interface{}{
		"uid": uuid.New().String(),
		"hello": map[string]interface{}{
			"participantMeta": map[string]interface{}{
				"name":        p.name,
				"role":        "SPEAKER",
				"description": "",
				"sendAudio":   false,
				"sendVideo":   true,
			},
			"participantAttributes": map[string]interface{}{
				"name": p.name,
				"role": "SPEAKER",
			},
			"sendAudio":     false,
			"sendVideo":     true,
			"sendSharing":   false,
			"participantId": p.conn.PeerID,
			"roomId":        p.conn.RoomID,
			"serviceName":   "telemost",
			"credentials":   p.conn.Credentials,
			"capabilitiesOffer": map[string]interface{}{
				"offerAnswerMode":              []string{"SEPARATE"},
				"initialSubscriberOffer":       []string{"ON_HELLO"},
				"slotsMode":                    []string{"FROM_CONTROLLER"},
				"simulcastMode":                []string{"DISABLED", "STATIC"},
				"selfVadStatus":                []string{"FROM_SERVER", "FROM_CLIENT"},
				"dataChannelSharing":           []string{"TO_RTP"},
				"videoEncoderConfig":           []string{"NO_CONFIG", "ONLY_INIT_CONFIG", "RUNTIME_CONFIG"},
				"dataChannelVideoCodec":        []string{"VP8", "UNIQUE_CODEC_FROM_TRACK_DESCRIPTION"},
				"bandwidthLimitationReason":    []string{"BANDWIDTH_REASON_DISABLED", "BANDWIDTH_REASON_ENABLED"},
				"sdkDefaultDeviceManagement":   []string{"SDK_DEFAULT_DEVICE_MANAGEMENT_DISABLED", "SDK_DEFAULT_DEVICE_MANAGEMENT_ENABLED"},
				"joinOrderLayout":              []string{"JOIN_ORDER_LAYOUT_DISABLED", "JOIN_ORDER_LAYOUT_ENABLED"},
				"pinLayout":                    []string{"PIN_LAYOUT_DISABLED"},
				"sendSelfViewVideoSlot":        []string{"SEND_SELF_VIEW_VIDEO_SLOT_DISABLED", "SEND_SELF_VIEW_VIDEO_SLOT_ENABLED"},
				"serverLayoutTransition":       []string{"SERVER_LAYOUT_TRANSITION_DISABLED"},
				"sdkPublisherOptimizeBitrate":  []string{"SDK_PUBLISHER_OPTIMIZE_BITRATE_DISABLED", "SDK_PUBLISHER_OPTIMIZE_BITRATE_FULL", "SDK_PUBLISHER_OPTIMIZE_BITRATE_ONLY_SELF"},
				"sdkNetworkLostDetection":      []string{"SDK_NETWORK_LOST_DETECTION_DISABLED"},
				"sdkNetworkPathMonitor":        []string{"SDK_NETWORK_PATH_MONITOR_DISABLED"},
				"publisherVp9":                 []string{"PUBLISH_VP9_DISABLED", "PUBLISH_VP9_ENABLED"},
				"svcMode":                      []string{"SVC_MODE_DISABLED", "SVC_MODE_L3T3", "SVC_MODE_L3T3_KEY"},
				"subscriberOfferAsyncAck":      []string{"SUBSCRIBER_OFFER_ASYNC_ACK_DISABLED", "SUBSCRIBER_OFFER_ASYNC_ACK_ENABLED"},
				"androidBluetoothRoutingFix":   []string{"ANDROID_BLUETOOTH_ROUTING_FIX_DISABLED"},
				"fixedIceCandidatesPoolSize":   []string{"FIXED_ICE_CANDIDATES_POOL_SIZE_DISABLED"},
				"subscriberDtlsPassiveMode":    []string{"SUBSCRIBER_DTLS_PASSIVE_MODE_DISABLED", "SUBSCRIBER_DTLS_PASSIVE_MODE_ENABLED"},
			},
			"sdkInfo": map[string]interface{}{
				"implementation": "go",
				"version":        "1.0.0",
				"userAgent":      "OlcRTC-" + p.name,
			},
			"sdkInitializationId": uuid.New().String(),
			"disablePublisher":    false,
			"disableSubscriber":   false,
		},
	}

	p.wsMu.Lock()
	defer p.wsMu.Unlock()
	return p.ws.WriteJSON(hello)
}

func (p *Peer) handleSignaling() {
	pubSent := false

	for {
		var msg map[string]interface{}
		if err := p.ws.ReadJSON(&msg); err != nil {
			if p.shuttingDown.Load() || p.closed.Load() {
				log.Printf("WS read closed during shutdown: %v", err)
				return
			}
			log.Printf("WS read error: %v", err)
			if !p.closed.Load() {
				p.queueReconnect()
			}
			return
		}

		p.wsMu.Lock()
		if p.ws != nil {
			p.ws.SetReadDeadline(time.Now().Add(60 * time.Second))
		}
		p.wsMu.Unlock()

		uid, _ := msg["uid"].(string)

		if _, ok := msg["ack"]; ok {
			p.resolveAck(uid)
		}

		if serverHello, ok := msg["serverHello"].(map[string]interface{}); ok {
			p.startTelemetry(serverHello)
			p.sendAck(uid)
		}

		if _, ok := msg["updateDescription"]; ok {
			p.sendAck(uid)
		}

		if _, ok := msg["vadActivity"]; ok {
			p.sendAck(uid)
		}

		if isConferenceEndMessage(msg) {
			p.signalEnded("conference ended")
			return
		}

		if _, ok := msg["ping"]; ok {
			p.sendPong(uid)
			continue
		}

		if _, ok := msg["pong"]; ok {
			p.sendAck(uid)
			continue
		}

		if offer, ok := msg["subscriberSdpOffer"].(map[string]interface{}); ok {
			sdp, _ := offer["sdp"].(string)
			pcSeq, _ := offer["pcSeq"].(float64)

			// SDP debug: log subscriber offer m= lines from SFU
			for _, line := range strings.Split(sdp, "\n") {
				if strings.HasPrefix(line, "m=") || strings.HasPrefix(line, "a=sendrecv") || strings.HasPrefix(line, "a=sendonly") || strings.HasPrefix(line, "a=recvonly") || strings.HasPrefix(line, "a=inactive") {
					log.Printf("[SDP-SUB-OFFER] %s", strings.TrimSpace(line))
				}
			}
			if err := p.pcSub.SetRemoteDescription(webrtc.SessionDescription{
				Type: webrtc.SDPTypeOffer,
				SDP:  sdp,
			}); err != nil {
				log.Printf("SetRemoteDescription error: %v", err)
				continue
			}

			answer, err := p.pcSub.CreateAnswer(nil)
			if err != nil {
				log.Printf("CreateAnswer error: %v", err)
				continue
			}

			if err := p.pcSub.SetLocalDescription(answer); err != nil {
				log.Printf("SetLocalDescription error: %v", err)
				continue
			}

			p.wsMu.Lock()
			p.ws.WriteJSON(map[string]interface{}{
				"uid": uuid.New().String(),
				"subscriberSdpAnswer": map[string]interface{}{
					"pcSeq": int(pcSeq),
					"sdp":   answer.SDP,
				},
			})
			p.wsMu.Unlock()

			p.sendAck(uid)

			if !pubSent {
			time.Sleep(300 * time.Millisecond)

			pubOffer, err := p.pcPub.CreateOffer(nil)
			if err != nil {
				log.Printf("CreateOffer error: %v", err)
				continue
			}
			for _, line := range strings.Split(pubOffer.SDP, "\n") {
				if strings.HasPrefix(line, "m=") || strings.HasPrefix(line, "a=sendrecv") || strings.HasPrefix(line, "a=sendonly") || strings.HasPrefix(line, "a=recvonly") || strings.HasPrefix(line, "a=inactive") {
					log.Printf("[SDP-PUB-OFFER] %s", strings.TrimSpace(line))
				}
			}
			// SDP debug: log m= lines to verify video is included
			for _, line := range strings.Split(pubOffer.SDP, "\n") {
				if strings.HasPrefix(line, "m=") || strings.HasPrefix(line, "a=sendrecv") || strings.HasPrefix(line, "a=sendonly") || strings.HasPrefix(line, "a=recvonly") || strings.HasPrefix(line, "a=inactive") {
					log.Printf("[SDP-PUB-OFFER] %s", strings.TrimSpace(line))
				}
			}

			if err := p.pcPub.SetLocalDescription(pubOffer); err != nil {
				log.Printf("SetLocalDescription error: %v", err)
				continue
			}

			p.wsMu.Lock()
			p.ws.WriteJSON(map[string]interface{}{
				"uid": uuid.New().String(),
				"publisherSdpOffer": map[string]interface{}{
					"pcSeq": 1,
					"sdp":   pubOffer.SDP,
				},
			})
			p.wsMu.Unlock()

			pubSent = true
			} // end !pubSent
		}

		if answer, ok := msg["publisherSdpAnswer"].(map[string]interface{}); ok {
			sdp, _ := answer["sdp"].(string)

			// SDP debug: log answer m= lines
			for _, line := range strings.Split(sdp, "\n") {
				if strings.HasPrefix(line, "m=") || strings.HasPrefix(line, "a=sendrecv") || strings.HasPrefix(line, "a=sendonly") || strings.HasPrefix(line, "a=recvonly") || strings.HasPrefix(line, "a=inactive") {
					log.Printf("[SDP-PUB-ANSWER] %s", strings.TrimSpace(line))
				}
			}
			for _, line := range strings.Split(sdp, "\n") {
				if strings.HasPrefix(line, "m=") || strings.HasPrefix(line, "a=sendrecv") || strings.HasPrefix(line, "a=sendonly") || strings.HasPrefix(line, "a=recvonly") || strings.HasPrefix(line, "a=inactive") {
					log.Printf("[SDP-PUB-ANSWER] %s", strings.TrimSpace(line))
				}
			}
			if err := p.pcPub.SetRemoteDescription(webrtc.SessionDescription{
				Type: webrtc.SDPTypeAnswer,
				SDP:  sdp,
			}); err != nil {
				log.Printf("SetRemoteDescription error: %v", err)
			}

			p.sendAck(uid)
		}

		if cand, ok := msg["webrtcIceCandidate"].(map[string]interface{}); ok {
			p.handleICE(cand)
		}
	}
}

func (p *Peer) handleICE(cand map[string]interface{}) {
	candStr, _ := cand["candidate"].(string)
	target, _ := cand["target"].(string)
	sdpMid, _ := cand["sdpMid"].(string)
	sdpMLineIndex, _ := cand["sdpMlineIndex"].(float64)

	parts := strings.Fields(candStr)
	if len(parts) < 8 {
		return
	}

	init := webrtc.ICECandidateInit{
		Candidate:     candStr,
		SDPMid:        &sdpMid,
		SDPMLineIndex: func() *uint16 { v := uint16(sdpMLineIndex); return &v }(),
	}

	if target == "SUBSCRIBER" {
		p.pcSub.AddICECandidate(init)
	} else if target == "PUBLISHER" {
		p.pcPub.AddICECandidate(init)
	}
}

func (p *Peer) sendAck(uid string) {
	if uid == "" {
		return
	}

	p.wsMu.Lock()
	defer p.wsMu.Unlock()

	p.ws.WriteJSON(map[string]interface{}{
		"uid": uid,
		"ack": map[string]interface{}{
			"status": map[string]interface{}{
				"code": "OK",
			},
		},
	})
}

func (p *Peer) registerAckWaiter(uid string) chan struct{} {
	ch := make(chan struct{})
	p.ackMu.Lock()
	p.ackWaiters[uid] = ch
	p.ackMu.Unlock()
	return ch
}

func (p *Peer) removeAckWaiter(uid string) {
	p.ackMu.Lock()
	delete(p.ackWaiters, uid)
	p.ackMu.Unlock()
}

func (p *Peer) waitForAck(uid string, ch <-chan struct{}, timeout time.Duration) bool {
	if uid == "" {
		return false
	}

	defer func() {
		p.removeAckWaiter(uid)
	}()

	select {
	case <-ch:
		return true
	case <-time.After(timeout):
		return false
	case <-p.closeCh:
		return false
	}
}

func (p *Peer) resolveAck(uid string) {
	if uid == "" {
		return
	}

	p.ackMu.Lock()
	ch := p.ackWaiters[uid]
	if ch != nil {
		delete(p.ackWaiters, uid)
		close(ch)
	}
	p.ackMu.Unlock()
}

func (p *Peer) sendPong(uid string) {
	p.wsMu.Lock()
	defer p.wsMu.Unlock()

	p.ws.WriteJSON(map[string]interface{}{
		"uid":  uid,
		"pong": map[string]interface{}{},
	})
}

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

func (p *Peer) signalEnded(reason string) {
	log.Printf("Conference ended: %s", reason)
	p.closed.Store(true)
	p.stopTelemetry()
	if p.onEnded != nil {
		p.onEnded(reason)
	}
}

func isConferenceEndMessage(msg map[string]interface{}) bool {
	for _, key := range []string{"conferenceClosed", "conferenceEnded", "roomClosed", "roomEnded", "callEnded"} {
		if _, ok := msg[key]; ok {
			return true
		}
	}

	if raw, ok := msg["conference"].(map[string]interface{}); ok {
		if state, _ := raw["state"].(string); isEndedState(state) {
			return true
		}
	}

	if raw, ok := msg["conferenceState"].(map[string]interface{}); ok {
		if state, _ := raw["state"].(string); isEndedState(state) {
			return true
		}
	}

	return false
}

func isEndedState(state string) bool {
	switch strings.ToLower(state) {
	case "closed", "ended", "finished", "terminated":
		return true
	default:
		return false
	}
}

func (p *Peer) setupICEHandlers() {
	p.pcSub.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}

		init := c.ToJSON()
		p.wsMu.Lock()
		p.ws.WriteJSON(map[string]interface{}{
			"uid": uuid.New().String(),
			"webrtcIceCandidate": map[string]interface{}{
				"candidate":     init.Candidate,
				"sdpMid":        init.SDPMid,
				"sdpMlineIndex": init.SDPMLineIndex,
				"target":        "SUBSCRIBER",
				"pcSeq":         1,
			},
		})
		p.wsMu.Unlock()
	})

	p.pcPub.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}

		init := c.ToJSON()
		p.wsMu.Lock()
		p.ws.WriteJSON(map[string]interface{}{
			"uid": uuid.New().String(),
			"webrtcIceCandidate": map[string]interface{}{
				"candidate":     init.Candidate,
				"sdpMid":        init.SDPMid,
				"sdpMlineIndex": init.SDPMLineIndex,
				"target":        "PUBLISHER",
				"pcSeq":         1,
			},
		})
		p.wsMu.Unlock()
	})
}

func (p *Peer) sendLeave(uid string) bool {
	p.wsMu.Lock()
	defer p.wsMu.Unlock()

	if p.ws == nil {
		log.Println("WebSocket already closed, cannot send leave")
		return false
	}

	leave := map[string]interface{}{
		"uid":   uid,
		"leave": map[string]interface{}{},
	}

	if err := p.ws.WriteJSON(leave); err != nil {
		log.Printf("Failed to send leave: %v", err)
		return false
	} else {
		log.Println("Sent leave message to server")
	}
	return true
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

func (p *Peer) reconnect(ctx context.Context) error {
	log.Println("Reconnecting...")
	p.reconnecting.Store(true)
	p.shuttingDown.Store(true)
	defer p.reconnecting.Store(false)
	defer p.shuttingDown.Store(false)

	p.stopTelemetry()
	p.gracefulLeave(2 * time.Second)
	time.Sleep(1500 * time.Millisecond)

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

	time.Sleep(3 * time.Second)

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

func (p *Peer) SetReconnectCallback(cb func(*webrtc.DataChannel)) {
	p.onReconnect = cb
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
