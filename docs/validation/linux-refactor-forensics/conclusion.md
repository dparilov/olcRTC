# Linux VP8 Forensic — Conclusion (Phase 1)

## Status: Evidence collected, root cause narrowed

## What is proven
1. Linux VP8 keepalive frames pass through SFU correctly
2. Linux VP8 DATA frames never arrive at server (confirmed over 15+ min)
3. Android VP8 DATA frames arrive at server (confirmed via forensic logs)
4. Same Go code, same VP8 encoding, same pion library
5. Not a refactor regression (v2.1.0 happy-path PASS)

## Primary explanation
RTP packetization difference between Linux native and Android gomobile
runtime causes SFU to drop Linux data frames while accepting Android ones.

## What is NOT yet proven
- Exact RTP packet difference (needs tcpdump/wireshark)
- Whether SFU drops based on frame size, timing, or content inspection
- Whether pion behaves differently under gomobile vs native Go

## Recommended next investigation
Three-party room test (documented in result-matrix.md) to confirm
the issue is sender-specific, not session-specific.
