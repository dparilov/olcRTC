# Windows Full-Tunnel Client Plan v0.1

## Goal
Build a second Windows client mode that routes system traffic through a virtual tunnel interface instead of exposing only a local SOCKS5 endpoint.

This mode is a separate engineering track from the existing SOCKS-based Windows client and must not replace it during early development.

## Why separate from SOCKS mode
The SOCKS client remains valuable for:
- transport validation
- diagnostics
- fallback operation
- controlled debugging without system routing side effects

The full-tunnel mode adds OS-level networking concerns that should be isolated:
- virtual adapter lifecycle
- route management
- DNS handling
- privileged operations
- cleanup and rollback on failure

## MVP target
A first proof-of-life milestone should demonstrate:
1. creating / attaching a Windows virtual tunnel adapter
2. assigning interface addresses
3. applying and removing controlled routes
4. surfacing adapter / route status in the Windows UI
5. clean rollback on stop or failure

At this stage, packet transport through olcRTC may still be mocked or stubbed if needed.

## Proposed phases

### Phase 1 — Design checkpoint
- document architecture and separation from SOCKS mode
- identify adapter technology and privilege model
- identify server-side implications for packet egress

### Phase 2 — Windows adapter prototype
- evaluate Wintun as preferred adapter technology
- add code to create/open adapter and bring it up
- expose adapter status in UI/logs

### Phase 3 — Route control prototype
- add default-route and split-route application primitives
- add rollback/cleanup on stop and error paths
- log exact route mutations for debugging

### Phase 4 — Packet path prototype
- read packets from the tunnel interface
- validate lifecycle and buffering behavior
- define packet framing strategy for olcRTC transport

### Phase 5 — olcRTC integration
- transport IP packets over the Telemost channel
- add server-side packet egress strategy
- define NAT / next-hop / VPN chaining behavior

### Phase 6 — Controlled validation
- one-room clean tests mirroring the SOCKS validation protocol
- tunnel-up confirmation
- route verification
- readable validation report

## Technical assumptions
- Windows full-tunnel mode will likely require admin rights
- Wintun is the preferred first adapter target unless a better Windows-native option emerges
- DNS handling must be treated as first-class work, not an afterthought
- server-side egress may eventually chain into a second VPS / VPN stack

## Near-term implementation plan
1. add this plan and create a dedicated branch
2. research and scaffold Wintun integration points in repo
3. create a small Windows-only route/adapter management package
4. wire UI placeholders for tunnel-mode status
5. keep SOCKS and full-tunnel modes separate until packet transport is proven

## Success criteria for first milestone
The first milestone is successful if we can show, on Windows:
- a virtual adapter can be brought up from the client
- a controlled route change is applied
- the route change can be rolled back safely
- the UI/logs reflect the adapter and route lifecycle accurately
