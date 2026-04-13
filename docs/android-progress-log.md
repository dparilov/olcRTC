# Android Progress Log

## Date
2026-04-12 to 2026-04-13

## Summary
We moved from zero Android infrastructure to a working Android project skeleton with a fully bootstrapped local toolchain. The main backend story also progressed significantly on 2026-04-13: Linux controlled validation, diagnostics, reconnect hygiene, and shutdown hygiene were all exercised and improved so Android can now build on a much cleaner transport baseline.

## What was completed

### Toolchain
Installed locally in user-space:
- JDK 21
- Android SDK command-line tools
- Android Platform 35
- Android Build-Tools 35.0.0
- Android Platform-Tools
- Android NDK 27.2.12479018
- `gomobile`
- `gobind`

### Android app skeleton
Created `android/` project with:
- Gradle settings
- root build config
- app module config
- manifest
- theme
- `MainActivity.kt`
- placeholder UI aligned with Telemost Android spec v0.1

### UI placeholder currently includes
- Launch tunnel
- Stop
- Run diagnostics again
- Copy log
- status text
- meeting text
- diagnostics text
- log area

### Build system status
- Android Gradle project configures successfully
- `./gradlew tasks` works
- Gradle wrapper generated successfully

## Integration findings

### gomobile/Android bind path
We attempted to generate an Android AAR from `mobile/mobile.go` using:
- `gomobile bind -target=android ... ./mobile`

We resolved the following issues in sequence:
1. Missing Android SDK
2. Missing Android NDK
3. Android API range mismatch for bind target
4. `golang.org/x/mobile/bind` not being retained in module graph

### Current hard blocker
Current bind failure:

`link: github.com/wlynxg/anet: invalid reference to net.zoneCache`

## Interpretation
The Android effort has crossed the environment/bootstrap phase.
The remaining problem is a real code-level mobile compatibility issue in the dependency graph, most likely requiring one of:
- conditional compilation for Android
- dependency replacement
- patching/removing the problematic networking path for mobile builds

## Backend baseline update (2026-04-13)
Before returning to Android-specific work, the Linux transport layer was used as the source-of-truth baseline.

### Completed on Linux baseline
- Controlled clean-room Telemost test protocol established
- Expanded diagnostics implemented:
  - foreign/Russian contour IP checks
  - URL batch checks
  - throughput probes
  - parallel request checks
- Human-readable controlled report produced and exported
- Client-side stream bug fixed (initial post-connect bytes preservation)
- Intentional shutdown hygiene fixed
- Reconnect / participant presence hygiene improved and verified
- Controlled reconnect under induced instability verified externally in Telemost UI

### Why this matters for Android
This significantly reduces ambiguity in the next Android phase:
- if emulator/device behavior differs, that difference is now much more likely to be Android-specific rather than a raw transport-layer mystery
- diagnostics design can be ported from the Linux baseline instead of invented from scratch inside Android

## Recommended next step
1. Use the Linux controlled testbed as the baseline reference
2. Move to Android emulator validation with the same clean-room protocol
3. Only after emulator behavior is characterized, return to real-device iteration
4. In parallel when needed, continue improving Android/mobile compatibility in the bind path, especially around:
   - `github.com/wlynxg/anet`
   - Android/mobile-specific dependency graph adaptation
   - gomobile compatibility boundaries
