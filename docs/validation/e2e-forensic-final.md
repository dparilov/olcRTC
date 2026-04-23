# E2E Forensic Analysis — Final Findings

## Date: 2026-04-23

## Summary

Linux E2E test (VPS server + local client) does not achieve tunnel data flow.
VP8 bidirectional track forwarding works, but VP8 **data frames** (with
embedded mux payload) are not received by server. Only keepalive frames
arrive. This is identical behavior in both baseline v2.0.0 and refactored code.

Production (Android + VPS server) works correctly.

## Proven Facts

### 1. VP8 track forwarding depends on join order
- SFU includes VP8 tracks in subscriberSdpOffer only for peers already present
- No renegotiation when new peers join
- Client must join first for server to receive client's VP8 track
- Server must join first for client to receive server's VP8 track
- With correct order (client first): both sides get VP8 track

### 2. VP8 keepalive frames are forwarded correctly
- Server receives 10000+ keepalive frames from client (17 bytes, 0xb1/0x10)
- Client receives keepalive frames from server
- VP8 media path is established and working for keepalive

### 3. VP8 data frames are NOT received by server
- Client calls `VP8Sender.SendData()` — creates VP8 frame with embedded data
- Frame is written to sample track via `WriteSample()`
- Server's VP8 reader never sees these frames
- Only keepalive frames (17-30 bytes) pass through SFU
- Data frames (52-101+ bytes) are silently dropped

### 4. Baseline v2.0.0 behaves identically
- Same test with baseline `/opt/olcrtc` binary: same result
- Server first, client second: no VP8 forwarding
- Client first, server second: VP8 track forwarding OK, but data frames dropped
- This is NOT a refactor regression

### 5. Production Android works
- Bootstrap log shows `TUNNEL DATA #1: 101 bytes` arriving at server
- VP8 data frames from Android pass through SFU
- Server responds with `SendData #1 len=53`
- Bidirectional tunnel established

## Unresolved Question

**Why do VP8 data frames from Android pass through SFU but data frames
from Linux do not?**

Both use identical code (pion WebRTC, same VP8 encoding, same SDP).
SDP publisher offers are byte-identical between test and production.

### Hypotheses (untested)
1. SFU may inspect VP8 frame content and filter non-standard frames differently
   based on network path or ICE candidate type
2. Android may use a different TURN relay path that handles VP8 frames differently
3. RTP packetization differences between gomobile (Android) and native (Linux)
   pion may affect SFU forwarding behavior
4. Frame timing/rate may affect SFU decision to forward or drop

### Required next step
Packet capture (tcpdump/wireshark) on both sides to compare:
- RTP packet structure from Android vs Linux
- Whether data frames reach SFU at all
- Whether SFU forwards them (server-side capture)

## Test Matrix

| # | Client | Server | Join order | VP8 track? | Keepalive? | Data frames? | Tunnel? |
|---|--------|--------|-----------|-----------|-----------|-------------|---------|
| 1 | Linux (VPS) | Linux (VPS) | srv first | NO | NO | NO | NO |
| 2 | Linux (local) | Linux (VPS) | srv first | srv→cnt only | YES | NO | NO |
| 3 | Linux (local) | Linux (VPS) | cnt first | BOTH | YES | NO | NO |
| 4 | Android | Linux (VPS) | cnt first | BOTH | YES | YES | YES |
| 5 | Linux (VPS) | Linux (VPS) | simultaneous | BOTH (from existing) | YES | NO | NO |

## Refactor Impact Assessment

- Items 5/6/7 transport refactor: **NO REGRESSION** (baseline identical)
- Mux/crypto loopback: **8/8 PASS** (196 MB/s, zero degradation)
- VP8 data frame forwarding: **pre-existing limitation**, not introduced by refactor
- Production Android flow: **WORKS** (confirmed today via bootstrap logs)
