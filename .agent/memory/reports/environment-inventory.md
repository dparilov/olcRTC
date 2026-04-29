---
title: Environment Inventory
project: olcRTC / Telemost
created_at: 2026-04-26T13:10:00+03:00
created_by: infra-agent
status: draft-for-review
scope: read-only inventory; no canonical promotion
---

> Historical inventory from 2026-04-26. Some recommendations are superseded by PR #1, which bootstraps `.agent/`.

# Environment Inventory

## 1. Inventory Scope

This report records the first read-only inventory for the OpenClaw shared-memory and task/review handoff infrastructure project.

Allowed Telegram topics/session sources confirmed by Dmitrii on 2026-04-26:

- Clearmind_projects / `OpenClaw_infra` / topic `15222`
- Clearmind_projects / `Telemost_review` / topic `13350`
- Clearmind_projects / `Telemost` / topic `7301`
- Clearmind_projects / `General` / topic `0`, selectively for context-skill and context-management infrastructure

No session history was modified. No memory was promoted. No canonical docs were changed.

## 2. OpenClaw Runtime

Observed via `openclaw status --all`, `openclaw agents list`, `openclaw agents list --bindings`, `openclaw channels status --probe`, and `openclaw gateway status`.

- OpenClaw version: `2026.4.23`
- OS: Linux `6.17.0-19-generic` x64
- Node: `24.14.1`
- Config: `~/.openclaw/openclaw.json`
- Dashboard: `http://127.0.0.1:18789/`
- Gateway:
  - systemd service installed, enabled, running
  - command: `/usr/bin/node /usr/lib/node_modules/openclaw/dist/index.js gateway --port 18789`
  - bind: loopback `127.0.0.1:18789`
  - probe: OK
- Tailscale: off
- Channel: stable
- Update notice: `pnpm · npm update 2026.4.24`

## 3. Telegram Channel

- Telegram channel: enabled and OK
- Account: `default`
- Bot: `@clearmind_jarvis_bot`
- Mode: polling
- Groups: unmentioned
- Allowlist shown by status: `125132275,5438761859,135331988`

## 4. Agents and Routing

Observed agents:

| Agent | Workspace | Model | Routing |
| --- | --- | --- | --- |
| `main` | `~/.openclaw/workspace` | `meridian/claude-sonnet-4-6` default; current topic overridden to `openai-codex/gpt-5.5` | default fallback |
| `codex` | `~/.openclaw/workspace` | `openai-codex/gpt-5.5` | Telegram `group:-1003596522926:topic:13350` |
| `reviewer` | `~/.openclaw/workspace` | `meridian/claude-sonnet-4-6` | Telegram `group:-1003596522926:topic:4853` |
| `alena` | `~/.openclaw/workspace-alena` | `meridian/claude-sonnet-4-6` | Telegram `group:-1003596522926:topic:391` |
| `uae` | `~/.openclaw/workspace-uae` | `meridiana/claude-opus-4-7` | Telegram direct `5438761859` |
| `tanya` | `~/.openclaw/workspace-tanya` | `meridiana/claude-opus-4-7` | Telegram direct `1539461057` |
| `andrey` | `~/.openclaw/workspace-andrey` | `meridiana/claude-opus-4-7` | Telegram direct `135331988` |

Current infra topic `15222 / OpenClaw_infra` does not have a dedicated routing rule yet and is currently handled by `main` with a user-selected model override to `openai-codex/gpt-5.5`.

## 5. Topic Mapping

Observed from Telegram topic-name cache and read-topic inventory:

| Topic | ID | Purpose / Current Role | Access Status |
| --- | ---: | --- | --- |
| `OpenClaw_infra` | `15222` | Infrastructure Agent onboarding and shared-memory infra project | allowed; read |
| `Telemost_review` | `13350` | Codex/review/architecture topic | allowed; read |
| `Telemost` | `7301` | Telemost implementation/testing topic | allowed; read |
| `General` | `0` | Selective context-management / skill-management infrastructure history | allowed selectively; read last 1000 requested-window messages returned 38 General messages |

