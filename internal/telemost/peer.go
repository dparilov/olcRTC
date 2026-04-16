// Package telemost implements a WebRTC-based tunnel using Yandex Telemost SFU.
//
// The Peer type is the central orchestrator. Its implementation is split across:
//   - peer.go      — Peer struct, constructor, accessors
//   - session.go   — session lifecycle: Connect, Close, reconnect, WatchConnection
//   - protocol.go  — signaling dialect: hello, SDP negotiation, ICE, ack, leave
//   - media.go     — data sending, keepalive, queue monitoring, traffic shaping
//   - telemetry.go — telemetry emulation (XHR heartbeats to SFU)
package telemost

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
)

const (
	realDataChannelMessageLimit = 8192
	defaultSendDelayMin         = 2 * time.Millisecond
	defaultSendDelayMax         = 12 * time.Millisecond
	defaultTelemetryInterval    = 20 * time.Second
)

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
	dcMu            sync.Mutex
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
	sendQueueClosed atomic.Bool
	closed          atomic.Bool
	reconnecting    atomic.Bool
	shuttingDown    atomic.Bool
	telemetryActive atomic.Bool
	lastPcSeq       int
	slotsKey        int
	hasVP8Track     atomic.Bool
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

func (p *Peer) SetReconnectCallback(cb func(*webrtc.DataChannel)) {
	p.onReconnect = cb
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
