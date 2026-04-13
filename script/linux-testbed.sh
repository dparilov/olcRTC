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
SPEEDTEST_ENABLE="${SPEEDTEST_ENABLE:-1}"
CLIENT_LOG="$OUT_DIR/client.log"
SUMMARY="$OUT_DIR/summary.txt"
REPORT_JSON="$OUT_DIR/report.json"
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
cat > "$REPORT_JSON" <<'JSON'
{
  "room_id": null,
  "socks_port": null,
  "datachannels": {},
  "ip_checks": {},
  "url_checks": {},
  "downloads": {},
  "parallel": {},
  "speedtests": {}
}
JSON

echo "== Linux transport testbed ==" | tee -a "$SUMMARY"
date | tee -a "$SUMMARY"
echo "ROOM_ID=$ROOM_ID" | tee -a "$SUMMARY"
echo "SOCKS_PORT=$SOCKS_PORT" | tee -a "$SUMMARY"
jq --arg room "$ROOM_ID" --argjson port "$SOCKS_PORT" '.room_id=$room | .socks_port=$port' "$REPORT_JSON" > "$REPORT_JSON.tmp" && mv "$REPORT_JSON.tmp" "$REPORT_JSON"

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
jq --arg def "$(grep -q "Received datachannel: default" "$CLIENT_LOG" && echo seen || echo not-seen)" --arg olc "$( [[ "$OLCRTC_SEEN" == "1" ]] && echo seen || echo not-seen )" '.datachannels.default=$def | .datachannels.olcrtc=$olc' "$REPORT_JSON" > "$REPORT_JSON.tmp" && mv "$REPORT_JSON.tmp" "$REPORT_JSON"

PROXY="socks5h://$SOCKS_HOST:$SOCKS_PORT"
probe_reason() {
  local err="$1"
  local reason="unknown"
  if grep -q "Can't complete SOCKS5 connection" "$err" 2>/dev/null; then
    reason="socks-connect-failed"
  elif grep -q "Connection timed out" "$err" 2>/dev/null; then
    reason="timeout"
  elif grep -q "Could not resolve host" "$err" 2>/dev/null; then
    reason="dns-failed"
  fi
  printf "%s" "$reason"
}
run_probe() {
  local bucket="$1"
  local name="$2"
  local url="$3"
  local body="$RAW_DIR/${name}.body"
  local meta="$RAW_DIR/${name}.meta"
  local err="$RAW_DIR/${name}.err"
  if curl --silent --show-error --fail --proxy "$PROXY" --connect-timeout "$PROBE_TIMEOUT" --max-time "$PROBE_TIMEOUT" --output "$body" --write-out "http_code=%{http_code} time_total=%{time_total}\n" "$url" > "$meta" 2> "$err"; then
    printf "%s -> %s" "$url" "$(cat "$meta")" | tee -a "$SUMMARY"
    jq --arg bucket "$bucket" --arg name "$name" --arg url "$url" --arg meta "$(tr -d '\n' < "$meta")" '.[$bucket][$name]={url:$url,result:$meta,status:"ok"}' "$REPORT_JSON" > "$REPORT_JSON.tmp" && mv "$REPORT_JSON.tmp" "$REPORT_JSON"
  else
    local reason
    reason=$(probe_reason "$err")
    echo "$url -> FAIL ($reason)" | tee -a "$SUMMARY"
    jq --arg bucket "$bucket" --arg name "$name" --arg url "$url" --arg reason "$reason" '.[$bucket][$name]={url:$url,status:"fail",reason:$reason}' "$REPORT_JSON" > "$REPORT_JSON.tmp" && mv "$REPORT_JSON.tmp" "$REPORT_JSON"
  fi
}

