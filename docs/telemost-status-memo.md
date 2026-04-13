# Telemost Status Memo

## Transport validation status
`olcRTC` has been validated as a working experimental transport in the scheme:

client -> Telemost/WebRTC/DataChannel -> VPS -> Internet

## Confirmed results
- Server-side on VPS starts and connects to Telemost room
- Client-side starts successfully
- WebRTC PeerConnection is established
- DataChannel opens successfully
- Local SOCKS5 listener starts successfully
- HTTPS traffic passes through the tunnel
- Client egress IP becomes the VPS IP: `46.161.15.138`
- Multiple HTTPS sites were successfully accessed through the tunnel
- Small download test succeeded
- Parallel connection test succeeded on the baseline set

## 2026-04-13 controlled Linux validation update
A full Linux-only controlled validation pass was completed against clean Telemost rooms.

### What is now confirmed
- Clean-room controlled transport baseline works reliably on Linux
- Expanded diagnostics now exist for Linux with:
  - foreign contour IP checks
  - Russian contour IP checks
  - URL batch checks
  - parallel request checks
  - throughput/download probes
- A full human-readable controlled report was produced and exported
- Controlled reconnect under induced instability was exercised successfully
- Reconnect now exits and returns as a single participant in Telemost UI (verified externally)

### Engineering fixes completed during this phase
1. **Initial stream bytes preservation**
   - fixed a client-side issue where early stream bytes after SOCKS connect acknowledgement could be discarded
2. **Intentional shutdown hygiene**
   - graceful leave path now waits for leave acknowledgement
   - shutdown no longer behaves like an accidental reconnect failure
3. **Reconnect/presence hygiene**
   - reconnect callbacks are suppressed during intentional shutdown
   - close-path signaling distinguishes real reconnect from expected close events
   - controlled reconnect no longer leaves duplicate visible participants in Telemost UI

### Practical testing protocol established
For reliable diagnostics, use a **clean Telemost room per controlled test**:
1. start controlled server peer
2. confirm a single visible participant
3. start client diagnostics run
4. collect artifacts
5. stop peers cleanly

This protocol proved much more reliable than reusing rooms that had previous reconnect / stale-presence history.

## Conclusion
`olcRTC` is confirmed as a working experimental transport layer for the target direction:

Android/Client -> VPS -> Internet

It is now also confirmed that Linux controlled testing can be used as a stable baseline before moving to Android emulator and real-device phases.

## Current engineering focus
1. Preserve and use the Linux controlled testbed as the source-of-truth baseline
2. Move next to Android emulator validation using the same clean-room protocol
3. Then return to real Android device validation and Android-specific orchestration polish
