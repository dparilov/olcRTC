# Security Model

## Secret Handling

OlcRTC uses three secrets:

| Secret | Purpose | Env Variable |
|--------|---------|-------------|
| Master Secret | HMAC key derivation per room | `OLCRTC_MASTER_SECRET` |
| OAuth Token | Yandex Disk / Telemost API access | `OLCRTC_OAUTH_TOKEN` |
| Encryption Key | Direct tunnel key (if no master secret) | `OLCRTC_KEY` |

### How secrets are provided

1. **Environment variables** (preferred): Set `OLCRTC_MASTER_SECRET`, `OLCRTC_OAUTH_TOKEN`, `OLCRTC_KEY` before launching.
2. **GUI input**: Desktop and Android apps accept secrets in settings UI, store them in memory only.
3. **CLI flags** (deprecated): `--master-secret` and `--oauth-token` still work but env vars take priority. Avoid flags — they leak into process lists and shell history.

### What is NOT done

- Secrets are **never** stored in `config.json` (fields tagged `json:"-"`).
- Secrets are **never** passed as command-line arguments to subprocesses.
- Secrets are **never** returned by the `/api/room` HTTP endpoint.
- Log output **redacts** secret values.

### Legacy config migration

If an old `config.json` contains `oauth_token`, `master_secret`, or `encryption_key`,
the app reads them into memory on startup, then rewrites the file without those fields.

## Key Derivation

```
key = HMAC-SHA256(master_secret, room_id)
```

Both client and server compute the same 256-bit key from the shared master secret
and the room ID. No key exchange over the network. The room ID (from Yandex Disk
rendezvous) serves as the public nonce.

## Fail-Closed Behavior

If no key or master secret is available at startup:
- **Windows client**: refuses to launch tunnel, shows error.
- **Android client**: refuses to start, shows "No encryption key" message.
- **CLI**: proceeds only if env var or flag provides a key.

## Network Exposure

- `/api/room` endpoint binds to `127.0.0.1` only (loopback).
- No wildcard CORS headers.
- Room metadata (ID, URL, expiry) is returned; key material is never returned.

## Reporting Vulnerabilities

Open a GitHub issue or contact the maintainers directly.