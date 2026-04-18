# Multi-Tenant v1 — Unified Architecture and Delivery Spec

## Status
This document supersedes earlier fragmented notes and is intended as the single
implementation spec for Phase 1 multi-tenant architecture.

The goal is to make the first version shippable in one focused pass.

---

## Hard boundary / disclaimer

This spec MUST NOT be interpreted as transport work.

The following areas are explicitly out of scope and must remain untouched in this phase:

- `peer` transport behavior
- `mux` behavior or frame format
- sleep/poll orchestration cleanup
- VPN transport internals
- SOCKS transport internals
- room media/data tunnel semantics
- reconnect semantics inside transport runtime

This spec is strictly about:
- tenant bootstrap
- tenant registry
- tenant runtime isolation
- control-plane API
- OAuth attachment
- fallback namespace model
- process supervision
- client onboarding/configuration

If an implementation idea requires changing transport-layer code, it is outside this phase and must be rejected or deferred.

---

# 1. Purpose

Build a Phase 1 multi-tenant architecture where:

- the server is deployed without tenant-specific secrets or OAuth tokens
- each tenant is provisioned after deployment
- one tenant corresponds to one secret
- OAuth token is optional and only enables Yandex Disk fallback
- the client always knows where to register / bootstrap the tenant
- each tenant gets a dedicated runtime process
- each tenant gets a server-assigned runtime port
- the bootstrap plane and runtime plane are explicitly separated

This spec is for a **small-scale Phase 1 deployment** (roughly up to 10 tenants).

---

# 2. Core product truth

## Canonical truth
1. a tenant is provisioned from the client
2. tenant registration always goes through a shared bootstrap API
3. the tenant secret is required for provisioning and signing room intents
4. OAuth token is optional and only enables fallback through Yandex Disk
5. each tenant gets its own runtime process
6. each tenant gets its own runtime port
7. room creation happens on the client
8. the client sends a signed room intent
9. the tenant runtime joins the room
10. Yandex Disk fallback is a delivery fallback, not the primary provisioning mechanism

Anything contradicting this flow should be treated as legacy or removed.

---

# 3. Identity model

## 3.1 Tenant model
Phase 1 uses the following identity rule:

- **one tenant = one secret**
- if the secret changes, that is a **new tenant**
- this is **not** treated as secret rotation

## 3.2 Tenant ID
The server returns a `tenant_id` after provisioning.

### Important
`tenant_id` is:
- a server-side handle
- an opaque identifier
- not the primary credential

The primary credential remains:
- the tenant secret

## 3.3 Client-side truth
The client may store:
- `tenant_id`
- assigned runtime port
- assigned runtime endpoint
- optional OAuth token
- bootstrap endpoint

But the trust root is still:
- the tenant secret

---

# 4. Credential model

## Required on the client
- `Server Endpoint`
- `Secret`

## Optional on the client
- `OAuth Token`

## Required on the server at deploy time
- none of the tenant secrets
- none of the tenant OAuth tokens

## Result
The server can be deployed generically, and tenants are onboarded later.

---

# 5. Bootstrap vs runtime separation

This separation is mandatory.

## 5.1 Shared bootstrap plane
There is one shared bootstrap/control endpoint.

It is used for:
- tenant registration
- uniqueness check
- OAuth attach/update
- retrieving tenant runtime info
- optional tenant health/status operations

## 5.2 Dedicated runtime plane
Each tenant gets:
- its own process
- its own runtime state
- its own runtime port
- its own optional Yandex Disk watcher
- its own active room/session state

### Important
There is **no shared runtime endpoint in Phase 1**.

There is only:
- a shared bootstrap endpoint
- tenant-specific runtime processes/ports

---

# 6. Port policy

## 6.1 Assignment
The runtime port is assigned dynamically by the server during tenant provisioning.

## 6.2 Stability rule
The port should be:

- dynamically assigned once
- then **stable per tenant**

It should NOT change on every restart.

### Why
Changing the port repeatedly creates:
- client-side churn
- worse support/debugging
- worse handoff
- unnecessary operational complexity

## 6.3 Client behavior
The assigned port must be returned to the client and stored locally.
It may also be shown in the UI when the tunnel/runtime is installed.

---

# 7. Tenant registry

The server must maintain a tenant registry.

