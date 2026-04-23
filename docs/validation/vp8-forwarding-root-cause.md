# VP8 Forwarding Root Cause — PROVEN

## Date: 2026-04-23

## Finding

Telemost SFU does NOT perform subscriber renegotiation when a new
participant joins a room. VP8 video tracks are only included in the
`subscriberSdpOffer` for peers that were **already present** at the
time of connection.

## Evidence

### Production (working) — bootstrap log
```
08:07:40 subscriberSdpOffer (1st and only)
08:07:41 VP8RX: codec=audio/opus
08:07:42 VP8RX: codec=video/VP8   ← VP8 from client (already in room)
08:07:49 TUNNEL DATA #1: 101 bytes ← bidirectional tunnel works
```
Server connects SECOND. Client's VP8 is in first subscriberSdpOffer.

### Test (failing) — server connects first
```
09:22:17 subscriberSdpOffer (1st and only)
09:22:18 VP8RX: codec=audio/opus  ← no VP8 (no one else in room yet)
09:29:44 upsertDescription: client joined
— NO new subscriberSdpOffer
— NO VP8 track ever received from client
```

### Test (working) — server connects second (Android already in room)
```
09:34:25 subscriberSdpOffer (1st and only)
09:34:26 VP8RX: codec=audio/opus
09:34:27 VP8RX: codec=video/VP8   ← VP8 from Android (already in room)
```

## Comparison Matrix

| Scenario | Server joins | Client joins | Server gets VP8? | Tunnel works? |
|----------|-------------|-------------|-----------------|---------------|
| Production | 2nd | 1st (creates room) | YES | YES |
| Test: srv first | 1st | 2nd | NO | NO |
| Test: srv second | 2nd (Android in room) | 1st | YES | YES |

## Root Cause

**Join order determines VP8 forwarding.** The SFU includes remote VP8
tracks in subscriberSdpOffer only for peers already present. No
renegotiation occurs when new peers join.

## Impact on E2E Testing

For automated E2E testing on one VPS, the test harness must ensure:
1. Client peer connects first
2. Server peer connects second
3. Or: implement SFU renegotiation request (not currently supported)

## Not a Regression

This behavior is identical on baseline v2.0.0 and refactored code.
The transport refactor (Items 5/6/7) did not introduce this behavior.

## Update: Linux cross-machine test (2026-04-23 12:38)

### Scenario: Linux client first (local), Linux server second (VPS)

**VP8 forwarding: YES** — server receives VP8 from client.

```
09:39:57 VP8RX: codec=video/VP8 ← VP8 from Linux client RECEIVED
09:39:57 frame #1 20 bytes
```

**SOCKS tunnel: NO** — reset signal lost.

Client sent reset signal at 12:38:52 (before server connected).
Server connected at 09:39:55. Reset signal was not re-sent.
Server never created mux/olcrtc channel.

### Proven: NOT a same-VPS issue

VP8 forwarding works between local Linux and VPS when join order is correct
(client first, server second). The same-VPS failure was purely due to
join order, not network topology.

### Additional finding: reset signal timing

In production, the reset signal works because:
1. Client sends reset at startup (line 109 in client.go)
2. Server is already in room (joined via bootstrap after seeing room on Disk)
3. Server receives reset immediately

In test scenario with correct join order:
1. Client sends reset at startup — server not yet in room
2. Server joins later — reset already sent and lost
3. No mechanism to re-send reset when server appears

Production workaround: `onReconnect` callback (line 265) resends reset.
But initial connect has no equivalent re-send mechanism.

## Updated Comparison Matrix

| Scenario | 1st peer | 2nd peer | Same host? | VP8 works? | Tunnel works? | Why? |
|----------|----------|----------|-----------|-----------|--------------|------|
| Production | Android client | VPS server | No | YES | YES | Server joins after reset sent |
| Test same-VPS | VPS server | VPS client | Yes | NO | NO | Wrong join order |
| Test cross: srv 1st | VPS server | Local client | No | NO | NO | Wrong join order |
| Test cross: cnt 1st | Local client | VPS server | No | YES | NO | Reset signal lost (sent before srv) |
| Test cross: Android 1st | Android | VPS server | No | YES | YES(keepalive) | Correct order + reset timing |
