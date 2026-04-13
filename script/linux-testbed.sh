#!/usr/bin/env bash
set -euo pipefail

ROOM_ID="${ROOM_ID:-}"
KEY_HEX="${KEY_HEX:-}"
SOCKS_PORT="${SOCKS_PORT:-18090}"
BINARY="${BINARY:-./build/olcrtc-linux-amd64}"
DATA_DIR="${DATA_DIR:-./data}"
OUT_DIR="${OUT_DIR:-./build/testbed}"
SOCKS_HOST="127.0.0.1"
PROBE_TIMEOUT="${PROBE_TIMEOUT:-15}"
CLIENT_LOG="$OUT_DIR/client.log"
SUMMARY="$OUT_DIR/summary.txt"
RAW_DIR="$OUT_DIR/raw"

if [[ -z "$ROOM_ID" ]]; then
  echo "ROOM_ID is required" >&2
  exit 1
fi

if [[ -z "$KEY_HEX" ]]; then
  echo "KEY_HEX is required" >&2
  exit 1
fi

if [[ ! -x "$BINARY" ]]; then
  echo "Binary not found or not executable: $BINARY" >&2
  exit 1
fi

mkdir -p "$RAW_DIR"
: > "$CLIENT_LOG"
: > "$SUMMARY"

echo "== Linux transport testbed ==" | tee -a "$SUMMARY"
date | tee -a "$SUMMARY"
echo "ROOM_ID=$ROOM_ID" | tee -a "$SUMMARY"
echo "SOCKS_PORT=$SOCKS_PORT" | tee -a "$SUMMARY"

