package telemost

import (
	"encoding/json"
	"fmt"
	"os"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/pion/webrtc/v4"
)

// traceLog writes raw WS messages to a trace file for forensic analysis.
// Set OLCRTC_WS_TRACE to a file path to enable. Disabled by default.
var wsTraceFile *os.File

func init() {
	if path := os.Getenv("OLCRTC_WS_TRACE"); path != "" {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			log.Printf("[WS-TRACE] Failed to open trace file: %v", err)
			return
		}
		wsTraceFile = f
		log.Printf("[WS-TRACE] Tracing to %s", path)
	}
}

func wsTrace(direction string, msg map[string]interface{}) {
	if wsTraceFile == nil {
		return
	}
	entry := map[string]interface{}{
		"dir": direction,
		"ts":  time.Now().UnixMilli(),
		"msg": msg,
	}
	data, _ := json.Marshal(entry)
	wsTraceFile.Write(append(data, '\n'))
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

	wsTrace("send", hello)
	p.wsMu.Lock()
	defer p.wsMu.Unlock()
	return p.ws.WriteJSON(hello)
}

// sendPubOffer creates a fresh publisher SDP offer and sends it to SFU.
// Called on every subscriberSdpOffer (not just the first) per kulikov0 pattern.
func (p *Peer) sendPubOffer() {
	pubOffer, err := p.pcPub.CreateOffer(nil)
	if err != nil {
		log.Printf("[PUB-OFFER] CreateOffer error: %v", err)
		return
	}
	if err := p.pcPub.SetLocalDescription(pubOffer); err != nil {
		log.Printf("[PUB-OFFER] SetLocalDescription error: %v", err)
		return
	}

	// SDP debug: log m= lines
	for _, line := range strings.Split(pubOffer.SDP, "\n") {
		if strings.HasPrefix(line, "m=") || strings.HasPrefix(line, "a=sendrecv") || strings.HasPrefix(line, "a=sendonly") || strings.HasPrefix(line, "a=recvonly") || strings.HasPrefix(line, "a=inactive") {
			log.Printf("[SDP-PUB-OFFER] %s", strings.TrimSpace(line))
		}
	}

	// Parse mids for track metadata (required by SFU)
	audioMid, videoMid := parseMids(pubOffer.SDP)
	var tracks []map[string]interface{}
	if audioMid != "" {
		tracks = append(tracks, map[string]interface{}{"mid": audioMid, "transceiverMid": audioMid, "kind": "AUDIO", "priority": 0, "label": "", "codecs": map[string]interface{}{}, "groupId": 1, "description": ""})
	}
	if videoMid != "" {
		tracks = append(tracks, map[string]interface{}{"mid": videoMid, "transceiverMid": videoMid, "kind": "VIDEO", "priority": 0, "label": "", "codecs": map[string]interface{}{}, "groupId": 2, "description": ""})
	}

	p.pubSeq++
	log.Printf("[PUB-OFFER] audioMid=%s videoMid=%s tracks=%d pcSeq=%d", audioMid, videoMid, len(tracks), p.pubSeq)

	// Reset pub remote set — new offer invalidates previous answer
	p.iceMu.Lock()
	p.pubRemoteSet = false
	p.pubPending = nil
	p.iceMu.Unlock()

	p.wsMu.Lock()
	p.ws.WriteJSON(map[string]interface{}{
		"uid": uuid.New().String(),
		"publisherSdpOffer": map[string]interface{}{
			"pcSeq": p.pubSeq,
			"sdp":   pubOffer.SDP,
			"tracks": tracks,
		},
	})
	p.wsMu.Unlock()
}

// requestVideoSlots sends setSlots to SFU for VP8 track forwarding.
func (p *Peer) requestVideoSlots() {
	p.wsMu.Lock()
	p.ws.WriteJSON(map[string]interface{}{
		"uid": uuid.New().String(),
		"setSlots": map[string]interface{}{
			"slots":          []map[string]interface{}{{"width": 320, "height": 240}},
			"audioSlotsCount": 1,
			"key":            1,
			"nLastConfig":    map[string]interface{}{"nCount": 1, "showInSubgrid": false},
		},
	})
	p.wsMu.Unlock()
	log.Println("[WS] -> setSlots")
}

