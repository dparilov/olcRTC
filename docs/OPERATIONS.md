# Operations Guide

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `OLCRTC_MASTER_SECRET` | Yes | Shared cryptographic secret for the setup |
| `OLCRTC_OAUTH_TOKEN` | Yes (publish/discover) | Yandex Disk OAuth token |
| `OLCRTC_PREVIOUS_SECRET` | No | Previous master secret during rotation window |
| `OLCRTC_KEY` | No | Direct room key (hex), bypasses DeriveKey |

## Server Initial Deploy

1. Copy binary to server:
   ```bash
   scp olcrtc root@SERVER:/opt/olcrtc
   chmod +x /opt/olcrtc
   ```

2. Create secrets file (root-only, 0600):
   ```bash
   cat > /opt/olcrtc.env << 'EOF'
   OLCRTC_MASTER_SECRET=your-shared-secret
   OLCRTC_OAUTH_TOKEN=your-yandex-oauth-token
   EOF
   chmod 600 /opt/olcrtc.env
   ```

3. Start server (discover mode watches Yandex Disk):
   ```bash
   set -a; source /opt/olcrtc.env; set +a
   /opt/olcrtc -mode srv --discover -debug
   ```

4. Verify startup:
   - Log shows `[WATCH-SRV] Polling Yandex Disk for room...`
   - No secrets visible in `ps aux`

## Client Setup

1. Set environment variables:
   ```bash
   export OLCRTC_MASTER_SECRET="your-shared-secret"
   export OLCRTC_OAUTH_TOKEN="your-yandex-oauth-token"  # only for publishing clients
   ```

2. Run client with room ID:
   ```bash
   ./olcrtc -mode cnc -id ROOM_ID -socks-port 18090
   ```
   If both env vars are set, the client publishes a signed room record to Yandex Disk.

3. Run client in discover mode (reads room from Disk):
   ```bash
   ./olcrtc -mode cnc --discover -socks-port 18090
   ```

## Rotate Master Secret

### On all clients first:
1. Set new secret:
   ```bash
   export OLCRTC_MASTER_SECRET="new-secret"
   ```
2. New room records will be signed with the new secret.

### On server:
1. Update secrets file with both secrets:
   ```bash
   cat > /opt/olcrtc.env << 'EOF'
   OLCRTC_MASTER_SECRET=new-secret
   OLCRTC_PREVIOUS_SECRET=old-secret
   OLCRTC_OAUTH_TOKEN=your-token
   EOF
   chmod 600 /opt/olcrtc.env
   ```
2. Restart server:
   ```bash
   pkill -f olcrtc; set -a; source /opt/olcrtc.env; set +a
   /opt/olcrtc -mode srv --discover -debug
   ```
3. Server accepts records signed with either secret during rotation window.

### Finalize rotation:
After all clients are using the new secret:
1. Remove previous secret:
   ```bash
   cat > /opt/olcrtc.env << 'EOF'
   OLCRTC_MASTER_SECRET=new-secret
   OLCRTC_OAUTH_TOKEN=your-token
   EOF
   chmod 600 /opt/olcrtc.env
   ```
2. Restart server.

## Replace OAuth Token

### On server:
1. Get new token from Yandex ID.
2. Test read access:
   ```bash
   curl -s -H "Authorization: OAuth NEW_TOKEN" \
     "https://cloud-api.yandex.net/v1/disk/resources?path=app%3A%2Folcrtc"
   ```
3. If successful, update secrets file:
   ```bash
   sed -i 's/OLCRTC_OAUTH_TOKEN=.*/OLCRTC_OAUTH_TOKEN=NEW_TOKEN/' /opt/olcrtc.env
   ```
4. Restart server.

### On publishing client:
1. Set new token:
   ```bash
   export OLCRTC_OAUTH_TOKEN="new-token"
   ```
2. Test publish by running client — check log for `published to Yandex Disk`.

## Room Record Format (v2)

```json
{
  "room_id": "06131212948922",
  "room_url": "https://telemost.yandex.ru/j/06131212948922",
  "created_at": "2026-04-16T12:00:00Z",
  "expires_at": "2026-04-16T15:00:00Z",
  "version": 2,
  "key_version": 1,
  "record_id": "a1b2c3d4e5f6...",
  "sig": "hmac-sha256-hex..."
}
```

- `sig` = HMAC-SHA256(master_secret, canonical_json_without_sig)
- `key_version` 1 = current secret, used for rotation tracking
- `record_id` = random nonce for replay prevention
- Legacy v1 records (unsigned) are rejected

## Security Checklist

- [ ] Secrets file is chmod 600
- [ ] No secrets in `ps aux` output
- [ ] No secrets in log output
- [ ] No secrets in config.json
- [ ] No secrets in repository
- [ ] Room records are signed (version=2, sig present)
- [ ] Server verifies signatures before connecting
