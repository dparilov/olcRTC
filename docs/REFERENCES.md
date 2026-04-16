# References & External Dependencies

## Telemost SFU Protocol

The Telemost SFU protocol was reverse-engineered from the Yandex Telemost web client JavaScript bundle.

### Key Endpoints
- **WebSocket**: `wss://telemost.yandex.ru/conferences/{roomId}/ws`
- **Room creation**: Via Yandex account at `telemost.yandex.ru`

### What to Watch For
- Changes to WS message format (especially `publisherSdpOffer`, `setSlots`, `subscriberSdpOffer`)
- Changes to SDP negotiation flow
- New authentication requirements
- Changes to video codec support (currently VP8/VP9/H264)

### Reference JS Bundle
The web client JS bundle at `telemost.yandex.ru` contains the canonical SFU protocol implementation.
Key patterns to grep for in the minified JS:
- `publisherSdpOffer` - track metadata format
- `setSlots` - slot request format
- `subscriberSdpOffer` - renegotiation handling
- `webrtcIceCandidate` - ICE candidate exchange

## pion/webrtc (github.com/pion/webrtc)

### Current: v4.2.11
### What to Watch For
- VP8 RTP depacketizer changes
- Track API changes (AddTrack behavior)
- PeerConnection state machine changes
- ICE agent updates

### Related Pion Packages
- `pion/ice/v4` - ICE connectivity
- `pion/transport/v4` - Network transport layer (uses anet)
- `pion/stun/v3` - STUN protocol

## wlynxg/anet (github.com/wlynxg/anet)

### Current: v0.0.5 (local patch applied)
### Issue
Uses `//go:linkname` to reference `net.zoneCache` which breaks gomobile linker.
We maintain a patched copy in `third_party/anet/` with linkname directives removed.

### What to Watch For
- New versions that fix gomobile compatibility
- If fixed upstream, remove `replace` directive from `go.mod` and `third_party/anet/`

## gomobile (golang.org/x/mobile)

### What to Watch For
- Go version compatibility (currently needs Go 1.25+)
- NDK version requirements
- AAR packaging changes

## Fyne (fyne.io/fyne/v2)

### Current: v2.7.3
### What to Watch For
- Windows cross-compilation improvements
- CGO requirement changes (currently needs MinGW for Windows GUI build)
- macOS support stability

## Android SDK/NDK

### Requirements
- SDK: Android SDK with platform tools
- NDK: Auto-detected from SDK
- JDK: 17+ (e.g. Adoptium `jdk-17.x`)
- Emulator: API 35 recommended
- Min API: 21

### What to Watch For
- NDK toolchain deprecations
- Android API level requirements for Compose
- Emulator image updates

## Deployment Scripts

| Script | Purpose |
|--------|---------|
| `script/srv.sh` | Server deployment via Podman |
| `script/cnc.sh` | Client deployment via Podman |
| `script/build-windows-client.sh` | Windows cross-compilation |
| `script/ui.sh` | UI launcher |
| `script/linux-testbed.sh` | Linux transport test harness |
