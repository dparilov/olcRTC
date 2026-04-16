#!/bin/bash
# deploy-server.sh — Deploy or upgrade olcRTC server
# Usage: OLCRTC_MASTER_SECRET=... OLCRTC_OAUTH_TOKEN=... ./script/deploy-server.sh <VPS_HOST> [BINARY_PATH]
#
# Prerequisites:
#   - SSH access to VPS_HOST as root
#   - OLCRTC_MASTER_SECRET and OLCRTC_OAUTH_TOKEN set in environment
#   - Binary built for linux/amd64 (or specify path)
set -euo pipefail

VPS_HOST="${1:?Usage: deploy-server.sh <VPS_HOST> [BINARY_PATH]}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# Build binary if not provided
if [ -n "${2:-}" ]; then
    BINARY="$2"
else
    echo "==> Building linux/amd64 binary..."
    BINARY="$PROJECT_ROOT/olcrtc-linux-amd64"
    cd "$PROJECT_ROOT"
    GOOS=linux GOARCH=amd64 go build -o "$BINARY" ./cmd/olcrtc
    echo "    Built: $BINARY ($(stat -c%s "$BINARY" 2>/dev/null || stat -f%z "$BINARY") bytes)"
fi

# Validate secrets
if [ -z "${OLCRTC_MASTER_SECRET:-}" ]; then
    echo "ERROR: OLCRTC_MASTER_SECRET must be set"
    exit 1
fi
if [ -z "${OLCRTC_OAUTH_TOKEN:-}" ]; then
    echo "ERROR: OLCRTC_OAUTH_TOKEN must be set"
    exit 1
fi

echo "==> Uploading binary to $VPS_HOST..."
scp "$BINARY" "root@${VPS_HOST}:/opt/olcrtc-new"

echo "==> Creating secrets file and deploying..."
# Write secrets file via stdin (never in argv or local files)
ssh "root@${VPS_HOST}" bash -s << REMOTE
set -euo pipefail

# Stop old process
pkill -f olcrtc || true
sleep 1

# Install binary
chmod +x /opt/olcrtc-new
mv /opt/olcrtc-new /opt/olcrtc

# Write secrets file (0600, root-only)
cat > /opt/olcrtc.env << 'ENVEOF'
OLCRTC_MASTER_SECRET=${OLCRTC_MASTER_SECRET}
OLCRTC_OAUTH_TOKEN=${OLCRTC_OAUTH_TOKEN}
ENVEOF
chmod 600 /opt/olcrtc.env

# Run health check
export \$(cat /opt/olcrtc.env | xargs)
/opt/olcrtc -mode check

# Start server
nohup bash -c 'export \$(cat /opt/olcrtc.env | xargs) && /opt/olcrtc -mode srv --discover -debug' > /opt/olcrtc.log 2>&1 &
echo "Server started (PID=\$!)"

# Verify running
sleep 2
if pgrep -f "olcrtc.*-mode srv" > /dev/null; then
    echo "==> Deploy successful"
else
    echo "ERROR: Server failed to start. Check /opt/olcrtc.log"
    tail -10 /opt/olcrtc.log
    exit 1
fi
REMOTE

echo "==> Deploy complete. Server running on $VPS_HOST"
