# Telemost SFU DataChannel Fix

## Проблема

DataChannel закрывался немедленно (~1 с) после открытия.  
Тайм-аут `datachannel timeout` на старте.

## Root cause

Telemost SFU отключает пира без медиа-треков.  
Наш publisher PC ранее содержал только DataChannel — без audio/video.  
SFU идентифицировал такую сессию как «пустую» и кикал её.

## Решение (портировано из kulikov0/whitelist-bypass)

### 1. Audio track на publisher PC
```go
audioTrack, _ := webrtc.NewTrackLocalStaticRTP(
    webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus},
    "audio", "tunnel-audio",
)
pcPub.AddTransceiverFromTrack(audioTrack, webrtc.RTPTransceiverInit{
    Direction: webrtc.RTPTransceiverDirectionSendonly,
})
```
Достаточно присутствия в SDP — реальные пакеты не отправляются.

### 2. VP8 video track + keepalive frames
```go
sampleTrack, _ := webrtc.NewTrackLocalStaticSample(
    webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8},
    "video", "tunnel-video",
)
pcPub.AddTransceiverFromTrack(sampleTrack, webrtc.RTPTransceiverInit{
    Direction: webrtc.RTPTransceiverDirectionSendonly,
})
```
25 fps keepalive: каждые 40 мс отправляется VP8 interframe (17 байт),  
каждый 60-й фрейм (~2.4 с) — VP8 keyframe (30 байт).

Байты фреймов взяты напрямую из whitelist-bypass:
```go
var vp8Keyframe   = []byte{16,2,0,157,1,42,2,0,2,0,2,7,8,133,133,136,153,132,136,11,2,0,12,13,96,0,254,252,173,16}
var vp8Interframe = []byte{177,1,0,8,17,24,0,24,0,24,88,47,244,0,8,0,0}
```

### 3. DataChannel label "sharing"
Изменён с `"olcrtc"` на `"sharing"` — SFU принимает только известные лейблы,  
`"sharing"` соответствует screen-sharing трафику и проходит через SFU.

### 4. `sendVideo: true` в hello
SFU-хэндшейк теперь корректно объявляет видео-трек.

### 5. Расширенный capabilitiesOffer
20+ полей вместо 6, зеркалящих реальный клиент Telemost.  
Это снижает вероятность получить `capabilityError` от SFU.

## Изменённые файлы
- `internal/telemost/peer.go`

## Ссылки
- https://github.com/kulikov0/whitelist-bypass — оригинальный источник решения
- `relay/tunnel/vp8tunnel.go` — VP8 frame bytes и логика keepalive
- `headless/telemost/main.go` — полный SFU signaling flow
- `headless/telemost/tunnel_relay.go` — DC label "sharing", pub/sub архитектура

## TODO
- [ ] Проверить совместимость с VK Calls (kulikov0/whitelist-bypass поддерживает оба)
- [ ] VP8 video mode как fallback если DC не работает (данные в VP8 фреймах)
