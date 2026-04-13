# Linux Transport Testbed Plan

## Goal
Build a local Linux-only test harness for validating `olcRTC` transport-layer stability independently of Android runtime specifics.

## Scope
This testbed validates:
- client startup on Linux
- SOCKS5 readiness
- external IP via tunnel
- HTTPS probes through SOCKS
- TCP connectivity probe through SOCKS
- small download through SOCKS
- parallel request behavior
- reconnect/recovery observations over repeated or long-running runs

This testbed does **not** validate:
- Android UI
- Android lifecycle
- Android permissions
- gomobile / AAR integration
- emulator/device-specific networking behavior

## Why this exists
We already have evidence that transport works on Linux and partially works on Android.
Before spending more time on emulator/device behavior, we need a stable baseline for the transport layer itself.

## Testbed layers
1. `olcrtc` Linux client binary
2. local SOCKS5 endpoint exposed by the client
3. probe runner that exercises destinations through SOCKS
4. result/log artifacts written under `build/testbed/`

## Inputs
- Telemost room ID
- shared key (hex)
- path to client binary
- local SOCKS port

## Outputs
- client log
- probe summary
- raw probe outputs
- simple pass/fail markers per step

## First milestone
A single command that:
1. launches Linux client
2. waits for SOCKS readiness
3. runs baseline probes
4. writes a summary report
5. stops the client cleanly

## Second milestone
Repeated-run / soak mode that:
- loops baseline probes
- records failures/reconnects
- measures rough stability over time

## Suggested artifact layout
- `build/testbed/client.log`
- `build/testbed/summary.txt`
- `build/testbed/raw/*.out`
- `build/testbed/raw/*.err`

## Initial probe set
- external IP (`https://ifconfig.me`)
- HTTPS: `https://example.com`
- HTTPS: `https://cloudflare.com`
- HTTPS: `https://ifconfig.me/all.json`
- TCP: `1.1.1.1:443`
- small download
- parallel HTTPS requests

## Next extensions
- repeated N-run stability test
- long soak run
- reconnect event counting from logs
- comparison against Android emulator results
