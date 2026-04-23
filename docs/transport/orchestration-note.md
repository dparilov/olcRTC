# Orchestration Refactor Note — Transport Phase C / Item 7 (Final)

## Timing-Driven Points — Final Status

| Location | Original Pattern | Status | Replacement |
|----------|-----------------|--------|-------------|
| `mux.go:waitForBufferSpace` | `time.Sleep(5ms)` poll | **REMOVED** | `sync.Cond.Wait()` — event-driven |
| `media.go:Send()` | `time.Sleep(50ms)` DC poll | **REMOVED** | `dcReadyCh` channel wait |
| `client.go:proxyStream()` | `time.NewTicker(10ms)` poll | **REMOVED** | `WaitForData()` + `StreamClosedCh()` signals |
| `session.go:reconnect()` 1500ms | `time.Sleep(1500ms)` blind | **REMOVED** | `wg.Wait()` goroutine completion signal |
| `session.go:reconnect()` 3s | `time.Sleep(3s)` blind | **DEMOTED** | 500ms non-correctness pacing (safety net only) |
| `client.go:waitUntilPeersCanSend` | `time.Sleep(10ms)` unbounded | **BOUNDED** | 5s timeout added |
| `client.go:108` | `time.Sleep(100ms)` | Unchanged | Harmless startup pacing |
| `client.go:260` | `time.Sleep(150ms)` | Unchanged | Post-reconnect signal pacing |
| `media.go:50` | `time.After(50ms)` | Unchanged | Intentional drop policy |

## What Changed in Finish Pass

### Fix A — Stream pump: ticker → event-driven
- `proxyStream()` no longer uses `time.NewTicker(10ms)` for data delivery
- Data delivery driven by `mux.WaitForData(sid)` channel signal
- Stream close driven by `mux.StreamClosedCh(sid)` channel signal
- New mux methods: `StreamClosedCh(sid)`, `signalStreamClosed(sid)`
- Close signal fired from: `HandleFrame` (close frame), `CloseStream`, `ResetClient`, `Reset`
- Tests: `TestStreamClosedCh_SignaledOnCloseFrame`, `TestStreamClosedCh_SignaledOnReset`,
  `TestStreamClosedCh_SignaledOnResetClient`, `TestStreamClosedCh_NotSignaledForOtherClient`,
  `TestEventDrivenStreamPump`

### Fix B — Reconnect: fixed delays → completion-driven
- First wait (was 1500ms blind sleep): replaced with `wg.Wait()` — waits for actual
  goroutine completion. 3s timeout as safety net only.
- Second wait (was 3s blind sleep): reduced to 500ms non-correctness pacing.
  Correctness does not depend on this duration — `Connect()` retries independently.
- Both waits remain context-cancellable via `ctx.Done()`.

## Remaining Timing (Non-Correctness)

| Location | Duration | Why it remains |
|----------|----------|---------------|
| `reconnect()` post-close pacing | 500ms | Remote-side courtesy delay; correctness independent |
| `reconnect()` wg.Wait safety net | 3s timeout | Prevents infinite hang; primary mechanism is wg.Wait |
| `waitUntilPeersCanSend` | 10ms poll, 5s bound | Requires Peer interface change for full event-driven |
| `client.go:108` startup pacing | 100ms | One-time startup; not in hot path |
| `client.go:260` reconnect pacing | 150ms | One-time post-reconnect; not correctness |
| `media.go:50` send queue timeout | 50ms | Intentional backpressure drop policy |

**None of these remaining timers are correctness gates.** Removing any of them would not
break transport behavior — it would only affect pacing/courtesy timing.

## Test Coverage Summary

| Test | Validates |
|------|-----------|
| `TestBackpressure_CondWakeOnRead` | Mux Cond-driven backpressure (no polling) |
| `TestStreamClosedCh_SignaledOnCloseFrame` | Stream close signal on close frame |
| `TestStreamClosedCh_SignaledOnReset` | Stream close signal on full Reset |
| `TestStreamClosedCh_SignaledOnResetClient` | Stream close signal on ResetClient |
| `TestStreamClosedCh_NotSignaledForOtherClient` | No false-positive close signals |
| `TestEventDrivenStreamPump` | Full data+close event-driven cycle |
| 26 existing mux tests | Frame format, ordering, OOO, reset, overflow |
| 30 existing telemost tests | Signaling, session, VP8 tunnel |