## Required fields
- `tenant_id`
- secret fingerprint / uniqueness index
- encrypted tenant secret or equivalent verification material
- assigned runtime port
- optional OAuth token
- fallback enabled flag
- runtime status
- creation/update timestamps

## Optional fields
- display name
- notes / metadata
- last active time
- current process metadata
- current room/session metadata

---

# 8. Secret handling

## 8.1 Secret uniqueness
The server must check that the secret is unique at provisioning time.

### Recommended approach
Use a uniqueness fingerprint / hash derived from the secret.

## 8.2 Storage
If the architecture remains symmetric-signature-based, the server must store
enough tenant secret material to verify signed room intents.

### Phase 1 acceptable model
- encrypted-at-rest tenant secret storage is acceptable
- high-end HSM/KMS is NOT required for Phase 1

## 8.3 No rotation in Phase 1
There is no “change secret” flow in Phase 1.

If the secret changes:
- create a new tenant
- obtain a new `tenant_id`
- obtain a new runtime port
- reattach OAuth if needed

This is explicitly called:
- **re-registration**
- not secret rotation

---

# 9. OAuth model

## 9.1 Optional capability
OAuth is optional.

A tenant may exist without OAuth.

## 9.2 What OAuth enables
OAuth enables:
- Yandex Disk fallback delivery
- tenant-specific Disk watcher path

## 9.3 What OAuth does NOT do
OAuth does NOT define tenant identity.
OAuth does NOT replace the secret.
OAuth is not required for bootstrap registration.

## 9.4 Attach flow
The client may later:
- log into Yandex
- obtain token
- send token to bootstrap API
- enable fallback for that tenant

---

# 10. Yandex Disk fallback model

## 10.1 Role of fallback
Yandex Disk is a fallback path for room/session delivery.

It is NOT the primary provisioning channel.

## 10.2 Tenant isolation
Different tenants must not share the same active-room path.

Each tenant must have a tenant-specific namespace/path.

### Conceptual example
- `app:/olcrtc/tenants/<tenant_id>/active-room.json`

## 10.3 Watcher model
Each tenant runtime process may run its own Yandex Disk watcher if:
- OAuth token is configured
- fallback is enabled for this tenant

---

# 11. Bootstrap API

The shared bootstrap API is mandatory.

## 11.1 `POST /tenant/register`
Registers a new tenant.

### Request
- secret (or proof based on secret)
- optional metadata

### Server actions
- validate request
- check secret uniqueness
- create `tenant_id`
- assign stable runtime port
- create tenant runtime record
- return runtime info

### Response
- `tenant_id`
- runtime port
- runtime endpoint (if applicable)
- capabilities
- fallback enabled = false initially (unless token already attached)

## 11.2 `POST /tenant/oauth`
Attaches or updates tenant OAuth token.

### Request
- tenant identifier
- tenant secret or equivalent authorization
- OAuth token

### Server actions
- authenticate tenant
- store OAuth token
- enable fallback for tenant

### Response
- success/failure
- fallback enabled state

## 11.3 `GET /tenant/config`
Returns current tenant runtime information.

### Response
- `tenant_id`
- assigned runtime port
- runtime endpoint
- fallback enabled
- tenant status

## 11.4 Optional operational endpoints
May be added if needed:
- `GET /tenant/status`
- `POST /tenant/restart`
- `POST /tenant/disable-fallback`

These are optional for Phase 1.

---

# 12. Tenant runtime process

Each tenant runtime process owns:

- tenant secret context
- optional OAuth token / Disk watcher
- active room/session state
- runtime port
- local control/status state

## Responsibilities
- accept signed room intents for this tenant
- join room on demand
- handle tenant-specific fallback watcher
- expose runtime state if needed
- keep process isolated from other tenants

---

# 13. Client flow

## 13.1 First install / first setup
User installs APK.

Client requires:
- `Server Endpoint`
- `Secret`

Client then:
1. calls bootstrap API
2. receives `tenant_id`
3. receives assigned runtime port
4. stores returned runtime info

## 13.2 Optional OAuth enablement
User may then:
1. log into Yandex
2. obtain OAuth token
3. attach it to the tenant through bootstrap API

## 13.3 Session flow
For room/session start:
1. client creates room
2. client signs room intent with secret
3. direct runtime/API delivery is attempted
4. if direct delivery unavailable and OAuth fallback exists:
   - publish to tenant-specific Disk path
