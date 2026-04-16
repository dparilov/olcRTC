# Operations Guide

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| OLCRTC_MASTER_SECRET | Yes | Shared cryptographic secret for the setup |
| OLCRTC_OAUTH_TOKEN | Yes (publish/discover) | Yandex Disk OAuth token |
| OLCRTC_PREVIOUS_SECRET | No | Previous master secret during rotation window |
| OLCRTC_KEY | No | Direct room key (hex), bypasses DeriveKey |
| OLCRTC_EXPECTED_IP | No | Expected exit IP for E2E report validation |

## Modes

| Mode | Description |
|---|---|
| `srv` | Server mode (with `--discover` for Yandex Disk polling) |
| `cnc` | Client mode (with `--discover` for Yandex Disk room lookup) |
| `check` | Validate secrets, Disk access, and sign/verify capability |
| `rotate-secret` | Validate master secret rotation (new + previous) |
| `rotate-token` | Validate new OAuth token (test Disk read access) |

## Automated Deploy Scripts

All server operations are automated via scripts in `script/`. The operator does not need to SSH into the VPS manually. Scripts receive secrets via environment variables — secrets never appear in argv, local files, or script source.

### Fresh Deploy or Upgrade

```bash
OLCRTC_MASTER_SECRET=<secret> \
OLCRTC_OAUTH_TOKEN=<token> \
./script/deploy-server.sh <VPS_HOST>
```

What it does:
1. Builds linux/amd64 binary (or uses provided path)
2. Uploads to VPS via SCP
3. Writes secrets file (0600, root-only)
4. Runs `-mode check` (health check)
5. Starts server in discover mode
6. Verifies process is running

### Rotate Master Secret

```bash
OLCRTC_MASTER_SECRET=<new> \
OLCRTC_PREVIOUS_SECRET=<old> \
OLCRTC_OAUTH_TOKEN=<token> \
./script/rotate-secret-server.sh <VPS_HOST>
```

What it does:
1. Local validation: `rotate-secret` mode checks both secrets
2. Updates secrets file on VPS with rotation window
3. Restarts server — accepts records signed with current OR previous
4. Verifies server is running

After all clients migrated to new secret:
```bash
OLCRTC_MASTER_SECRET=<new> \
OLCRTC_OAUTH_TOKEN=<token> \
./script/deploy-server.sh <VPS_HOST>
```
This closes the rotation window (no OLCRTC_PREVIOUS_SECRET).

### Replace OAuth Token

```bash
OLCRTC_MASTER_SECRET=<secret> \
OLCRTC_OAUTH_TOKEN=<new-token> \
./script/rotate-token-server.sh <VPS_HOST>
```

What it does:
1. Local validation: `rotate-token` mode tests Disk access
2. Updates secrets file on VPS
3. Restarts server with new token
4. Verifies server is running

After confirming: revoke old token from Yandex ID.

## Manual Operations (Alternative)

### Health Check

```bash
OLCRTC_MASTER_SECRET=... OLCRTC_OAUTH_TOKEN=... ./olcrtc -mode check
```

### Client Setup

1. Export OLCRTC_MASTER_SECRET (required) and OLCRTC_OAUTH_TOKEN (for publishing clients)
2. Run health check: `./olcrtc -mode check`
3. Run with room ID: `./olcrtc -mode cnc -id ROOM_ID -socks-port 18090`
4. Or discover mode: `./olcrtc -mode cnc --discover -socks-port 18090`

## Room Record Format (v2)

Fields: room_id, room_url, created_at, expires_at, version(2), key_version, record_id, sig

- sig = HMAC-SHA256(master_secret, canonical_json_without_sig)
- key_version tracks which secret signed it
- record_id = random nonce for replay prevention
- Legacy v1 unsigned records are rejected by server

## Security Checklist

- Secrets file chmod 600 (root-only on server)
- No secrets in ps aux (env vars, not argv)
- No secrets in logs (redacted)
- No secrets in config.json (json:"-" tags)
- No secrets in repo, docs, or build artifacts
- Room records signed (version=2, HMAC-SHA256)
- Server verifies signatures before connecting
- CLI flags removed, env-only secret loading
- Health check (`-mode check`) validates without exposing secrets
- Windows: DPAPI-encrypted secret storage
- Android: EncryptedSharedPreferences (AES256-GCM, Keystore-backed)
- Deploy scripts: secrets via env, never in argv or source
