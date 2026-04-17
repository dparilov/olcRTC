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
| `script/deploy-server.sh` | Automated server deploy (secrets via env) |
| `script/rotate-secret-server.sh` | Master secret rotation |
| `script/build-windows-client.sh` | Windows cross-compilation |
| `script/ui.sh` | UI launcher |
| `script/linux-testbed.sh` | Linux transport test harness |

## Room Creation API

### Official Yandex 360 API (наш текущий `CreateConference`)
- Endpoint: `https://cloud-api.yandex.net/v1/telemost-api/conferences`
- Auth: OAuth token с scope telemost:write
- **Ограничение: только для аккаунтов Яндекс 360 для бизнеса**
- Отве��: `{"id":"...","join_url":"https://telemost.yandex.ru/j/XXXX"}`

### Frontend API (kulikov0 pattern — работает с любым Yandex аккаунтом)
- Base: `https://cloud-api.yandex.ru/telemost_front/v2/telemost`
- Auth: cookies из залогиненной Yandex сессии (не OAuth)
- Create: `POST /conferences?next_gen_media_platform_allowed=true`
- Connect: `GET /conferences/{uri}/connection?next_gen_media_platform_allowed=true&display_name=NAME&waiting_room_supported=true`
- Возвращает: `peer_id, room_id, credentials, client_configuration (media_server_url, service_name, ice_servers)`
- **Наш GetConnectionInfo уже использует этот endpoint для подключения**
- **TODO: добавить создание комнат через frontend API с cookies (без OAuth)**

### Важные параметры
- `next_gen_media_platform_allowed=true` — роутит на новую SFU платформу
- `X-Telemost-Client-Version` — kulikov0 фетчит реальную версию из JS бандла
- `Client-Instance-Id` — UUID, генерируется на каждый запрос
