#!/bin/bash
# rotate-secret-server.sh — Rotate master secret on olcRTC server
# Usage: OLCRTC_MASTER_SECRET=<new> OLCRTC_PREVIOUS_SECRET=<old> OLCRTC_OAUTH_TOKEN=... \
#        ./script/rotate-secret-server.sh <VPS_HOST>
#
# This script:
#   1. Validates new + old secrets locally
#   2. Updates secrets file on VPS with rotation window
#   3. Restarts server with both current and previous secrets
#   4. Verifies server is running and accepting records
#
# After all clients have migrated to the new secret, run:
#   OLCRTC_MASTER_SECRET=<new> OLCRTC_OAUTH_TOKEN=... ./script/close-rotation-server.sh <VPS_HOST>
set -euo pipefail

VPS_HOST="${1:?Usage: rotate-secret-server.sh <VPS_HOST>}"

# Validate required env vars
: "${OLCRTC_MASTER_SECRET:?ERROR: OLCRTC_MASTER_SECRET (new secret) must be set}"
: "${OLCRTC_PREVIOUS_SECRET:?ERROR: OLCRTC_PREVIOUS_SECRET (old secret) must be set}"
: "${OLCRTC_OAUTH_TOKEN:?ERROR: OLCRTC_OAUTH_TOKEN must be set}"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
OLCRTC_BIN="$PROJECT_ROOT/olcrtc"

if [ ! -x "$OLCRTC_BIN" ]; then
    echo "==> Building olcrtc for local validation..."
    cd "$PROJECT_ROOT" && go build -o "$OLCRTC_BIN" ./cmd/olcrtc
fi

echo "==> Step 1: Local rotation validation..."
"$OLCRTC_BIN" -mode rotate-secret
echo ""

echo "==> Step 2: Updating secrets on $VPS_HOST (rotation window: current + previous)..."
ssh "root@${VPS_HOST}" bash -s << REMOTE
set -euo pipefail

pkill -f olcrtc || true
sleep 1

# Write secrets with rotation window
cat > /opt/olcrtc.env << 'ENVEOF'
OLCRTC_MASTER_SECRET=${OLCRTC_MASTER_SECRET}
OLCRTC_PREVIOUS_SECRET=${OLCRTC_PREVIOUS_SECRET}
OLCRTC_OAUTH_TOKEN=${OLCRTC_OAUTH_TOKEN}
ENVEOF
chmod 600 /opt/olcrtc.env

# Health check
export \$(cat /opt/olcrtc.env | xargs)
/opt/olcrtc -mode check

# Start server with rotation window
nohup bash -c 'export \$(cat /opt/olcrtc.env | xargs) && /opt/olcrtc -mode srv --discover -debug' > /opt/olcrtc.log 2>&1 &
echo "Server started with rotation window (PID=\$!)"

sleep 2
if pgrep -f "olcrtc.*-mode srv" > /dev/null; then
    echo "==> Rotation deploy successful"
else
    echo "ERROR: Server failed to start"
    tail -10 /opt/olcrtc.log
    exit 1
fi
REMOTE

echo ""
echo "==> Rotation window active on $VPS_HOST"
echo "    Server accepts records signed with current OR previous secret."
echo ""
echo "Next steps:"
echo "  1. Update all clients with new OLCRTC_MASTER_SECRET"
echo "  2. After migration complete, close rotation window:"
echo "     OLCRTC_MASTER_SECRET=<new> OLCRTC_OAUTH_TOKEN=... ./script/deploy-server.sh $VPS_HOST"
