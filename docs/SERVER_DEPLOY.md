# Server Deploy and Secret Rotation

## Purpose

This document defines the canonical server-side deployment and maintenance flow for olcRTC in the secure model based on:

- `OLCRTC_MASTER_SECRET`
- `OLCRTC_OAUTH_TOKEN`
- optional `OLCRTC_PREVIOUS_SECRET`

Raw room keys are not part of the canonical deployment model.

---

## Required environment variables

### Required
- `OLCRTC_MASTER_SECRET`
- `OLCRTC_OAUTH_TOKEN`

### Optional
- `OLCRTC_PREVIOUS_SECRET`

---

## Initial deployment

### 1. Prepare secrets
Create a root-only secrets file, for example:

```bash
OLCRTC_MASTER_SECRET=...
OLCRTC_OAUTH_TOKEN=...
```

Permissions must be:

```bash
chmod 600 /path/to/olcrtc.secrets
```

### 2. Validate configuration

Run:

```bash
set -a
source /path/to/olcrtc.secrets
set +a

./olcrtc -mode check
```

Expected result:
- master secret loaded
- OAuth token loaded
- Yandex Disk read access confirmed
- sign/verify cycle working

### 3. Start server in watch/discovery mode

Example:

```bash
set -a
source /path/to/olcrtc.secrets
set +a

./olcrtc -mode srv -discover -dns 1.1.1.1:53
```

### 4. Expected runtime behavior
- server polls Yandex Disk
- server verifies signed room records
- server derives room key from `Master Secret + roomID`
- server connects to the published room
- server reconnects on new room publication

---

## Master Secret rotation

### Goal
Replace the active setup secret without immediate service breakage.

### 1. Prepare rotation window
Update secrets file:

```bash
OLCRTC_MASTER_SECRET=<new secret>
OLCRTC_PREVIOUS_SECRET=<old secret>
OLCRTC_OAUTH_TOKEN=...
```

### 2. Validate rotation
Run:

```bash
set -a
source /path/to/olcrtc.secrets
set +a

./olcrtc -mode rotate-secret
```

Expected result:
- new secret loaded
- previous secret loaded
- sign/verify with new secret works
- fallback verification with previous secret works

### 3. Restart server
Restart server with both secrets present.

### 4. Update all clients
All publishing and consuming clients must switch to the new `Master Secret`.

### 5. Close rotation window
After all clients are migrated:

- remove `OLCRTC_PREVIOUS_SECRET`
- restart server
- verify server works only with current secret

---

## OAuth token replacement

### Goal
Replace Disk access credential without breaking room discovery.

### 1. Update secrets file
Replace:

```bash
OLCRTC_OAUTH_TOKEN=<new token>
```

### 2. Validate token
Run:

```bash
set -a
source /path/to/olcrtc.secrets
set +a

./olcrtc -mode rotate-token
```

Expected result:
- token loaded
- Yandex Disk read access confirmed
- current room record readable

### 3. Restart server
Restart with the new token.

### 4. Revoke old token
After successful validation, revoke the old token in Yandex.

---

## Operational rules

### MUST
- use `OLCRTC_MASTER_SECRET` as the canonical secret source
- use `OLCRTC_OAUTH_TOKEN` as the canonical Disk credential
- keep secrets outside the repository
- keep secrets in root-only storage
- use `-mode check` before production restart
- use `-mode rotate-secret` before master secret switch
- use `-mode rotate-token` before token switch

### MUST NOT
- use raw `-key` as a production deployment model
- store secrets in repository files
- store secrets in public scripts
- log secret values

---

## Failure handling

### If `-mode check` fails
Do not start or restart production server.

### If `-mode rotate-secret` fails
Do not enter rotation window.

### If `-mode rotate-token` fails
Do not switch token.

### If server cannot verify room record
Server must reject the record and continue polling.

---

## Acceptance checklist

- [ ] secrets are stored outside repo
- [ ] secrets file has `0600`
- [ ] `-mode check` passes
- [ ] `-mode srv -discover` starts successfully
- [ ] server reads signed room record
- [ ] server derives room key locally
- [ ] rotation procedure documented
- [ ] token replacement procedure documented
