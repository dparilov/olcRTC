# Telemost Android Client Plan v0.1

## Goal
Build a basic Android APK around the existing `mobile/mobile.go` olcRTC binding layer.

## MVP scope
- Launch tunnel from Telemost meeting link in clipboard, deep link, or shared text payload
- Parse room id from full link or raw id
- Start/stop tunnel
- Show status states
- Show in-app logs
- Copy log button
- Run diagnostics automatically after tunnel ready
- Manual "Run diagnostics again" button
- Diagnostics include:
  - external IP check
  - resource checks
  - small download check
  - Yandex speedtest
  - Ookla speedtest
  - explicit failure reporting per test

## Non-goals for v0.1
- Auto-discover active Telemost session from native Telemost app
- Full-device VPN routing
- Automatic SSTP/VPN launch
- Production hardening/obfuscation

## Proposed app architecture

### Modules
1. `app` (Android UI shell)
2. `olcrtc-bridge` (gomobile-generated binding from `mobile/mobile.go`)
3. `diagnostics` (HTTP/TCP/resource checks + speedtests)
4. `logging` (buffer + copy/share)

### Main screen
- Tunnel state label
- Parsed meeting / room id
- Buttons:
  - Launch tunnel (resolve meeting link from clipboard or latest intent payload)
  - Stop tunnel
  - Run diagnostics again
  - Copy log
- Diagnostics result area
- Scrollable log area

### State machine
- Idle
- ClipboardMissing
- InvalidMeetingLink
- Starting
- Connecting
- DataChannelUp
- SocksReady
- DiagnosticsRunning
- DiagnosticsFinished
- Error

## Backend integration path
1. Use `mobile/mobile.go` as the runtime API surface
2. Generate Android binding with `gomobile bind`
3. Kotlin app calls:
   - SetLogWriter
   - SetDebug(true)
   - Start(roomID, keyHex, socksPort, duo, socksUser, socksPass)
   - WaitReady(timeoutMillis)
   - Stop()

## Open questions
1. How Yandex speedtest is best executed on Android (web endpoint vs native flow)
2. Whether Ookla requires embedding SDK, CLI wrapper, or web flow
3. Whether diagnostics should run through the local SOCKS path directly or via a future VpnService wrapper

## Minimal toolchain needed
1. JDK (Java toolchain)
2. Android SDK command-line tools
3. Gradle (wrapper is enough once project exists)
4. gomobile
5. Android platform + build-tools packages

## Build order
1. Create Android project skeleton
2. Add minimal single-screen app
3. Add logging buffer + copy log
4. Add clipboard link parsing
5. Add gomobile bridge integration
6. Add tunnel start/stop + wait ready
7. Add diagnostics runner
8. Add speedtests placeholders/integration
9. Build first debug APK

## Validation order (updated after Linux controlled test work)
Before relying on Android-side observations, validate in this order:
1. Linux controlled testbed baseline
2. Android emulator controlled run
3. Real Android device controlled run

Each validation step should use a **clean Telemost room** when possible to avoid stale participant/reconnect artifacts.

## Progress status (2026-04-12)

### Completed
- Android app skeleton created under `android/`
- Gradle project bootstrapped and configured
- JDK 21 installed in user-space
- Android SDK command-line tools installed in user-space
- Android Platform 35, Build-Tools 35.0.0, Platform-Tools installed
- Android NDK 27.2 installed
- `gomobile` / `gobind` installed
- Compose/Kotlin Gradle config fixed so the Android project configures successfully
- Minimal placeholder UI created with:
  - Launch tunnel
  - Stop
  - Run diagnostics again
  - Copy log
  - status / diagnostics / log blocks
- Emulator environment validated:
  - Android Emulator installed
  - AVD `telemost35` created
  - emulator boot confirmed via `adb`
  - debug APK install confirmed
  - `MainActivity` launch confirmed
- Meeting-link intake path hardened locally to support:
  - clipboard candidates
  - launch intents
  - deep links
  - share-intent text

### Current blocker
The immediate blocker for the emulator phase is no longer environment bring-up. It is finishing one clean controlled validation pass after the local meeting-link intake hardening.

A separate deeper technical issue still exists in the bind/toolchain history:
- `link: github.com/wlynxg/anet: invalid reference to net.zoneCache`

But that is no longer the only relevant story for the current app/emulator workflow, because the present APK/emulator path is already far enough along to validate UI-driven startup behavior.

### Important context after Linux validation
On 2026-04-13 the Linux transport baseline was validated much more deeply, including:
- successful clean-room controlled tunnel tests
- expanded diagnostics
- throughput measurements
- shutdown hygiene fixes
- reconnect/presence hygiene fixes

So the next Android phase should be executed against a cleaner and much better understood transport baseline than before.

### Secondary lessons learned
- `gomobile bind` required explicit Android API selection (`21+`) because default API selection conflicted with the installed NDK range.
- `golang.org/x/mobile/bind` had to be anchored in the module graph explicitly for `gobind` resolution.
- The current `olcRTC` mobile path is close to bindable, but not yet Android-ready without dependency fixes or conditional build logic.
