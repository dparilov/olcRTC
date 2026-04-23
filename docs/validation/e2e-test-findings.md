# E2E Transport Validation — Test Findings

## Date: 2026-04-23

## Test Scenarios Attempted

### Scenario 1: Two instances on same VPS (srv + cnc)
- Room: `71809993760686` (created by user)
- Server: `/opt/olcrtc -mode srv -id <room> -socks-port 2096`
- Client: `/opt/olcrtc -mode cnc -id <room> -socks-port 10906`
- Both with `OLCRTC_MASTER_SECRET=Software@18`

**Result: FAILED — VP8 track not forwarded between peers**

Server log shows:
```
subscriberSdpOffer → subscriberSdpAnswer pcSeq=1 → setSlots
VP8RX: Got remote track codec=audio/opus (only audio, no VP8)
upsertDescription: Participant joined (client name)
— NO second subscriberSdpOffer after client joins
```

Client log shows identical pattern — only audio/opus received.

**Tested with both baseline v2.0.0 binary AND refactored binary — same result.**

### Scenario 2: Server on VPS + Client on local Linux host
- Server: VPS `/opt/olcrtc -mode srv -id <room>`
- Client: local `go run ./cmd/olcrtc/ -mode cnc -id <room>`

**Result: PARTIAL — asymmetric VP8 forwarding**

Client receives VP8 from server:
```
[VP8RX] Got remote track: codec=video/VP8
[VP8RX] Starting reader for track
[VP8RX] frame #1 30 bytes (keyframe)
[VP8RX] frame #2..#175 17 bytes (keepalive interframes)
```

Server does NOT receive VP8 from client:
```
VP8RX: Got remote track codec=audio/opus (only audio, no VP8)
upsertDescription: Participant joined
— NO new subscriberSdpOffer after client joins
— setSlots sent after upsertDescription (with fix) but no renegotiation from SFU
```

Client sends data (`SendData #1 len=52` reset signal) but server never receives it.
SOCKS5 proxy opens but connections fail (data doesn't reach server).

## Connection Establishment Flow (observed)

### Server (first to connect):
1. `GetConnectionInfo` → room_id, peer_id, media_server_url
2. WS dial to media server (goloom.strm.yandex.net)
3. sendHello → serverHello (includes TURN servers)
4. SFU sends `subscriberSdpOffer` (2 video + 2 audio + datachannel, all sendrecv)
5. We send `subscriberSdpAnswer`
6. We send `publisherSdpOffer` (audio sendonly + video sendonly + datachannel)
7. SFU sends `publisherSdpAnswer` (audio recvonly + video recvonly + datachannel)
8. Publisher PC connected, DataChannel opened
9. VP8 keepalive frames sent successfully (err=nil)
10. Subscriber PC connected — receives audio/opus track only

### Client (connects second):
1. Same flow as server
2. After connecting: sends reset signal via VP8 tunnel
3. Receives VP8 track from server (works!)
4. Server does NOT receive VP8 from client

### Key observation:
After client joins, server receives `upsertDescription` but:
- **NO new `subscriberSdpOffer`** from SFU
- Without renegotiation, server's subscriber PC never gets VP8 track from client
- `setSlots` re-request does not trigger renegotiation

## Root Cause Analysis

The Telemost SFU does not automatically send a new `subscriberSdpOffer` to
existing participants when a new peer joins and starts publishing VP8.

The initial `subscriberSdpOffer` contains m-line slots for video, but the
SFU does not populate them with the new peer's VP8 track without explicit
renegotiation.

The second peer (client) receives VP8 from the first peer (server) because
the SFU had the server's VP8 track when creating the client's subscriber offer.

### Comparison with upstream (openlibrecommunity/olcrtc)
Upstream uses **DataChannel** (`CreateDataChannel("olcrtc")`) for data transport,
NOT VP8 video tunnel. DataChannel forwarding may work differently in the SFU.
Our code uses VP8-embedded data tunnel (`CreateDataChannel("sharing")` + VP8 frames).

### Comparison with Android client
Android client reportedly works with VPS server. The code path is identical
(`client.RunWithReady`). The difference may be:
- Network environment (mobile vs local Linux)
- Timing of connection establishment
- SFU behavior with different ICE/TURN paths
- Not yet investigated

## What was verified working

### Mux/crypto loopback (8/8 PASS on VPS)
| Scenario | Result | Details |
|----------|--------|---------|
| A — Basic send/recv | PASS | Bidirectional data integrity |
| B — Repeated lifecycle | PASS | 10x create/transfer/reset/destroy |
| C — Unidirectional throughput | PASS | 196 MB/s (local), 196 MB/s (VPS) |
| D — Bidirectional throughput | PASS | 101+104 MB/s (local), 101+104 MB/s (VPS) |
| E — Latency | PASS | median 43us, p95 77us, p99 125us |
| F — Soak sustained 20min | PASS | 188 MB/s, zero degradation (ratio 1.03) |
| G — Mixed workload 20min | PASS | 120 MB/s, zero degradation (ratio 0.98) |
| H — Reconnect during traffic | PASS | Reset mid-traffic, recovery OK |

## Open Questions for Review

1. Why does Android client successfully exchange VP8 with VPS server while Linux client does not?
2. Is there a SFU-side renegotiation mechanism we're missing?
3. Should we switch from VP8 tunnel to DataChannel transport (upstream approach)?
4. Does the `requestVideoSlots` after `upsertDescription` need additional messages to trigger renegotiation?