5. tenant runtime joins room

---

# 14. Transport/control-plane truth

## Required
Provisioning and tenant management belong to the bootstrap plane.

## Required
Tunnel/data-plane must not be used for initial tenant provisioning.

### Explicit rule
Do NOT build tenant registration around:
- already-established tunnel
- Disk-only bootstrap
- ad hoc runtime channel bootstrapping

Bootstrap happens through the shared bootstrap API.

---

# 15. Process supervisor requirements

Because Phase 1 uses process-per-tenant, a supervisor is required.

## Supervisor responsibilities
- start tenant runtime process
- restart tenant process if needed
- preserve stable port assignment
- maintain tenant->process mapping
- track tenant health
- avoid duplicate processes for same tenant

## Acceptable Phase 1 implementation
A lightweight supervisor is acceptable.
It does not need to be a large orchestration system.

---

# 16. UI / UX implications

## Required settings on client
- `Server Endpoint` (required)
- `Secret` (required)
- `OAuth Token` (optional)

## Required UI truth
Client must clearly indicate:
- tenant registered / not registered
- runtime port assigned
- fallback enabled / disabled
- current runtime availability

## Secret-change behavior
If user changes the secret:
- do NOT present it as secret rotation
- present it as:
  - new registration
  - re-register
  - new account / new tenant

---

# 17. Checks and validation

## Check 1 — bootstrap endpoint required
If `Server Endpoint` is missing:
- tenant registration must not proceed
- clear error must be shown

## Check 2 — secret uniqueness
If the secret is already used by another tenant:
- registration must fail
- clear uniqueness error must be returned

## Check 3 — stable port assignment
After registration:
- a runtime port is assigned
- the same tenant must continue to use the same port across restarts in normal cases

## Check 4 — OAuth optionality
A tenant without OAuth must still:
- register successfully
- operate without fallback

## Check 5 — fallback enablement
After attaching OAuth:
- fallback capability becomes active for that tenant
- tenant-specific watcher path becomes valid

## Check 6 — tenant isolation
Two tenants must not:
- share the same active-room path
- share the same runtime process
- share the same runtime port

## Check 7 — re-registration semantics
If the secret changes:
- a new tenant is created
- a new `tenant_id` is returned
- a new runtime port may be assigned
- old tenant remains separate

## Check 8 — bootstrap/runtime split
Tenant provisioning must still work even when:
- no tunnel is active
- no Yandex Disk fallback is configured

---

# 18. Acceptance criteria

Phase 1 multi-tenant architecture is accepted when:

1. server is deployable without tenant secrets/tokens
2. tenant registration works via shared bootstrap API
3. secret uniqueness is enforced
4. each tenant gets a dedicated runtime process
5. each tenant gets a stable server-assigned runtime port
6. runtime info is returned to the client
7. OAuth attach is optional and works independently
8. fallback uses tenant-specific Disk namespace
9. client can operate with:
   - required `Server Endpoint`
   - required `Secret`
   - optional `OAuth`
10. secret change is handled as new tenant registration, not rotation

---

# 19. Non-goals

Phase 1 does NOT include:
- secret rotation within the same tenant
- shared runtime endpoint for all tenants
- large-scale orchestration
- KMS/HSM complexity
- sophisticated multi-session scheduling
- any transport refactor
- any `peer` refactor
- any `mux` refactor
- any sleep/poll cleanup
- any VPN transport rework
- any SOCKS transport rework
- Windows-specific runtime completion work

If a proposed implementation touches transport-layer semantics, it is outside scope.

---

# 20. Recommended implementation order

1. bootstrap API
2. tenant registry
3. stable port allocation
4. process supervisor
5. runtime process per tenant
6. OAuth attach flow
7. tenant-specific Disk watcher
8. client UX for registration + runtime info
9. client room/session flow on top of registered tenant

---

# 21. Final summary

Phase 1 should be:

- shared bootstrap API
- required `Server Endpoint`
- required `Secret`
- optional `OAuth`
- tenant defined by secret
- `tenant_id` returned as opaque handle
- one tenant = one dedicated runtime process
- one tenant = one stable server-assigned runtime port
- Disk fallback only after provisioning

This is intentionally simple, operationally understandable, and appropriate for a small-scale first deployment.