echo "\n== IP CHECKS ==" | tee -a "$SUMMARY"
echo "-- foreign contour --" | tee -a "$SUMMARY"
run_probe "ip_checks" "ip_ifconfig_me" "https://ifconfig.me"
run_probe "ip_checks" "ip_api_ipify_org" "https://api.ipify.org"
echo "-- russian contour --" | tee -a "$SUMMARY"
run_probe "ip_checks" "ip_yandex_internet" "https://yandex.ru/internet"
run_probe "ip_checks" "ip_ya_ru" "https://ya.ru"

echo "\n== URL BATCH ==" | tee -a "$SUMMARY"
echo "-- foreign urls --" | tee -a "$SUMMARY"
run_probe "url_checks" "example_com" "https://example.com"
run_probe "url_checks" "cloudflare_com" "https://cloudflare.com"
run_probe "url_checks" "google_com" "https://www.google.com"
run_probe "url_checks" "ifconfig_me_all_json" "https://ifconfig.me/all.json"
run_probe "url_checks" "api_telegram_org" "https://api.telegram.org"
echo "-- russian urls --" | tee -a "$SUMMARY"
run_probe "url_checks" "ya_ru" "https://ya.ru"
run_probe "url_checks" "yandex_ru" "https://yandex.ru"
run_probe "url_checks" "telemost_yandex_ru" "https://telemost.yandex.ru"
run_probe "url_checks" "yandex_internet" "https://yandex.ru/internet"

echo "\n== TELEGRAM HEAD ==" | tee -a "$SUMMARY"
if curl --silent --show-error --head --proxy "$PROXY" --connect-timeout "$PROBE_TIMEOUT" --max-time "$PROBE_TIMEOUT" https://api.telegram.org > "$RAW_DIR/telegram-head.out" 2> "$RAW_DIR/telegram-head.err"; then
  head -n 5 "$RAW_DIR/telegram-head.out" | tee -a "$SUMMARY"
  jq --arg head "$(head -n 5 "$RAW_DIR/telegram-head.out" | tr '\n' ' ' | sed 's/  */ /g')" '.url_checks.telegram_head={status:"ok",result:$head}' "$REPORT_JSON" > "$REPORT_JSON.tmp" && mv "$REPORT_JSON.tmp" "$REPORT_JSON"
else
  echo "https://api.telegram.org -> FAIL" | tee -a "$SUMMARY"
  jq '.url_checks.telegram_head={status:"fail"}' "$REPORT_JSON" > "$REPORT_JSON.tmp" && mv "$REPORT_JSON.tmp" "$REPORT_JSON"
fi

echo "\n== DOWNLOAD THROUGHPUT ==" | tee -a "$SUMMARY"
measure_download() {
  local name="$1"
  local url="$2"
  local out="$RAW_DIR/${name}.download"
  local meta="$RAW_DIR/${name}.download.meta"
  local err="$RAW_DIR/${name}.download.err"
  if curl --silent --show-error --fail --proxy "$PROXY" --connect-timeout "$PROBE_TIMEOUT" --max-time "$PROBE_TIMEOUT" "$url" -o "$out" --write-out "size=%{size_download} time=%{time_total} speed=%{speed_download}\n" > "$meta" 2> "$err"; then
    printf "%s -> %s" "$url" "$(cat "$meta")" | tee -a "$SUMMARY"
    jq --arg name "$name" --arg url "$url" --arg meta "$(tr -d '\n' < "$meta")" '.downloads[$name]={url:$url,status:"ok",result:$meta}' "$REPORT_JSON" > "$REPORT_JSON.tmp" && mv "$REPORT_JSON.tmp" "$REPORT_JSON"
  else
    local reason
    reason=$(probe_reason "$err")
    echo "$url -> FAIL ($reason)" | tee -a "$SUMMARY"
    jq --arg name "$name" --arg url "$url" --arg reason "$reason" '.downloads[$name]={url:$url,status:"fail",reason:$reason}' "$REPORT_JSON" > "$REPORT_JSON.tmp" && mv "$REPORT_JSON.tmp" "$REPORT_JSON"
  fi
}
measure_download "small_ifconfig" "https://ifconfig.me/all.json"
measure_download "medium_google_robots" "https://www.google.com/robots.txt"
measure_download "medium_yandex_robots" "https://yandex.ru/robots.txt"

