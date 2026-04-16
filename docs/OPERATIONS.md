# Operations Guide

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| OLCRTC_MASTER_SECRET | Yes | Shared cryptographic secret for the setup |
| OLCRTC_OAUTH_TOKEN | Yes (publish/discover) | Yandex Disk OAuth token |
| OLCRTC_PREVIOUS_SECRET | No | Previous master secret during rotation window |
| OLCRTC_KEY | No | Direct room key (hex), bypasses DeriveKey |

## Server Initial Deploy

1. Copy binary to server and make executable
2. Create secrets file (root-only, 0600) with OLCRTC_MASTER_SECRET and OLCRTC_OAUTH_TOKEN
3. Start server: source secrets, run with -mode srv --discover -debug
4. Verify: log shows WATCH-SRV polling, no secrets in ps aux

## Client Setup

1. Export OLCRTC_MASTER_SECRET (required) and OLCRTC_OAUTH_TOKEN (for publishing clients)
2. Run with room ID: ./olcrtc -mode cnc -id ROOM_ID -socks-port 18090
3. Or discover mode: ./olcrtc -mode cnc --discover -socks-port 18090

## Rotate Master Secret

1. Update all clients with new OLCRTC_MASTER_SECRET
2. On server: set OLCRTC_MASTER_SECRET=new, OLCRTC_PREVIOUS_SECRET=old, restart
3. Server accepts records signed with either secret during rotation window
4. After all clients migrated: remove OLCRTC_PREVIOUS_SECRET, restart

## Replace OAuth Token

1. Get new token from Yandex ID
2. Test read access to Disk path
3. Update secrets file, restart server/client

## Room Record Format (v2)

Fields: room_id, room_url, created_at, expires_at, version(2), key_version, record_id, sig

- sig = HMAC-SHA256(master_secret, canonical_json_without_sig)
- key_version tracks which secret signed it
- record_id = random nonce for replay prevention
- Legacy v1 unsigned records are rejected

## Security Checklist

- Secrets file chmod 600
- No secrets in ps aux
- No secrets in logs
- No secrets in config.json or repo
- Room records signed (version=2)
- Server verifies signatures before connecting
- CLI flags removed, env-only secret loading
