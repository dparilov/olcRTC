# Peer Layer Architecture — Transport Refactor Phase A

## Overview

The `internal/telemost/` package implements a WebRTC-based data tunnel
over the Yandex Telemost SFU. The `Peer` type is the central orchestrator,
with its implementation split into coherent units with clear boundaries.

## File → Responsibility Mapping

| File | Lines | Responsibility |
|------|-------|----------------|
| `peer.go` | 134 | Peer struct definition, constructor (`NewPeer`), accessors, constants |
| `protocol.go` | 693 | **Signaling dialect** — hello, SDP offer/answer, ICE candidate handling, ack/pong, leave, conference-end detection |
| `session.go` | 548 | **Session lifecycle** — `Connect`, `Close`, `reconnect`, `WatchConnection`, session reset/stop |
| `media.go` | 141 | **Media/data tunnel** — `Send`, keepAlive (WS ping + app ping), queue monitoring, `CanSend`, traffic shaping |
| `telemetry.go` | 115 | **Telemetry** — XHR heartbeat emulation to SFU endpoint (join/stats/leave) |
| `vp8tunnel.go` | 218 | **VP8 data embedding** — `VP8Sender` (data→VP8 frames), `ReadVP8Track` (VP8→data extraction) |
| `api.go` | 195 | **Telemost HTTP API** — room creation, connection info retrieval |
| `dns.go` | 30 | Custom DNS resolver for mobile clients |

## Architectural Boundaries

```
session.go (orchestrator)
  ├── protocol.go  — signaling dialect (WS messages)
  ├── media.go     — data send, keepalive, queue monitor
  ├── telemetry.go — XHR heartbeats to SFU
  └── vp8tunnel.go — VP8 data embedding (send + receive)

peer.go — struct, constructor, config
api.go  — HTTP API (room creation, connection info)
dns.go  — custom DNS resolver
```

## Dependency Flow

- `session.go` orchestrates: calls into `protocol.go` (signaling), starts `media.go` goroutines, manages `telemetry.go`
- `protocol.go` handles WS message parsing/sending — pure signaling dialect
- `media.go` handles data path: send queue → VP8 sender, keepalive pings
- `telemetry.go` runs independently (HTTP POST to SFU telemetry endpoint)
- `vp8tunnel.go` is self-contained: VP8 frame encode/decode, track read/write
- `api.go` is called only from `peer.go` constructor and `session.go` reconnect

## Shared Mutable State

The `Peer` struct contains shared state accessed across files via mutex:
- `wsMu` — WebSocket write serialization (protocol.go, media.go, session.go)
- `dcMu` — DataChannel pointer swap (session.go)
- `iceMu` — ICE candidate buffering (protocol.go)
- `ackMu` — ACK waiter map (protocol.go)
- `sessionMu` — session channel lifecycle (session.go)
- `reconnectMu` — reconnect counter/timing (session.go)

Atomic flags (`closed`, `reconnecting`, `shuttingDown`, `telemetryActive`,
`sendQueueClosed`, `hasVP8Track`) provide lock-free cross-goroutine state.

## Reconnect Lifecycle

Located in `session.go`:
1. `queueReconnect()` — non-blocking signal via buffered channel
2. `WatchConnection()` — event loop: dequeue, backoff, call `reconnect()`
3. `reconnect()` — graceful leave → stop session → close PCs → re-GetConnectionInfo → Connect
4. `drainReconnectQueue()` — prevents stale signals after successful reconnect

Reconnect is independently testable via `TestQueueReconnect`, `TestDrainReconnectQueue`,
`TestStopSession`, `TestResetSession`.

## Test Coverage

| File | Tests | What's covered |
|------|-------|----------------|
| `protocol_test.go` | `TestParseMids`, `TestExtractUfrag`, `TestIsConferenceEndMessage`, `TestIsEndedState` | Signaling dialect parsing |
| `session_test.go` | `TestCloseSignal`, `TestQueueReconnect`, `TestDrainReconnectQueue`, `TestResetSession`, `TestStopSession`, `TestSetTrafficShape`, callbacks | Reconnect lifecycle, session management |
| `vp8tunnel_test.go` | `TestBuildAndExtractDataFrame`, `TestExtractDataFromPayload_Keepalive`, `TestDataFrameMarker` | VP8 data tunnel round-trip |

## Intentional Non-Goals (deferred to Item 6 / Item 7)

- **Mux rewrite** — current data path via VP8 embedding preserved as-is
- **Sleep/poll cleanup** — `time.Sleep` in `reconnect()` and `Send()` poll loops preserved
- **Transport protocol changes** — signaling dialect unchanged
- **Shared state reduction beyond current level** — mutex structure matches current access patterns; further reduction requires mux redesign
