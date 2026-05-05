# Current State

_Last updated: 2026-05-05 (seeded from session JSONL + AGENT_CONTEXT.md)_

---

## Active Branch (olcRTC target repo)

- Branch: `v2-sso` (ahead 32 of origin/v2-sso, dirty)
- Modified: `go.mod`, `go.sum`, `olcrtc-bootstrap`, `build/android-bind/olcrtc-api21.aar`
- Untracked: `olcrtc-windows`, `.agent/` (entire new directory)

---

## Memory Architecture (decision confirmed 2026-05-05)

- **Canonical:** File-first — `.agent/memory/**` in target repo
- **Optional / not required:** OpenClaw memory-core, SQLite vector index, OpenAI embeddings
- Index built 2026-05-04: topics 7301 (36 windows), 13350 (2 windows), 15222 (8 windows)
- Window files are statistical metadata envelopes only — no embedded text or extracted facts
- Extraction/promotion not yet run — `promoted/` and `wiki/` are empty

---

## Recent Completions

### infra repo (openclaw-agent-memory-infra)

| PR | Title | Status | Commit |
|----|-------|--------|--------|
| PR27 | `feat(pr27): deterministic fast onboarding CLI` — `scripts/onboard-project.py` (720 lines) + 46 tests | ✅ merged | `b28ae13` |
| PR28 | `docs: record OpenClaw 2026.5 runtime chunk incident` | ✅ merged | `5cec1df` |
| PR29 | `docs: clarify onboard CLI MVP status` — `docs/ONBOARD_PROJECT_CLI.md` + `scripts/onboard-project.py` (74 ins) | ✅ merged | `9fe4c4b` |

### olcRTC target repo

| PR | Title | Status | Commit / Notes |
|----|-------|--------|----------------|
| PR3 | `.gitignore` `.agent/memory/` protection | ✅ merged to master | `48d6d69`; line 253 covers all 3 topic dirs |
| PR4 | Tool sync — `initial-index.py` → `.agent/tools/context_access/` | ✅ merged | `f16407c` (Fast-forward, 624 ins) |
| — | Memory index initial build | ✅ completed 2026-05-04 | All 3 topics indexed via `initial-index.py` |
| — | Working memory seed (this session) | ✅ completed 2026-05-05 | current-state.md + known-issues.md seeded from JSONL + AGENT_CONTEXT.md; no vector DB / read-topic required |

---

## In Progress

_(none as of 2026-05-05 — session complete)_

---

## Blockers

- **SendMessage cross-topic blocked** — error `"message required"` for all 5 payload shapes tried from infra topic (15222) → topics 7301 / 13350. Cross-topic agent notification non-functional. _[PENDING: schema/plugin investigation — do not upgrade OpenClaw from 2026.4.23]_
- **working/ and promoted/ empty** — no extracted facts promoted yet; file-first recall limited to static files (AGENT_CONTEXT.md, handoffs, reports) — partially addressed by this seed
- **wiki/ not built** — requires at least one L2 promotion first
- **All topic-7301 windows tier-c** — all 36 windows require @pariloff approval before promotion; VPS credential batches block auto-promotion
- **PR3 / file-first ADR not recorded** — `.agent/decisions/` still contains only templates

---

## OpenClaw Runtime

- Version: **2026.4.23** (a979721) — **pinned, do not upgrade**
- Gateway: loopback `127.0.0.1:18789`, systemd, probing OK
- Telegram: polling, @clearmind_jarvis_bot
- Node: 24.14.1