cleanup() {
  if [[ -n "${CLIENT_PID:-}" ]] && kill -0 "$CLIENT_PID" 2>/dev/null; then
    kill "$CLIENT_PID" 2>/dev/null || true
    wait "$CLIENT_PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT

"$BINARY" -mode cnc -id "$ROOM_ID" -key "$KEY_HEX" -socks-port "$SOCKS_PORT" -socks-host "$SOCKS_HOST" -data "$DATA_DIR" -debug >"$CLIENT_LOG" 2>&1 &
CLIENT_PID=$!

echo "\n== WAIT READY ==" | tee -a "$SUMMARY"
READY=0
for _ in $(seq 1 40); do
  if grep -q "SOCKS5 proxy listening" "$CLIENT_LOG" 2>/dev/null; then
    READY=1
    break
  fi
  if ! kill -0 "$CLIENT_PID" 2>/dev/null; then
    break
  fi
  sleep 1
done

if [[ "$READY" != "1" ]]; then
  echo "FAIL: tunnel did not reach SOCKS ready" | tee -a "$SUMMARY"
  tail -n 120 "$CLIENT_LOG" | tee "$RAW_DIR/client-tail.txt" >/dev/null
  exit 1
fi

echo "OK: SOCKS ready" | tee -a "$SUMMARY"

echo "\n== DATACHANNEL DIAG ==" | tee -a "$SUMMARY"
if grep -q "Received datachannel: default" "$CLIENT_LOG"; then
  echo "default channel: seen" | tee -a "$SUMMARY"
else
  echo "default channel: not seen" | tee -a "$SUMMARY"
fi

OLCRTC_SEEN=0
for _ in $(seq 1 8); do
  if grep -q "Received datachannel: olcrtc" "$CLIENT_LOG"; then
    OLCRTC_SEEN=1
    break
  fi
  sleep 1
done
if [[ "$OLCRTC_SEEN" == "1" ]]; then
  echo "olcrtc channel: seen" | tee -a "$SUMMARY"
else
  echo "olcrtc channel: NOT seen" | tee -a "$SUMMARY"
fi

PROXY="socks5h://$SOCKS_HOST:$SOCKS_PORT"
run_probe() {
  local name="$1"
  local url="$2"
  local body="$RAW_DIR/${name}.body"
  local meta="$RAW_DIR/${name}.meta"
  local err="$RAW_DIR/${name}.err"
  if curl --silent --show-error --fail --proxy "$PROXY" --connect-timeout "$PROBE_TIMEOUT" --max-time "$PROBE_TIMEOUT" --output "$body" --write-out "http_code=%{http_code} time_total=%{time_total}\n" "$url" > "$meta" 2> "$err"; then
    printf "%s -> %s" "$url" "$(cat "$meta")" | tee -a "$SUMMARY"
  else
    local reason="unknown"
    if grep -q "Can't complete SOCKS5 connection" "$err" 2>/dev/null; then
      reason="socks-connect-failed"
    elif grep -q "Connection timed out" "$err" 2>/dev/null; then
      reason="timeout"
    fi
    echo "$url -> FAIL ($reason)" | tee -a "$SUMMARY"
  fi
}

echo "\n== EXTERNAL IP ==" | tee -a "$SUMMARY"
run_probe "external-ip" "https://ifconfig.me"

echo "\n== HTTPS ==" | tee -a "$SUMMARY"
run_probe "example.com" "https://example.com"
run_probe "cloudflare.com" "https://cloudflare.com"
run_probe "ifconfig.me_all.json" "https://ifconfig.me/all.json"

echo "\n== TELEGRAM HEAD ==" | tee -a "$SUMMARY"
if curl --silent --show-error --head --proxy "$PROXY" --connect-timeout "$PROBE_TIMEOUT" --max-time "$PROBE_TIMEOUT" https://api.telegram.org > "$RAW_DIR/telegram-head.out" 2> "$RAW_DIR/telegram-head.err"; then
  head -n 5 "$RAW_DIR/telegram-head.out" | tee -a "$SUMMARY"
else
  echo "https://api.telegram.org -> FAIL" | tee -a "$SUMMARY"
fi

echo "\n== SMALL DOWNLOAD ==" | tee -a "$SUMMARY"
if curl --silent --show-error --fail --proxy "$PROXY" --connect-timeout "$PROBE_TIMEOUT" --max-time "$PROBE_TIMEOUT" https://ifconfig.me/all.json -o "$RAW_DIR/small-download.json" 2> "$RAW_DIR/small-download.err"; then
  wc -c "$RAW_DIR/small-download.json" | tee -a "$SUMMARY"
else
  echo "FAIL: small download" | tee -a "$SUMMARY"
fi

echo "\n== PARALLEL ==" | tee -a "$SUMMARY"
(
  curl --silent --show-error --fail --proxy "$PROXY" --connect-timeout "$PROBE_TIMEOUT" --max-time "$PROBE_TIMEOUT" --write-out "example http_code=%{http_code} time_total=%{time_total}\n" https://example.com -o /dev/null > "$RAW_DIR/parallel-example.meta" 2> "$RAW_DIR/parallel-example.err" &
  p1=$!
  curl --silent --show-error --fail --proxy "$PROXY" --connect-timeout "$PROBE_TIMEOUT" --max-time "$PROBE_TIMEOUT" --write-out "ifconfig http_code=%{http_code} time_total=%{time_total}\n" https://ifconfig.me -o /dev/null > "$RAW_DIR/parallel-ifconfig.meta" 2> "$RAW_DIR/parallel-ifconfig.err" &
  p2=$!
  wait "$p1" || true
  wait "$p2" || true
)
cat "$RAW_DIR/parallel-"*.meta 2>/dev/null | tee -a "$SUMMARY" >/dev/null || true

CLIENT_SOCKS_STARTS=$(grep -c "SOCKS5_START" "$CLIENT_LOG" || true)
CLIENT_OLCRTC_CHANNELS=$(grep -c "Received datachannel: olcrtc" "$CLIENT_LOG" || true)

echo "\n== DIAG SUMMARY ==" | tee -a "$SUMMARY"
echo "client socks starts: $CLIENT_SOCKS_STARTS" | tee -a "$SUMMARY"
echo "olcrtc channel count: $CLIENT_OLCRTC_CHANNELS" | tee -a "$SUMMARY"
if [[ "$CLIENT_OLCRTC_CHANNELS" == "0" ]]; then
  echo "diagnosis: session reached SOCKS ready but never exposed olcrtc datachannel" | tee -a "$SUMMARY"
fi

echo "\n== CLIENT LOG TAIL ==" | tee -a "$SUMMARY"
tail -n 80 "$CLIENT_LOG" | tee "$RAW_DIR/client-tail.txt" >/dev/null

echo "\nDONE" | tee -a "$SUMMARY"
echo "Artifacts: $OUT_DIR"
