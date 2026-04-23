# Linux VP8 Forensic — Result Matrix

## Date: 2026-04-23

## Comparison Table

| Field | Linux (L1) | Android (A1) | Same/Different |
|-------|-----------|-------------|----------------|
| VP8 track established | YES | YES | Same |
| Keepalive forwarded | YES (10000+ frames, 17 bytes) | YES (17 bytes) | Same |
| Data frame created in sender | YES (frame=3, size=74, err=nil) | YES (from mobile) | Same |
| Data frame WriteSample err | nil | nil (implied) | Same |
| Data frame first byte | 0xb1 | 0xb1 | Same |
| Server receives keepalive | YES (17 bytes, all frames) | YES (17 bytes) | Same |
| **Server receives DATA frame** | **NO (0 frames in 15+ min)** | **YES (frame #271, size=123)** | **DIFFERENT** |
| Data frame size (sender) | 74 bytes (52 payload) | 123 bytes (101 payload) | Different size |
| Frame # when data sent | frame 3 (~0.1s) | frame ~271 (~11s) | Different timing |

## Primary Conclusion

**Outcome 1 — sender/packetization difference** is the leading explanation.

Linux WriteSample() produces RTP output that SFU forwards for keepalive
frames but silently drops for data frames. Android WriteSample() produces
RTP output that SFU forwards for both.

Same Go code, same VP8 prefix (0xb1), same pion WebRTC library.
The difference is in runtime RTP packetization behavior.

## Next Steps — Open Setup Proposal

### Three-party room test
Connect Android client, v2.1 server, and Linux client to the **same room**:

```
┌──────────┐     ┌─────────────┐     ┌──────────────┐
│ Android   │ ──→ │ Telemost SFU│ ←── │ Linux client │
│ (baseline)│ ←── │             │ ──→ │ (forensic)   │
└──────────┘     │             │     └──────────────┘
                  │             │
                  │             │ ←── ┌──────────────┐
                  │             │ ──→ │ VPS server   │
                  └─────────────┘     │ (forensic)   │
                                      └──────────────┘
```

**Purpose**: In the same SFU session, compare whether server receives
DATA frames from Android but not from Linux. This eliminates all
variables except the sender platform.

**Setup**:
1. Android creates room via baseline app (joins first)
2. VPS forensic server joins room (OLCRTC_FORENSIC=1)
3. Linux forensic client joins same room
4. All three connected simultaneously
5. Android sends SOCKS data → server logs DATA frames received
6. Linux sends SOCKS data → server logs whether DATA frames received
7. Compare in same session, same SFU, same room

**How to run**:
```bash
# Step 1: Android creates room via baseline app → note room ID

# Step 2: Start forensic server on VPS
ssh root@VPS
export OLCRTC_MASTER_SECRET="Software@18"
export OLCRTC_FORENSIC=1
/opt/olcrtc-v2.1.1 -mode srv -id <ROOM_ID> -socks-port 2170 \
  -dns 1.1.1.1:53 -debug > /tmp/3party-srv.log 2>&1 &

# Step 3: Start forensic Linux client locally
cd /path/to/olcRTC
export OLCRTC_MASTER_SECRET="Software@18"
go run ./cmd/olcrtc/ -mode cnc -id <ROOM_ID> -socks-port 10960 \
  -dns 1.1.1.1:53 -debug > /tmp/3party-cnt.log 2>&1 &

# Step 4: Wait 30 sec for all to connect

# Step 5: Linux client sends data
curl --socks5 127.0.0.1:10960 http://httpbin.org/ip

# Step 6: Collect evidence
ssh root@VPS 'grep "kind=DATA" /tmp/3party-srv.log'
grep "FORENSIC.*DATA" /tmp/3party-cnt.log
```

**Expected result**:
- Server receives DATA from Android (via ifconfig.me check) → proves Android path works
- Server does NOT receive DATA from Linux curl → confirms Linux packetization issue
- Same room, same SFU session, same time → conclusive evidence

**Key question answered**:
Is the DATA frame drop specific to the Linux sender, or to the
SFU session/path? Three-party test eliminates session/path variable.
