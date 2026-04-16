# Contributing

## Docs Hygiene Checklist

Before merging any PR that touches documentation, verify:

- [ ] **No local paths** — no `/home/<user>/`, `~/tools/`, or absolute paths to local toolchains
- [ ] **No IP addresses** — VPS IPs, internal network addresses belong in private runbooks
- [ ] **No credentials** — no tokens, keys, passwords, or example values that look real
- [ ] **No personal references** — no "Dima's account" or similar; use role-based language ("the operator")
- [ ] **Secrets via env vars** — all examples show `OLCRTC_*` env vars, never `--secret` flags in argv
- [ ] **No operational details** — deployment paths (`/opt/...`), log locations, systemd units go in private ops docs
- [ ] **Neutral placeholders** — use `<token>`, `<your-vps-ip>`, `example.com` instead of real values
- [ ] **File permissions** — config examples show `0600` for sensitive files

## Where to put what

| Content | Location |
|---------|----------|
| Architecture, protocol, data flow | `docs/ARCHITECTURE.md` |
| External deps and versions | `docs/REFERENCES.md` |
| Security model, secret handling | `SECURITY.md` |
| Build instructions | `docs/windows-client-build.md`, `docs/android-client-plan.md` |
| VPS addresses, deploy paths, credentials | **Private runbook (not in repo)** |
| Personal dev environment setup | **Local notes (not in repo)** |
