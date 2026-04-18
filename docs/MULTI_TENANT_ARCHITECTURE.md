# Multi-Tenant Server Architecture

## Status: Design Document (not yet implemented)

## Vision

The server deploys **without any pre-configured secrets or tokens**. Each client brings their own credentials, and the server dynamically creates isolated tunnel "accounts" per client.

---

## Current State (single-tenant)

```
Server: 1 master_secret + 1 oauth_token + 1 socks_port (1080)
Client: knows master_secret + server_endpoint
Result: 1 tunnel, 1 room, 1 SOCKS proxy
```

## Target State (multi-tenant)

```
Server: starts empty, no secrets, no tokens
Client A: registers with secret_A + optional oauth_A → gets port 1080
Client B: registers with secret_B + optional oauth_B → gets port 1081
Client C: registers with secret_C (no oauth) → gets port 1082, no fallback
```

---

## Core Concept: Account = Master Secret

An "account" is identified by the **hash of the master secret**:

```
account_id = SHA256(master_secret)[:16]  // 16-byte hex identifier
```

The raw master secret is stored on the server (needed for signature verification), but the account_id is used as a safe, loggable identifier.

### Account Properties
- `account_id`: derived from master secret (unique, deterministic)
- `master_secret`: stored securely (needed for VerifyRecord)
- `oauth_token`: optional (enables Disk fallback for this account)
- `socks_port`: server-assigned from a managed port range
- `status`: active / idle / disabled
- `created_at`: registration timestamp

---

## Registration Flow

### Step 1 — Client registers

```
POST /api/register
{
  "master_secret": "client-secret-here",
  "oauth_token": "y0__optional..."   // optional, for Disk fallback
}
```

### Step 2 — Server processes

1. Compute `account_id = SHA256(master_secret)[:16]`
2. Check uniqueness — if account_id exists, return existing assignment
3. Allocate next available SOCKS port from range (e.g., 1080-1180)
4. Store account: `{account_id, master_secret, oauth_token, socks_port}`
5. If OAuth provided, start Disk watcher for this account's path
6. Return assigned port

### Step 3 — Server responds

```json
{
  "status": "registered",
  "account_id": "a1b2c3d4e5f6g7h8",
  "socks_port": 1081,
  "api_port": 8080,
  "disk_fallback": true
}
```

### Step 4 — Client stores port

Android stores `socks_port` from registration response. All subsequent operations use this port.

---

## Room Intent Flow (per-account)

### Direct API path (unchanged semantics)

```
POST /api/room-intent
{
  "room_id": "...",
  "room_url": "...",
  "sig": "...",              // signed with client's master_secret
  "record_id": "...",
  ...
}
```

Server:
1. Try all registered accounts' secrets to verify signature
2. Identify which account signed this intent
3. Start/switch tunnel on that account's SOCKS port
4. Return `{status: "accepted", socks_port: 1081}`

### Disk fallback path (per-account)

Each account with OAuth gets its own Disk path:

```
app:/olcrtc/accounts/{account_id}/active-room.json
```

Server watches all registered accounts' Disk paths. When a room appears:
1. Read and verify with the account's stored secret
2. Start tunnel on the account's assigned port

---

## Port Management

### Port range
- Default range: 1080-1180 (100 ports)
- Configurable via `--port-range-start 1080 --port-range-end 1180`

### Allocation
- Sequential: first available port in range
- Persistent: port assignment survives server restart (stored in state file)
- Released: when account is removed or expired

### State file
```json
// /opt/olcrtc/accounts.json
{
  "accounts": [
    {
      "account_id": "a1b2c3d4e5f6g7h8",
      "socks_port": 1080,
      "has_oauth": true,
      "created_at": "2026-04-18T10:00:00Z"
    },
    {
      "account_id": "x9y8z7w6v5u4t3s2",
      "socks_port": 1081,
      "has_oauth": false,
      "created_at": "2026-04-18T11:00:00Z"
    }
  ]
}
```