func (p *Peer) handleSignaling() {
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

		wsTrace("recv", msg)

		// Debug: log all WS message keys
		keys := make([]string, 0, len(msg))
		for k := range msg {
			if k != "uid" && k != "ack" {
				keys = append(keys, k)
			}
		}
		if len(keys) > 0 {
			log.Printf("[WS-MSG] keys=%v", keys)
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
			// serverHello already handled by waitForServerHello() in Connect().
			// This branch only fires on reconnect edge cases.
			p.logServerHelloICE(serverHello)
			p.startTelemetry(serverHello)
			p.sendAck(uid)
		}

		// updateDescription / upsertDescription — just ACK.
		// NOTE: requestVideoSlots() after upsertDescription was attempted but
		// did NOT produce SFU renegotiation. Rolled back per forensic memo.
		// See docs/validation/e2e-test-findings.md for details.
		if ud, ok := msg["upsertDescription"].(map[string]interface{}); ok {
			if descs, ok := ud["description"].([]interface{}); ok {
				for _, d := range descs {
					if dm, ok := d.(map[string]interface{}); ok {
						pid, _ := dm["id"].(string)
						if pid != "" && pid != p.conn.PeerID {
							name := ""
							if meta, ok := dm["meta"].(map[string]interface{}); ok {
								name, _ = meta["name"].(string)
							}
							log.Printf("[WS] Participant joined: %s (%s)", name, pid)
							if p.onPeerJoined != nil {
								p.onPeerJoined(pid)
							}
						}
					}
				}
			}
			p.sendAck(uid)
		}

		if _, ok := msg["updateDescription"]; ok {
			p.sendAck(uid)
		}

		if _, ok := msg["removeDescription"]; ok {
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
			p.lastPcSeq = int(pcSeq)

			// SDP debug: log subscriber offer m= lines from SFU
			for _, line := range strings.Split(sdp, "\n") {
				if strings.HasPrefix(line, "m=") || strings.HasPrefix(line, "a=sendrecv") || strings.HasPrefix(line, "a=sendonly") || strings.HasPrefix(line, "a=recvonly") || strings.HasPrefix(line, "a=inactive") {
					log.Printf("[SDP-SUB-OFFER] %s", strings.TrimSpace(line))
				}
			}

			// Mark sub remote as set BEFORE SetRemoteDescription so buffered ICE can flush
			p.iceMu.Lock()
			p.subRemoteSet = false // reset first
			p.iceMu.Unlock()

			if err := p.pcSub.SetRemoteDescription(webrtc.SessionDescription{
				Type: webrtc.SDPTypeOffer,
				SDP:  sdp,
			}); err != nil {
				log.Printf("SetRemoteDescription error: %v", err)
				continue
			}

			// Now flush buffered sub ICE candidates
			p.iceMu.Lock()
			p.subRemoteSet = true
			for _, cand := range p.subPending {
				p.pcSub.AddICECandidate(cand)
			}
			p.subPending = nil
			p.iceMu.Unlock()

			answer, err := p.pcSub.CreateAnswer(nil)
			if err != nil {
				log.Printf("CreateAnswer error: %v", err)
				continue
			}

			if err := p.pcSub.SetLocalDescription(answer); err != nil {
				log.Printf("SetLocalDescription error: %v", err)
				continue
			}

			log.Printf("[WS] -> subscriberSdpAnswer pcSeq=%d", p.lastPcSeq)
			p.wsMu.Lock()
			p.ws.WriteJSON(map[string]interface{}{
				"uid": uuid.New().String(),
				"subscriberSdpAnswer": map[string]interface{}{
					"pcSeq": p.lastPcSeq,
					"sdp":   answer.SDP,
				},
			})
			p.wsMu.Unlock()

			p.sendAck(uid)

			// Send publisher offer on EVERY subscriberSdpOffer (kulikov0 pattern).
			// SFU needs a fresh pub offer to properly route VP8 tracks.
			p.sendPubOffer()
			p.requestVideoSlots()
		}

		if answer, ok := msg["publisherSdpAnswer"].(map[string]interface{}); ok {
			sdp, _ := answer["sdp"].(string)

			// SDP debug: log answer m= lines
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
			} else {
				// Flush buffered pub ICE candidates
				p.iceMu.Lock()
				p.pubRemoteSet = true
				for _, cand := range p.pubPending {
					p.pcPub.AddICECandidate(cand)
				}
				p.pubPending = nil
				p.iceMu.Unlock()
			}

			p.sendAck(uid)
		}

		if cand, ok := msg["webrtcIceCandidate"].(map[string]interface{}); ok {
			p.handleICE(cand)
			p.sendAck(uid)
		}

		// ACK anything else with a uid
		if uid != "" {
			p.sendAck(uid)
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

	// Buffer ICE candidates until remote description is set (kulikov0 pattern)
	p.iceMu.Lock()
	defer p.iceMu.Unlock()

	if target == "SUBSCRIBER" {
		if !p.subRemoteSet {
			p.subPending = append(p.subPending, init)
			return
		}
		p.pcSub.AddICECandidate(init)
	} else if target == "PUBLISHER" {
		if !p.pubRemoteSet {
			p.pubPending = append(p.pubPending, init)
			return
		}
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

func (p *Peer) setupICEHandlers() {
	p.pcSub.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}

		init := c.ToJSON()
		mid := ""
		if init.SDPMid != nil {
			mid = *init.SDPMid
		}
		var idx uint16
		if init.SDPMLineIndex != nil {
			idx = *init.SDPMLineIndex
		}
		p.wsMu.Lock()
		p.ws.WriteJSON(map[string]interface{}{
			"uid": uuid.New().String(),
			"webrtcIceCandidate": map[string]interface{}{
				"candidate":        init.Candidate,
				"sdpMid":           mid,
				"usernameFragment": extractUfrag(init.Candidate),
				"sdpMlineIndex":    idx,
				"target":           "SUBSCRIBER",
				"pcSeq":            p.lastPcSeq,
			},
		})
		p.wsMu.Unlock()
	})

	p.pcPub.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}

		init := c.ToJSON()
		mid := ""
		if init.SDPMid != nil {
			mid = *init.SDPMid
		}
		var idx uint16
		if init.SDPMLineIndex != nil {
			idx = *init.SDPMLineIndex
		}
		p.wsMu.Lock()
		p.ws.WriteJSON(map[string]interface{}{
			"uid": uuid.New().String(),
			"webrtcIceCandidate": map[string]interface{}{
				"candidate":        init.Candidate,
				"sdpMid":           mid,
				"usernameFragment": extractUfrag(init.Candidate),
				"sdpMlineIndex":    idx,
				"target":           "PUBLISHER",
				"pcSeq":            p.pubSeq,
			},
		})
		p.wsMu.Unlock()
	})
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