echo "\n== PARALLEL ==" | tee -a "$SUMMARY"
(
  curl --silent --show-error --fail --proxy "$PROXY" --connect-timeout "$PROBE_TIMEOUT" --max-time "$PROBE_TIMEOUT" --write-out "example http_code=%{http_code} time_total=%{time_total}\n" https://example.com -o /dev/null > "$RAW_DIR/parallel-example.meta" 2> "$RAW_DIR/parallel-example.err" &
  p1=$!
  curl --silent --show-error --fail --proxy "$PROXY" --connect-timeout "$PROBE_TIMEOUT" --max-time "$PROBE_TIMEOUT" --write-out "ifconfig http_code=%{http_code} time_total=%{time_total}\n" https://ifconfig.me -o /dev/null > "$RAW_DIR/parallel-ifconfig.meta" 2> "$RAW_DIR/parallel-ifconfig.err" &
  p2=$!
  curl --silent --show-error --fail --proxy "$PROXY" --connect-timeout "$PROBE_TIMEOUT" --max-time "$PROBE_TIMEOUT" --write-out "ya http_code=%{http_code} time_total=%{time_total}\n" https://ya.ru -o /dev/null > "$RAW_DIR/parallel-ya.meta" 2> "$RAW_DIR/parallel-ya.err" &
  p3=$!
  wait "$p1" || true
  wait "$p2" || true
  wait "$p3" || true
)
cat "$RAW_DIR/parallel-"*.meta 2>/dev/null | tee -a "$SUMMARY" >/dev/null || true
jq --arg result "$(cat "$RAW_DIR"/parallel-*.meta 2>/dev/null | tr '\n' ';' )" '.parallel={status:"done",result:$result}' "$REPORT_JSON" > "$REPORT_JSON.tmp" && mv "$REPORT_JSON.tmp" "$REPORT_JSON"

echo "\n== SPEEDTESTS ==" | tee -a "$SUMMARY"
if [[ "$SPEEDTEST_ENABLE" == "1" ]] && command -v speedtest >/dev/null 2>&1; then
  if HTTPS_PROXY="$PROXY" HTTP_PROXY="$PROXY" ALL_PROXY="$PROXY" speedtest --accept-license --accept-gdpr --format=json > "$RAW_DIR/speedtest.json" 2> "$RAW_DIR/speedtest.err"; then
    python3 - <<'PY' "$RAW_DIR/speedtest.json" "$SUMMARY"
import json,sys
p,s=sys.argv[1],sys.argv[2]
d=json.load(open(p))
out=[]
out.append(f"download_bps={d.get('download',{}).get('bandwidth')}")
out.append(f"upload_bps={d.get('upload',{}).get('bandwidth')}")
out.append(f"latency_ms={d.get('ping',{}).get('latency')}")
open(s,'a').write('\n'.join(out)+'\n')
PY
    jq --slurpfile sp "$RAW_DIR/speedtest.json" '.speedtests.ookla={status:"ok",result:$sp[0]}' "$REPORT_JSON" > "$REPORT_JSON.tmp" && mv "$REPORT_JSON.tmp" "$REPORT_JSON"
  else
    echo "speedtest -> FAIL" | tee -a "$SUMMARY"
    jq --arg err "$(tr '\n' ' ' < "$RAW_DIR/speedtest.err")" '.speedtests.ookla={status:"fail",error:$err}' "$REPORT_JSON" > "$REPORT_JSON.tmp" && mv "$REPORT_JSON.tmp" "$REPORT_JSON"
  fi
else
  echo "speedtest -> SKIPPED" | tee -a "$SUMMARY"
  jq '.speedtests.ookla={status:"skipped"}' "$REPORT_JSON" > "$REPORT_JSON.tmp" && mv "$REPORT_JSON.tmp" "$REPORT_JSON"
fi

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
