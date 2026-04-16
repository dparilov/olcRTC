#!/bin/bash
# rotate-token-server.sh — Replace OAuth token on olcRTC server
# Usage: OLCRTC_MASTER_SECRET=... OLCRTC_OAUTH_TOKEN=<new-token> \
#        ./script/rotate-token-server.sh <VPS_HOST>
#
# This script:
#   1. Validates new token locally (Disk read test)
#   2. Updates secrets file on VPS with new token
#   3. Restarts server
#   4. Verifies server is running
#
# The old token remains valid until revoked in Yandex ID.
set -euo pipefail

VPS_HOST="${1:?Usage: rotate-token-server.sh <VPS_HOST>}"

: "${OLCRTC_MASTER_SECRET:?ERROR: OLCRTC_MASTER_SECRET must be set}"
: "${OLCRTC_OAUTH_TOKEN:?ERROR: OLCRTC_OAUTH_TOKEN (new token) must be set}"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
OLCRTC_BIN="$PROJECT_ROOT/olcrtc"

if [ ! -x "$OLCRTC_BIN" ]; then
    echo "==> Building olcrtc for local validation..."
    cd "$PROJECT_ROOT" && go build -o "$OLCRTC_BIN" ./cmd/olcrtc
fi

echo "==> Step 1: Validate new token locally..."
"$OLCRTC_BIN" -mode rotate-token
echo ""

echo "==> Step 2: Updating token on $VPS_HOST..."
ssh "root@${VPS_HOST}" bash -s << REMOTE
set -euo pipefail

pkill -f olcrtc || true
sleep 1

# Write secrets file with new token
cat > /opt/olcrtc.env << 'ENVEOF'
OLCRTC_MASTER_SECRET=${OLCRTC_MASTER_SECRET}
OLCRTC_OAUTH_TOKEN=${OLCRTC_OAUTH_TOKEN}
ENVEOF
chmod 600 /opt/olcrtc.env

# Health check with new token
export \$(cat /opt/olcrtc.env | xargs)
/opt/olcrtc -mode check

# Start server
nohup bash -c 'export \$(cat /opt/olcrtc.env | xargs) && /opt/olcrtc -mode srv --discover -debug' > /opt/olcrtc.log 2>&1 &
echo "Server started (PID=\$!)"

sleep 2
if pgrep -f "olcrtc.*-mode srv" > /dev/null; then
    echo "==> Token rotation successful"
else
    echo "ERROR: Server failed to start"
    tail -10 /opt/olcrtc.log
    exit 1
fi
REMOTE

echo ""
echo "==> Token replaced on $VPS_HOST"
echo ""
echo "Next steps:"
echo "  1. Update publishing clients with new OLCRTC_OAUTH_TOKEN"
echo "  2. Revoke old token from Yandex ID"
