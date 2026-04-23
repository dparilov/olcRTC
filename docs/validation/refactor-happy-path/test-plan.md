# Refactor Happy-Path Validation — Test Plan

## Scope
Validate that Items 5/6/7 refactor did NOT break the known-good
Android → VPS server production-like scenario.

## Out of scope
- Linux VP8 forensic investigation (separate track)
- New feature testing
- Performance benchmarking

## Prerequisites
1. Refactor bootstrap running on VPS port 8092 (isolated)
2. Stable V2 baseline running on VPS port 8091 (untouched)
3. Android device with both APKs available

## Test Steps

### Step 1: Verify baseline still works
1. Open baseline app on Android
2. Create & Launch
3. Verify: Status → Connected, IP shown
4. Verify: VPN mode works (if applicable)
5. Stop

### Step 2: Install refactor candidate
1. Download `v2-refactor-happy-path.apk` from Yandex Disk
2. Install (replaces baseline app — same package)
3. Open Settings → verify server endpoint points to port 8092

### Step 3: Run refactor happy path
1. Configure server endpoint: `https://ru-03.komarovo.online:8092`
   (or however bootstrap endpoint is configured)
2. Create & Launch
3. Verify: Status → Connected, IP shown
4. Verify: VPN mode works
5. Stop

### Step 4: Stop → Restart
1. Stop tunnel
2. Wait 5 seconds
3. Create & Launch again
4. Verify: reconnects and works

### Step 5: Record results
Fill in results table in results.md

## Important
After testing refactor candidate, **reinstall baseline APK** to restore
stable V2 for the evening demo.