// parseMids extracts the first audio and video mid values from an SDP.
func parseMids(sdp string) (audioMid, videoMid string) {
	var media string
	for _, line := range strings.Split(sdp, "\r\n") {
		if strings.HasPrefix(line, "m=audio") {
			media = "audio"
		} else if strings.HasPrefix(line, "m=video") {
			media = "video"
		}
		if strings.HasPrefix(line, "a=mid:") {
			mid := strings.TrimPrefix(line, "a=mid:")
			if media == "audio" && audioMid == "" {
				audioMid = mid
			} else if media == "video" && videoMid == "" {
				videoMid = mid
			}
		}
	}
	return
}

// waitForServerHello reads WS messages synchronously until serverHello arrives.
// Returns ICE servers (including TURN with credentials) for PeerConnection creation.
// ACKs and processes other messages that arrive before serverHello.
func (p *Peer) waitForServerHello() ([]webrtc.ICEServer, error) {
	for {
		var msg map[string]interface{}
		if err := p.ws.ReadJSON(&msg); err != nil {
			return nil, fmt.Errorf("WS read waiting for serverHello: %w", err)
		}

		uid, _ := msg["uid"].(string)

		if _, ok := msg["ack"]; ok {
			p.resolveAck(uid)
			continue
		}

		if _, ok := msg["ping"]; ok {
			p.sendPong(uid)
			continue
		}

		if serverHello, ok := msg["serverHello"].(map[string]interface{}); ok {
			log.Println("[WS] <- serverHello")
			p.logServerHelloICE(serverHello)
			p.startTelemetry(serverHello)
			p.sendAck(uid)
			return p.extractICEServers(serverHello), nil
		}

		// ACK anything else that arrives before serverHello
		if uid != "" {
			p.sendAck(uid)
		}
	}
}

// extractICEServers parses ICE servers from serverHello rtcConfiguration.
func (p *Peer) extractICEServers(sh map[string]interface{}) []webrtc.ICEServer {
	rtcCfg, ok := sh["rtcConfiguration"].(map[string]interface{})
	if !ok {
		return nil
	}
	servers, ok := rtcCfg["iceServers"].([]interface{})
	if !ok {
		return nil
	}
	var iceServers []webrtc.ICEServer
	for _, s := range servers {
		sm, _ := s.(map[string]interface{})
		var urls []string
		if u, ok := sm["urls"].([]interface{}); ok {
			for _, v := range u {
				if vs, ok := v.(string); ok {
					urls = append(urls, vs)
				}
			}
		}
		ice := webrtc.ICEServer{URLs: urls}
		if u, ok := sm["username"].(string); ok && u != "" {
			ice.Username = u
			ice.Credential, _ = sm["credential"].(string)
		}
		iceServers = append(iceServers, ice)
	}
	return iceServers
}

// logServerHelloICE logs ICE servers from serverHello (may include TURN with credentials).
func (p *Peer) logServerHelloICE(sh map[string]interface{}) {
	rtcCfg, ok := sh["rtcConfiguration"].(map[string]interface{})
	if !ok {
		log.Println("[ICE] serverHello has no rtcConfiguration")
		return
	}
	servers, ok := rtcCfg["iceServers"].([]interface{})
	if !ok {
		log.Println("[ICE] serverHello rtcConfiguration has no iceServers")
		return
	}
	log.Printf("[ICE] serverHello contains %d ICE servers:", len(servers))
	for i, s := range servers {
		sm, _ := s.(map[string]interface{})
		urls, _ := sm["urls"].([]interface{})
		user, _ := sm["username"].(string)
		log.Printf("[ICE]   [%d] urls=%v user=%s", i, urls, user)
	}
}

// extractUfrag extracts the ICE username fragment from a candidate string.
func extractUfrag(candidate string) string {
	parts := strings.Split(candidate, " ")
	for i, p := range parts {
		if p == "ufrag" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}