## 6. Project Repository

Confirmed target repo:

- Path: `/home/dima/projects/telemost/olcRTC`
- Current branch: `v2-sso`
- Tracking: `origin/v2-sso`, local branch ahead by 32 commits
- Remotes:
  - `origin https://github.com/dparilov/olcRTC.git`
  - `upstream https://github.com/openlibrecommunity/olcrtc.git`

Current dirty tree at inventory time:

- Modified:
  - `build/android-bind/olcrtc-api21.aar`
  - `go.mod`
  - `go.sum`
  - `olcrtc-bootstrap`
- Untracked:
  - `olcrtc-windows`

Interpretation: repo is not clean; future infrastructure changes should avoid mixing with current implementation artifacts unless Dmitrii explicitly wants one combined branch/commit.

## 7. OpenClaw Workspace Repository

The agent workspace is itself a git repo:

- Path: `/home/dima/.openclaw/workspace`
- Remote: `https://github.com/dparilov/interior-render-pipeline.git`
- Branch: `main`, ahead of origin by 4 commits
- Dirty/untracked files exist, including workspace memory files, ops scripts, local sessions, and logs.

Interpretation: this is not the project repo for `.agent/`; it is OpenClaw workspace/config context.

## 8. Session Stores and Transcript Locations

OpenClaw session indexes:

- `~/.openclaw/agents/main/sessions/sessions.json`
- `~/.openclaw/agents/codex/sessions/sessions.json`
- `~/.openclaw/agents/reviewer/sessions/sessions.json`
- other agent stores under `~/.openclaw/agents/*/sessions/sessions.json`

Relevant observed transcript files:

| Source | Session key | Transcript file |
| --- | --- | --- |
| main / Telemost | `agent:main:telegram:group:-1003596522926:topic:7301` | `/home/dima/.openclaw/agents/main/sessions/f72f52c3-10c2-4bd8-a0e8-bdcde7a60799-topic-7301.jsonl` |
| main / Telemost_review | `agent:main:telegram:group:-1003596522926:topic:13350` | `/home/dima/.openclaw/agents/main/sessions/2b21b4dc-b45c-41f5-ae22-e8f97a9ba25b-topic-13350.jsonl` |
| codex / Telemost_review | `agent:codex:telegram:group:-1003596522926:topic:13350` | `/home/dima/.openclaw/agents/codex/sessions/ed25f290-7b9e-4c96-87a5-bf0bf0dc3b2b-topic-13350.jsonl` |
| main / OpenClaw_infra | `agent:main:telegram:group:-1003596522926:topic:15222` | `/home/dima/.openclaw/agents/main/sessions/158de596-38b5-44a6-a5e8-078a95a1124b-topic-15222.jsonl` |

Important: these files are evidence sources, not canonical truth. They must not be rewritten or destructively modified.

## 9. Telegram Userbot Context Reader

The context-reading mechanism is a core infrastructure dependency.

Skill:

- Path: `/home/dima/.openclaw/workspace/skills/read-topic/SKILL.md`
- Purpose: read Telegram topic history through Pyrogram userbot
- Required command pattern:

```bash
python3 ~/.openclaw/workspace/ops/read-topic.py <chat_id> <topic_id> 50000
```

Script:

- Path: `/home/dima/.openclaw/workspace/ops/read-topic.py`
- Uses Pyrogram `Client("userbot", workdir="/home/dima/.openclaw/workspace/ops")`
- Session file: `/home/dima/.openclaw/workspace/ops/userbot.session`
- General topic convention: `topic_id=0`
- Filtering behavior:
  - for topic `0`, keeps messages without thread id
  - for non-zero topic, keeps root topic message or messages where thread id equals topic id
- Output includes bounded topic transcript previews, not raw full Telegram export

Observed operational caveats:

- Large reads can trigger Telegram flood waits (`messages.GetHistory`, ~18-26 seconds repeatedly).
- Concurrent invocations can lock the sqlite session database (`sqlite3.OperationalError: database is locked`).
- Therefore future automation should serialize userbot reads and prefer bounded ranges/search/export over multiple concurrent full-history reads.

