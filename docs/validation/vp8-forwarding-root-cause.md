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
