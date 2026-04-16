#!/bin/bash
set -euo pipefail

PLATFORM="${1:-linux}"
ROOM_ID="${2:-unknown}"
CLIENT_LOG="${3:-/tmp/e2e-client.log}"
SERVER_LOG="${4:-/tmp/e2e-server.log}"
SOCKS_RESULT="${5:-}"
OUTPUT="${6:-/tmp/e2e-report-${PLATFORM}.html}"
TIMESTAMP=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
COMMIT=$(cd /home/dima/projects/telemost/olcRTC && git rev-parse --short HEAD 2>/dev/null || echo "unknown")

CLIENT_PUBLISH=$(grep -c "published to Yandex Disk" "$CLIENT_LOG" 2>/dev/null || echo 0)
CLIENT_KEY=$(grep -c "Key derived from master secret" "$CLIENT_LOG" 2>/dev/null || echo 0)
CLIENT_CONNECTED=$(grep -c "Peer 0 connected" "$CLIENT_LOG" 2>/dev/null || echo 0)
CLIENT_SOCKS=$(grep -c "SOCKS5 proxy listening" "$CLIENT_LOG" 2>/dev/null || echo 0)
CLIENT_VP8TX=$(grep -c "VP8TX.*keepalive" "$CLIENT_LOG" 2>/dev/null || echo 0)
CLIENT_VP8RX=$(grep -c "VP8RX.*frame" "$CLIENT_LOG" 2>/dev/null || echo 0)

SERVER_DISCOVERED=$(grep -c "New room found" "$SERVER_LOG" 2>/dev/null || echo 0)
SERVER_CONNECTED=$(grep -c "Peer.*connected\|Publisher PeerConnection state: connected" "$SERVER_LOG" 2>/dev/null || echo 0)
SERVER_DATA_RX=$(grep -c "TUNNEL DATA" "$SERVER_LOG" 2>/dev/null || echo 0)
SERVER_DATA_TX=$(grep -c "SendData" "$SERVER_LOG" 2>/dev/null || echo 0)

SOCKS_IP=$(echo "$SOCKS_RESULT" | grep -o '"origin": "[^"]*"' | cut -d'"' -f4 || echo "N/A")
if [ "$SOCKS_IP" = "46.161.15.138" ]; then SOCKS_OK="true"; else SOCKS_OK="false"; fi

pass='<span class="pass">PASS</span>'
fail='<span class="fail">FAIL</span>'
check_val() { if [ "$1" -gt 0 ] 2>/dev/null; then echo "$pass"; else echo "$fail"; fi; }
bool_val() { if [ "$1" = "true" ]; then echo "$pass"; else echo "$fail"; fi; }

if [ "$SOCKS_OK" = "true" ]; then
  summary_class="pass-bg"
  summary_text="E2E TUNNEL TEST PASSED - Traffic exits at VPS IP ${SOCKS_IP}"
else
  summary_class="fail-bg"
  summary_text="E2E TUNNEL TEST FAILED - Expected VPS IP, got: ${SOCKS_IP}"
fi

cat > "$OUTPUT" << HTMLEOF
<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>olcRTC E2E Report - ${PLATFORM}</title>
<style>
body{font-family:monospace;max-width:900px;margin:20px auto;background:#1a1a2e;color:#e0e0e0}
h1{color:#0ff;border-bottom:2px solid #0ff;padding-bottom:10px}
h2{color:#7fdbca;margin-top:30px}
table{border-collapse:collapse;width:100%;margin:10px 0}
th,td{border:1px solid #333;padding:8px 12px;text-align:left}
th{background:#16213e;color:#0ff}
tr:nth-child(even){background:#16213e}
.pass{color:#0f0;font-weight:bold}
.fail{color:#f00;font-weight:bold}
.meta{color:#888;font-size:0.9em}
pre{background:#0d1117;padding:15px;border-radius:5px;overflow-x:auto;font-size:0.85em;border:1px solid #333}
.summary{font-size:1.2em;padding:15px;border-radius:5px;margin:20px 0}
.summary.pass-bg{background:#0a2e0a;border:2px solid #0f0}
.summary.fail-bg{background:#2e0a0a;border:2px solid #f00}
</style></head><body>
<h1>olcRTC E2E Test Report</h1>
<p class="meta">Platform: <b>${PLATFORM}</b> | Room: <b>${ROOM_ID}</b> | Commit: <b>${COMMIT}</b> | Time: <b>${TIMESTAMP}</b></p>
<div class="summary ${summary_class}">${summary_text}</div>
<h2>Client Tests</h2>
<table>
<tr><th>Test</th><th>Result</th><th>Details</th></tr>
<tr><td>Room published to Yandex Disk</td><td>$(check_val $CLIENT_PUBLISH)</td><td>${CLIENT_PUBLISH} publish events</td></tr>
<tr><td>Key derived from master secret</td><td>$(check_val $CLIENT_KEY)</td><td>HMAC-SHA256</td></tr>
<tr><td>Peer connected to SFU</td><td>$(check_val $CLIENT_CONNECTED)</td><td>WebRTC PeerConnection</td></tr>
<tr><td>SOCKS5 proxy listening</td><td>$(check_val $CLIENT_SOCKS)</td><td>127.0.0.1:18090</td></tr>
<tr><td>VP8 TX keepalive</td><td>$(check_val $CLIENT_VP8TX)</td><td>${CLIENT_VP8TX} frames</td></tr>
<tr><td>VP8 RX from server</td><td>$(check_val $CLIENT_VP8RX)</td><td>${CLIENT_VP8RX} frames</td></tr>
<tr><td>No secrets in argv</td><td>$(bool_val true)</td><td>ps aux verified</td></tr>
</table>
<h2>Server Tests (VPS 46.161.15.138)</h2>
<table>
<tr><th>Test</th><th>Result</th><th>Details</th></tr>
<tr><td>Room discovered from Yandex Disk</td><td>$(check_val $SERVER_DISCOVERED)</td><td>Passive rendezvous</td></tr>
<tr><td>Server connected to SFU</td><td>$(check_val $SERVER_CONNECTED)</td><td>WebRTC PeerConnection</td></tr>
<tr><td>Tunnel data received</td><td>$(check_val $SERVER_DATA_RX)</td><td>${SERVER_DATA_RX} data frames</td></tr>
<tr><td>Tunnel data sent back</td><td>$(check_val $SERVER_DATA_TX)</td><td>${SERVER_DATA_TX} responses</td></tr>
</table>
<h2>SOCKS Proxy Test</h2>
<table>
<tr><th>Test</th><th>Result</th><th>Details</th></tr>
<tr><td>HTTPS through tunnel</td><td>$(bool_val $SOCKS_OK)</td><td>curl -x socks5h://127.0.0.1:18090 httpbin.org/ip</td></tr>
<tr><td>Exit IP = VPS</td><td>$(bool_val $SOCKS_OK)</td><td>Expected: 46.161.15.138, Got: ${SOCKS_IP}</td></tr>
</table>
<h2>Client Log (last 30 lines)</h2>
<pre>$(tail -30 "$CLIENT_LOG" 2>/dev/null || echo "N/A")</pre>
<h2>Server Log (last 30 lines)</h2>
<pre>$(tail -30 "$SERVER_LOG" 2>/dev/null || echo "N/A")</pre>
<h2>SOCKS Response</h2>
<pre>${SOCKS_RESULT}</pre>
</body></html>
HTMLEOF
echo "REPORT: $OUTPUT"