Note: secrets and tokens are NOT in the state file. They are stored in a separate encrypted store or environment.

---

## Security Considerations

### Secret storage on server
The server must store client master secrets to verify signatures. Options:
- **File-based**: encrypted secrets file (DPAPI on Windows, file permissions on Linux)
- **Memory-only**: secrets stored in RAM, lost on restart (clients re-register)
- **Hybrid**: secrets in memory, account_id+port in state file (clients re-register secrets after restart)

**Recommendation for Phase 0**: Memory-only. Clients re-register when server restarts. State file only stores account_id + port (no secrets).

### Secret uniqueness
Two clients cannot register the same master secret. `account_id = SHA256(secret)` ensures deterministic dedup.

### OAuth token isolation
Each OAuth token accesses only its own Yandex Disk. No cross-account data leakage.

### Port isolation
Each account's SOCKS proxy is an independent tunnel. No traffic mixing between accounts.

---

## Client UX Changes

### Registration (new step)
On first use with a new server:
1. Client sends `POST /api/register` with master secret (+ optional OAuth)
2. Server responds with assigned `socks_port`
3. Client stores the port locally

### Subsequent use
- `Create & Start` sends room intent as before
- Server identifies account from signature, uses correct port
- Client uses stored port for SOCKS proxy

### Settings
```
Required:
  - Master Secret
  - Server Endpoint

Optional:
  - OAuth Token (enables Disk fallback)

Auto-assigned:
  - SOCKS Port (from server)
  - Account ID (derived from secret)
```

---

## Migration Path

### Phase 0 (current)
- Single secret, single OAuth, single port
- Server-assigned port via IntentAPI ✅ (just implemented)

### Phase 1 (multi-tenant MVP)
- Add `/api/register` endpoint
- Account storage (in-memory + state file)
- Port range allocation
- Per-account Disk watcher (for accounts with OAuth)
- Per-account tunnel process

### Phase 2 (hardening)
- Account expiry / TTL
- Admin API for account management
- Persistent secret storage (encrypted)
- Health monitoring per account
- Rate limiting per account

---

## Open Questions

### Q1: Should the server start tunnels eagerly or lazily?
- **Eager**: start tunnel process when account registers (uses resources)
- **Lazy**: start tunnel only when room intent arrives (saves resources)
- **Recommendation**: Lazy. Start tunnel on first intent, stop after idle timeout.

### Q2: How many concurrent accounts per VPS?
- Each tunnel: ~25MB RAM, ~5% CPU idle
- 1GB VPS: ~30 accounts (with lazy start: many more registered, few active)
- 4GB VPS: ~100+ accounts

### Q3: What happens when a client re-registers?
- Same secret = same account_id = same port (idempotent)
- Updated OAuth token replaces the old one
- No disruption to active tunnel

### Q4: Should the client send the raw secret in registration?
- **Option A**: Send raw secret (simple, server stores it)
- **Option B**: Use a derived registration token (more secure, but server still needs secret for HMAC verify)
- **Conclusion**: Option A is necessary because server needs raw secret for `VerifyRecord()`. TLS protects the transport.

### Q5: Per-account Disk path — who creates the directory?
- Server creates `app:/olcrtc/accounts/{account_id}/` on registration
- Client's fallback publishes to this path
- Client needs to know the path → server returns it in registration response

---

## Summary

The multi-tenant architecture enables:
- **Zero-config server deployment** (no pre-shared secrets)
- **Self-service client onboarding** (register → get port → start using)
- **Isolated tunnels per client** (no traffic mixing)
- **Optional Disk fallback per client** (bring your own OAuth)
- **Scalable to ~30-100 clients per VPS** (depending on resources)

The key insight: **Master Secret IS the account identity**. No separate user/password system needed. The same secret that signs room intents also identifies the client account on the server.
