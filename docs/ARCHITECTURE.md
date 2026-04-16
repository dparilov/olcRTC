# OlcRTC Architecture

## Overview

OlcRTC is a bidirectional IP tunnel that encapsulates TCP traffic inside VP8 video frames,
transmitted through Yandex Telemost video conferencing infrastructure (WebRTC SFU).

## Data Flow

```
App (browser/curl) --> SOCKS5 proxy (127.0.0.1:1080) --> VP8 Encoder --> WebRTC PeerConnection
    --> Telemost SFU --> WebRTC PeerConnection --> VP8 Decoder --> TCP --> Internet
```

## Components

### Core (`internal/`)

| Package | Purpose |
|---------|---------|
| `internal/telemost/peer.go` | WebRTC peer management, SFU protocol, SDP negotiation |
| `internal/telemost/vp8tunnel.go` | VP8 frame encoding/decoding for data transport |
| `internal/telemost/vp8sender.go` | VP8 keyframe/interframe generation at 25fps |
| `internal/client/` | Client orchestration (SOCKS proxy + Telemost peer) |
| `internal/protect/` | Android VPN socket protection |
| `internal/logger/` | Logging utilities |

### VP8 Data Encoding

Data is embedded in VP8 video frames using a simple protocol:
- **Marker byte**: `0xFF` indicates data frame (vs keepalive)
- **Length**: 2 bytes, big-endian payload length
- **Payload**: raw TCP data
- **Keepalive**: empty interframes at 25fps, keyframe every 60th frame

### Telemost SFU Protocol (Reverse-Engineered)

Critical WebSocket messages:

1. **`publisherSdpOffer`** - Must include `tracks` metadata:
   ```json
   {
     "publisherSdpOffer": {
       "pcSeq": 1,
       "sdp": "...",
       "tracks": [
         {"mid": "0", "kind": "AUDIO", "groupId": 1},
         {"mid": "1", "kind": "VIDEO", "groupId": 2}
       ]
     }
   }
   ```

2. **`setSlots`** - Required for SFU to forward video:
   ```json
   {
     "setSlots": {
       "slots": [{"width": 320, "height": 240}],
       "audioSlotsCount": 1,
       "key": 1
     }
   }
   ```

3. **`subscriberSdpOffer`** - Must handle ALL renegotiations, not just first.

### PeerConnection Setup

- **Publisher PC** (`pcPub`): `AddTrack()` with sendrecv direction (NOT sendonly)
- **Subscriber PC** (`pcSub`): Created by SFU, handles incoming tracks
- Both PCs have `OnTrack` handlers for VP8 reception
- DataChannel used only for signaling (hello, reset), NOT data

## Platforms

### Linux CLI (`cmd/olcrtc/`)
Direct binary, runs as `olcrtc -mode srv|cnc -id ROOM -key KEY`

### Windows GUI (`ui/`)
Fyne-based desktop app. Launches olcrtc as subprocess.
Build: `GOOS=windows GOARCH=amd64 go build ./cmd/windows-client`

### Android (`mobile/` + `android/`)
gomobile AAR binding + Jetpack Compose UI.
Build: `gomobile bind -target=android ./mobile` then `./gradlew assembleDebug`

## Modes

- **`srv` (server)**: Connects to Telemost room, accepts TCP from tunnel, forwards to internet
- **`cnc` (client)**: Connects to Telemost room, runs SOCKS5 proxy, tunnels traffic through VP8

## Key Dependencies

| Dependency | Version | Purpose |
|-----------|---------|---------|
| pion/webrtc/v4 | v4.2.11 | WebRTC stack |
| gorilla/websocket | v1.5.3 | Telemost WS signaling |
| fyne.io/fyne/v2 | v2.7.3 | Windows/Linux GUI |
| wlynxg/anet | v0.0.5 (patched) | Android net.Interfaces fix |
| golang.org/x/mobile | latest | gomobile AAR build |

## Known Limitations

1. **Sustained throughput**: Burst ~14 Mbit/s, sustained degrades for >1MB transfers
2. **Room lifetime**: Telemost rooms expire after ~24h
3. **Single channel**: One VP8 stream per direction (duo mode planned)
