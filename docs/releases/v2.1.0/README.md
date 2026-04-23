# Release v2.1.0

## What's included
- v2.0.0 baseline (DNS fix, release infrastructure)
- Item 5: peer layer boundaries + characterization tests (30 tests)
- Item 6: mux protocol spec + deterministic tests (32 tests)
- Item 7: event-driven orchestration (stream pump, reconnect, backpressure)

## Validation
- Happy-path: baseline PASS / refactor PASS — no regression
- Mux/crypto loopback: 8/8 PASS (196 MB/s, zero degradation over 40 min)
- 62+ unit/integration tests

## Known separate issues
- Linux VP8 data frame forwarding (documented, not a release blocker)
- See docs/validation/e2e-forensic-final.md

## Artifacts
- Server: `olcrtc-v2.1.0` (16 MB)
- Bootstrap: `olcrtc-bootstrap-v2.1.0` (9.9 MB)
- APK: `client-v2.1.0.apk` on Yandex Disk `app:/telemost/build/`
- versionCode: 210, versionName: 2.1.0

## Rollback
```bash
# Restore v2.0.0
ssh root@VPS 'systemctl stop olcrtc-bootstrap-v2 && \
  cp /opt/olcrtc-releases/2.0.0/olcrtc /opt/olcrtc && \
  cp /opt/olcrtc-releases/2.0.0/olcrtc-bootstrap /opt/olcrtc-bootstrap-v2 && \
  systemctl start olcrtc-bootstrap-v2'
```