## 10. Topic Read Inventory

Read-topic runs performed for this inventory:

| Topic | Command / Scope | Result |
| --- | --- | --- |
| `15222 / OpenClaw_infra` | full available via `read-topic.py -1003596522926 15222 50000` | 10 messages |
| `13350 / Telemost_review` | full available via `read-topic.py -1003596522926 13350 50000` | 349 messages |
| `7301 / Telemost` | full available via `read-topic.py -1003596522926 7301 50000` | 7283 messages |
| `0 / General` | bounded via `read-topic.py -1003596522926 0 1000` | 38 General messages |

Temporary local outputs were written outside the project repo under `/tmp/openclaw-infra-inventory/` for inspection only. They are not canonical project artifacts.

## 11. MEMORY.md Locations

`find ~/.openclaw -maxdepth 5 -name MEMORY.md` found:

- `~/.openclaw/workspace-tanya/MEMORY.md`
- `~/.openclaw/workspace-guest-old/MEMORY.md`
- `~/.openclaw/workspace-uae/MEMORY.md`
- `~/.openclaw/workspace-andrey/MEMORY.md`

No `MEMORY.md` was found in the main workspace within the searched depth at inventory time, despite startup context containing long-term memory instructions. Further investigation may be needed before using agent MEMORY sources.

## 12. Meridian / MeridianA Context

Observed model usage:

- Meridian:
  - `meridian/claude-sonnet-4-6` is default for `main`, `reviewer`, `alena`.
- MeridianA:
  - `meridiana/claude-opus-4-7` is used by `uae`, `tanya`, and `andrey`.
- Codex:
  - `openai-codex/gpt-5.5` is configured for `codex` and currently selected in this infra topic.

Known workspace rule from `AGENTS.md`: long commands under MeridianA may receive SIGTERM; use background execution or longer `yieldMs` for long exec operations. The shared-memory scripts should therefore support resumable/bounded execution and avoid single huge foreground commands.

## 13. Initial Project Context Observed from Allowed Topics

This section is evidence-only and not canonicalized.

From `Telemost / 7301` and `Telemost_review / 13350`, the active project area appears to include:

- Telemost / olcRTC development and review workflow.
- Windows MVP / full-tunnel testing work.
- Branch names observed in topic summaries include `feat/windows-full-tunnel` and repo branch `v2-sso`.
- Current discussions include E2E Windows testing feasibility, tunnel settings, ARM Windows testing, GUI validation, and log readability.
- Reviewer/architect style emphasizes SHA-based review, acceptance criteria, logs, and non-speculative protocol changes.

These should be converted into candidate knowledge only after the candidate schema and extraction policy exist.

## 14. Risks / Constraints Identified

- Repo dirty state means infra changes should be isolated carefully.
- Userbot read operations must be serialized due sqlite lock/flood-wait behavior.
- Topic history is useful but should remain evidence, not source of truth.
- Dedicated routing for `OpenClaw_infra` does not exist yet; if this topic should become a true Infrastructure Agent route, that is an OpenClaw config/routing change requiring explicit approval.
- Full migration of `Telemost` history is large enough to require bounded extraction and validation before broader processing.
- High-risk/canonical knowledge promotion requires Dmitrii approval.

## 15. Recommended Next Atomic Step

Create the initial portable `.agent/` skeleton and policy/runbook placeholders in the target repo, without implementing extraction logic yet.

Suggested next outputs:

- `.agent/README.md`
- `.agent/tasks/README.md`
- `.agent/reviews/README.md`
- `.agent/decisions/README.md`
- `.agent/runbooks/README.md`
- `.agent/memory/README.md`
- `.agent/memory/candidates/README.md`
- `.agent/memory/working/README.md`
- `.agent/memory/reports/README.md`
- `scripts/agent_memory/README.md`
- `docs/agent-infra/README.md`

No scripts and no promotion/canonicalization in the next step unless separately approved.
