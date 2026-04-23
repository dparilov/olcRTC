# Refactor Happy-Path Validation — Results

## Date: 2026-04-23 16:00 MSK
## Tester: Dmitrii Parilov

| Scenario | Stable V2 | Refactor Candidate | Verdict |
|----------|-----------|--------------------|---------|
| Android → VPS happy path | PASS | PASS | same — no regression |
| SOCKS tunnel data flow | PASS | PASS | same — no regression |
| VPN mode works | PASS | (not yet tested) | — |

## Evidence

### Baseline V2 (port 8091)
- Connected at 10:59 MSK
- TUNNEL DATA #1-4 received (ifconfig.me:443)
- VPN mode confirmed working at 14:23 MSK

### Refactor Candidate (port 8092 via nginx /refactor/)
- Tenant registered: t-c9f427c6
- Server connected, VP8 bidirectional
- SOCKS connections: sid=6,7,9,11,12 all CONNECT_SUCCESS
- VP8 frame #600: 997 bytes (real data, not keepalive)
- Status: Connected, SOCKS Ready

## Decision
- [x] baseline PASS / refactor PASS → **accept Items 5/6/7**

Items 5, 6, 7 transport refactor did NOT break the known-good
production-like Android → VPS server scenario.

## Notes
- Linux VP8 data frame issue remains open (separate track)
- Documented in: docs/validation/e2e-forensic-final.md
