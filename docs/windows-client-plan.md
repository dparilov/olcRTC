# Telemost Windows Client Plan v0.1

## Goal
Build a minimal Windows desktop UI around the existing Go client runtime, analogous to the Android app, without introducing a separate transport implementation.

## MVP scope
- Single-window Fyne desktop app
- Room/link input accepting a full Telemost link or raw room ID
- Launch tunnel button
- Stop button
- Status label
- SOCKS host/port display for the local proxy endpoint
- Diagnostics status label
- Manual "Run diagnostics again" button
- Scrollable log area
- Copy log button
- Reuse the existing `mobile` runtime API, which already wraps the current olcRTC client flow

## Non-goals
- Native Windows installer
- Tray integration
- Auto-discovery of meetings from the browser or Telemost desktop app
- Production-grade settings management
- Server mode UI
- A new desktop-only transport/runtime path

## Runtime approach
1. Parse room ID from the input field
2. Start the tunnel through `mobile.Start(...)`
3. Wait for readiness through `mobile.WaitReady(...)`
4. Display the fixed local SOCKS endpoint used by the MVP
5. Run lightweight SOCKS-routed diagnostics from the desktop app
6. Stop the tunnel through `mobile.Stop()`

## MVP defaults
- SOCKS host: `127.0.0.1`
- SOCKS port: `1080`
- Tunnel mode: single channel
- Shared key: same static development key currently used by the Android app

## Validation target
- The new desktop target should compile on the current Linux host
- The same code path should remain suitable for a later Windows build with Fyne
