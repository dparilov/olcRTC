# WS Trace Collection — Test Plan

## Goal
Collect side-by-side WS message traces to determine why VP8 forwarding
fails on Linux but works on Android.

## Trace mechanism
Set env var `OLCRTC_WS_TRACE=/path/to/trace.jsonl` to enable.
Each line is a JSON object: `{"dir":"recv|send","ts":<ms>,"msg":{...}}`

## Artifacts

### Server binary (VPS)
`/opt/olcrtc-trace` — same as baseline but with WS trace logging.
**Does NOT replace `/opt/olcrtc`** — runs on separate ports.

### Android APK
`client-trace-debug.apk` on Yandex Disk `app:/telemost/`
Install alongside or replace the test APK (not production).

## Test Scenarios

### Scenario 1: Linux (VPS srv) + Linux (local cnc)
Already captured. Key finding: no `subscriberSdpOffer` renegotiation.

### Scenario 3: Linux (VPS srv) + Android (cnc)
1. Create fresh room: https://telemost.yandex.ru → "Создать встречу"
2. Start trace server on VPS:
   ```
   ssh root@46.161.15.138
   export OLCRTC_MASTER_SECRET="Software@18"
   export OLCRTC_WS_TRACE="/tmp/trace-srv-s3.jsonl"
   /opt/olcrtc-trace -mode srv -id <ROOM_ID> -socks-port 2097 -dns 1.1.1.1:53 -debug > /tmp/trace-srv-s3.log 2>&1
   ```
3. Connect Android client to same room (install trace APK, connect)
4. Wait 30 seconds
5. Ctrl+C server
6. Collect: `/tmp/trace-srv-s3.jsonl` + `/tmp/trace-srv-s3.log`

### Scenario 4: Android (first) + Linux (VPS srv joins second)
1. Create fresh room
2. Connect Android first
3. Wait 10 seconds
4. Start trace server:
   ```
   export OLCRTC_WS_TRACE="/tmp/trace-srv-s4.jsonl"
   /opt/olcrtc-trace -mode srv -id <ROOM_ID> -socks-port 2098 -dns 1.1.1.1:53 -debug > /tmp/trace-srv-s4.log 2>&1
   ```
5. Wait 30 seconds
6. Collect traces

## Required comparison matrix

| Scenario | 1st peer | 2nd peer | New subSdpOffer after join? | 1st gets VP8 from 2nd? | 2nd gets VP8 from 1st? | Tunnel works? |
|----------|----------|----------|-----------------------------|------------------------|------------------------|---------------|
| 1        | Linux    | Linux    | NO                          | NO (audio only)        | NO (audio only)        | NO            |
| 3        | Linux    | Android  | ?                           | ?                      | ?                      | ?             |
| 4        | Android  | Linux    | ?                           | ?                      | ?                      | ?             |
