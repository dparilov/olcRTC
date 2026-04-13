# Android Progress Log

## Date
2026-04-12

## Summary
We moved from zero Android infrastructure to a working Android project skeleton with a fully bootstrapped local toolchain. The current blocker is no longer environment setup, but a code/dependency issue during `gomobile bind`.

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

## Recommended next step
Inspect where `github.com/wlynxg/anet` enters the graph and determine:
1. whether it is required on Android/mobile
2. whether it can be disabled behind build tags
3. whether a compatible replacement exists
4. whether the dependency should be pinned or patched for Android builds
