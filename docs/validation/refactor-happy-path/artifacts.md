# Refactor Happy-Path Validation — Artifacts

## Candidate Branch
- Branch: `validation/refactor-happy-path`
- Base commit: `6da9db2` (Items 5+6+7 finish)
- Frozen: 2026-04-23 15:39 MSK

## Commit Stack
```
6da9db2 refactor: finish Item 7 — event-driven stream pump + completion-driven reconnect
31fcabd refactor: remove sleep/poll-driven orchestration from hot paths (Item 7 Phase C)
9922ba9 refactor: spec-backed mux layer with deterministic tests (Item 6 Phase B)
bc51403 refactor: add characterization tests and architectural note for peer layer (Item 5 Phase A)
eb0a837 docs: update v2.0.0 release notes with final baseline SHA and artifact hashes
```

## Server Artifacts (VPS)
- Server binary: `/opt/olcrtc-refactor-validate` (16 MB)
- Bootstrap binary: `/opt/olcrtc-bootstrap-refactor-validate` (9.9 MB)
- State dir: `/opt/olcrtc-state-refactor-validate/`
- Log: `/opt/olcrtc-bootstrap-refactor-validate.log`
- Bootstrap port: 8092
- Tenant port range: 2180-2279
- Encrypt key: `olcrtc-secrets-2026`

## Android Artifact
- APK: `app:/telemost/v2-refactor-happy-path.apk` (Yandex Disk)
- Package: `com.telemost.client` (same as baseline — install replaces baseline)

## Stable V2 Baseline (untouched)
- Server: `/opt/olcrtc` (PID 19186)
- Bootstrap: `/opt/olcrtc-bootstrap-v2` (PID 19180, port 8091)
- State: `/opt/olcrtc-state-v2/`
- Ports: 8091 (bootstrap), 2080-2179 (tenants)

## Isolation Verification
- Separate binary paths ✅
- Separate ports (8092 vs 8091) ✅
- Separate state dir ✅
- Separate logs ✅
- Baseline untouched ✅
