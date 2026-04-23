# Orchestration Refactor Note — Transport Phase C / Item 7

## Timing-Driven Points Identified

| Location | Pattern | Correctness? | Action |
|----------|---------|-------------|--------|
| `mux.go:waitForBufferSpace` | `time.Sleep(5ms)` poll loop | **YES** — gates data delivery | **FIXED** → `sync.Cond.Wait()` |
| `media.go:Send()` | `time.Sleep(50ms)` poll for DC ready | **YES** — gates send path | **FIXED** → `dcReadyCh` channel wait |
| `session.go:reconnect()` L451 | `time.Sleep(1500ms)` blind wait | **YES** — reconnect correctness | **FIXED** → `select` with `ctx.Done()` |
| `session.go:reconnect()` L474 | `time.Sleep(3s)` blind wait | **YES** — reconnect correctness | **FIXED** → `select` with `ctx.Done()` |
| `client.go:waitUntilPeersCanSend` | `time.Sleep(10ms)` poll | Lower | **BOUNDED** — added 5s timeout |
| `client.go:108` | `time.Sleep(100ms)` pacing | No | Unchanged — harmless startup pacing |
| `client.go:260` | `time.Sleep(150ms)` pacing | No | Unchanged — post-reconnect signal pacing |
| `media.go:50` | `time.After(50ms)` send timeout | No | Unchanged — intentional drop policy |

## What Changed

### 1. Mux backpressure: sleep → sync.Cond
- `waitForBufferSpace()` now uses `sync.Cond.Wait()` instead of `time.Sleep(5ms)` polling
- `ReadStream()` calls `bufCond.Broadcast()` after draining buffer
- `Reset()` and `ResetClient()` call `bufCond.Broadcast()` to wake stuck waiters
- New test: `TestBackpressure_CondWakeOnRead` — proves event-driven wakeup

### 2. Send path: poll → channel
- Added `dcReadyCh` (closed-channel signal) to `Peer` struct
- `Send()` waits on `dcReadyCh` instead of polling DC state every 50ms
- `dcReadyCh` closed in `dc.OnOpen` callback
- `resetSession()` recreates `dcReadyCh` for reconnect

### 3. Reconnect: blind sleep → context-aware
- Both `time.Sleep` calls in `reconnect()` replaced with `select { case <-time.After(...): case <-ctx.Done(): }`
- Reconnect now responds to context cancellation immediately instead of blocking

### 4. Client send readiness: bounded
- `waitUntilPeersCanSend()` now has 5s timeout to prevent infinite blocking
- Full event-driven solution deferred (requires Peer interface change)

## Intentional Leftovers

- `client.go:108` — 100ms startup pacing (harmless, not correctness)
- `client.go:260` — 150ms post-reconnect pacing (harmless)
- `media.go:50` — 50ms send queue timeout (intentional backpressure policy)
- `waitUntilPeersCanSend` still polls but now bounded (follow-up: event-driven Peer readiness)

## Tests Added/Updated

- `TestBackpressure_CondWakeOnRead` — mux Cond-driven backpressure
- All existing 56 tests (26 mux + 30 telemost) continue to pass
