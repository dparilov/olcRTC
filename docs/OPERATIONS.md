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

## Server Initial Deploy

1. Copy binary to server and make executable
2. Create secrets file (root-only, 0600) with OLCRTC_MASTER_SECRET and OLCRTC_OAUTH_TOKEN
3. Run health check: `source secrets && ./olcrtc -mode check`
4. Start server: `./olcrtc -mode srv --discover -debug`
5. Verify: log shows WATCH-SRV polling, no secrets in ps aux

## Client Setup

1. Export OLCRTC_MASTER_SECRET (required) and OLCRTC_OAUTH_TOKEN (for publishing clients)
2. Run health check: `./olcrtc -mode check`
3. Run with room ID: `./olcrtc -mode cnc -id ROOM_ID -socks-port 18090`
4. Or discover mode: `./olcrtc -mode cnc --discover -socks-port 18090`

## Health Check

Validates all secrets and connectivity without starting a tunnel:

```bash
OLCRTC_MASTER_SECRET=... OLCRTC_OAUTH_TOKEN=... ./olcrtc -mode check
```

Output confirms: secret loaded, Disk access, key derivation, sign/verify cycle.

## Rotate Master Secret

### Validation (before applying)

```bash
OLCRTC_MASTER_SECRET=<new> OLCRTC_PREVIOUS_SECRET=<old> ./olcrtc -mode rotate-secret
```

Validates: both secrets loaded, key derivation differs, sign/verify with both, multi-verify works.

### Procedure

1. Generate new secret
2. Validate: `OLCRTC_MASTER_SECRET=new OLCRTC_PREVIOUS_SECRET=old ./olcrtc -mode rotate-secret`
3. Update all clients with new OLCRTC_MASTER_SECRET
4. On server: set OLCRTC_MASTER_SECRET=new, OLCRTC_PREVIOUS_SECRET=old, restart
5. Server accepts records signed with either secret during rotation window
6. After all clients migrated: remove OLCRTC_PREVIOUS_SECRET, restart
7. Verify: `./olcrtc -mode check`

## Replace OAuth Token

### Validation (before applying)

```bash
OLCRTC_MASTER_SECRET=... OLCRTC_OAUTH_TOKEN=<new-token> ./olcrtc -mode rotate-token
```

Validates: token loaded, Disk read access confirmed, published record signature (if available).

### Procedure

1. Get new token from Yandex ID
2. Validate: `OLCRTC_OAUTH_TOKEN=<new> ./olcrtc -mode rotate-token`
3. Update secrets file with new token
4. Restart server/client
5. Revoke old token from Yandex ID

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
- Health check (`-mode check`) validates configuration without exposing secrets
