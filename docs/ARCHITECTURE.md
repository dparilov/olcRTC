# OlcRTC Architecture

## Overview

OlcRTC is a bidirectional IP tunnel that encapsulates TCP traffic inside VP8 video frames,
transmitted through Yandex Telemost video conferencing infrastructure (WebRTC SFU).

## Data Flow

```
App (browser/curl) --> SOCKS5 proxy (127.0.0.1:1080) --> VP8 Encoder --> WebRTC PeerConnection
    --> Telemost SFU --> WebRTC PeerConnection --> VP8 Decoder --> TCP --> Internet
```

## Room Discovery (Inverted Flow)

No Yandex 360 Business subscription needed. Uses Yandex Disk as passive rendezvous
(CloudTransport pattern, Cornell PETS 2014).

```
1. Client creates room manually (browser/app)
2. Client publishes room to Yandex Disk (app:/olcrtc/active-room.json)
3. Server polls Disk every 10s, finds new room, connects
4. Key = HMAC-SHA256(master_secret, room_id) — deterministic, both sides compute same key
5. When room expires: client creates new room, publishes → server auto-switches
```

### Usage

```bash
# Server (VPS, runs permanently):
export OLCRTC_OAUTH_TOKEN="<token>"
export OLCRTC_MASTER_SECRET="<shared-secret>"
olcrtc -mode srv --discover

# Client (user device):
export OLCRTC_OAUTH_TOKEN="<token>"
export OLCRTC_MASTER_SECRET="<shared-secret>"
olcrtc -mode cnc -id ROOM_ID
```

> **Note:** Secrets are passed via environment variables, never via command-line
> arguments. See `SECURITY.md` for the full secret handling model.

### Room Record Contract (app:/olcrtc/active-room.json)

```json
{
  "room_id": "77873352023589",
  "room_url": "https://telemost.yandex.ru/j/77873352023589",
  "created_at": "2026-04-16T06:27:00+03:00",
  "expires_at": "2026-04-16T09:27:00+03:00",
  "version": 1
}
```

## Components

### Core (`internal/`)

| Package | Purpose |
|---------|---------|
| `internal/telemost/peer.go` | WebRTC peer management, SFU protocol, SDP negotiation |
| `internal/telemost/vp8tunnel.go` | VP8 frame encoding/decoding, drain loop optimization |
| `internal/client/` | Client orchestration (SOCKS proxy + Telemost peer) |
| `internal/server/` | Server orchestration + RoomManager |
| `internal/rendezvous/` | Yandex Disk passive rendezvous (publish/fetch/delete) |
| `internal/protect/` | Android VPN socket protection |
| `internal/crypto/` | AES encryption for tunnel data |
| `internal/mux/` | Stream multiplexer for SOCKS connections |

### VP8 Data Encoding

Data is embedded in VP8 video frames:
- **Marker byte**: `0xFF` indicates data frame (vs keepalive)
- **Length**: 4 bytes, big-endian payload length
- **Payload**: encrypted TCP data
- **Keepalive**: empty interframes at 25fps, keyframe every 60th frame
- **Drain loop**: up to 50 data frames per burst for sustained throughput

### Telemost SFU Protocol (Reverse-Engineered)

Critical WebSocket messages:

1. **`publisherSdpOffer`** — Must include `tracks` metadata:
   ```json
   {"publisherSdpOffer": {"pcSeq": 1, "sdp": "...", "tracks": [
     {"mid": "0", "kind": "AUDIO", "groupId": 1},
     {"mid": "1", "kind": "VIDEO", "groupId": 2}
   ]}}
   ```

2. **`setSlots`** — Required for SFU to forward video:
   ```json
   {"setSlots": {"slots": [{"width": 320, "height": 240}], "audioSlotsCount": 1, "key": 1}}
   ```

3. **`subscriberSdpOffer`** — Must handle ALL renegotiations.

### Key Derivation

```
key = HMAC-SHA256(master_secret, room_id)
```

Both client and server compute the same 256-bit key from a shared master secret
and the room ID. No key exchange needed — room ID from Yandex Disk serves as the nonce.

## Platforms

### Linux CLI (`cmd/olcrtc/`)
Full support: server watch mode, client publish, auto-reconnect.

### Windows GUI (`ui/`)
Fyne-based desktop app. Settings: OAuth token, master secret, conference ID.
Launches olcrtc as subprocess; secrets passed via environment variables.

### Android (`mobile/` + `android/`)
Jetpack Compose UI. Settings: OAuth token, master secret.
"Publish" button pushes room to Yandex Disk.
Key derived via `DeriveKeyFromSecret()` gomobile binding.
Secrets stored in memory only, not persisted to config files.

## Reconnect Strategy

### WebRTC Level (peer.go)
- Up to 10 reconnects in 5-minute window
- Exponential backoff (2s, 4s, ... 30s max)
- Handles: ICE failure, PeerConnection disconnected/failed

### Room Level (main.go)
- **Server (--discover)**: polls Disk every 10s, auto-switches to new room
- **Client (--discover)**: on conference end, re-fetches room from Disk in 5s
- **Client (manual)**: publishes room to Disk on startup

## Performance

| Metric | Value |
|--------|-------|
| Burst (1MB) | 1.76 MB/s (14 Mbit/s) |
| Latency (HTTP) | ~0.53s |
| Stability | 10/10 requests |
| Sustained (>1MB) | Degraded (WebRTC REMB throttling) |

## Academic References

- **Stegozoa** (AsiaCCS 2022): WebRTC video steganography for censorship circumvention
- **CloudTransport** (Cornell, PETS 2014): Cloud storage as passive rendezvous
- **CRON**: Censorship-Resistant Overlay Network via WebRTC covert channels
