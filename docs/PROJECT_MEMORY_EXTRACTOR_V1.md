# Project Memory Extractor v1

## Goal

Extract key project facts from existing topic/session context into a compact Markdown memory pack that agents can load at startup.

## Product shape

```
topic/session context
→ extracted Markdown memory pack
→ agent startup context
→ periodic refresh
```

## Non-goals

- no vector DB
- no OpenAI embeddings
- no OpenClaw memory-core dependency
- no automatic candidate promotion
- no mandatory wiki build
- no cross-topic SendMessage dependency
- no heavy phase/gate UX

## Canonical storage

Tracked/reviewed compiled memory:

- `.agent/memory/working/agent-brief.md`
- `.agent/memory/working/current-state.md`
- `.agent/memory/working/known-issues.md`
- optional later: `decisions.md`, `open-questions.md`, `project-brief.md`

Ignored/private runtime artifacts:

- `.agent/memory/index/`
- `.agent/memory/raw/`
- `.agent/memory/candidates/`
- `.agent/memory/.locks/`

## Inputs

- OpenClaw session JSONL for target topics
- `.agent/AGENT_CONTEXT.md`
- existing handoffs/reports
- GitHub PR/commit metadata
- operator notes

## Extraction rules

Extract only:
- stable facts
- current project state
- role/topic bindings
- active blockers
- explicit decisions
- do-not-do rules
- next useful actions

Do not extract:
- raw secrets
- credentials
- full logs
- speculative claims
- obsolete facts unless marked stale

## Agent startup behavior

Agents should load:
- `.agent/AGENT_CONTEXT.md`
- `.agent/memory/working/agent-brief.md`
- `.agent/memory/working/current-state.md`
- `.agent/memory/working/known-issues.md`

Agents must not run full `read-topic` on startup.

## Test strategy

### Test 1 — Initial onboarding test

Use current existing topics:
- `7301` coder
- `13350` reviewer
- `15222` infra

Expected:
- memory pack generated
- human reviews diff
- agents answer from memory pack

### Test 2 — Clean onboarding test

After v1 is implemented, rerun onboarding on the same three topics from a clean state.

Expected:
- deterministic output
- no manual troubleshooting
- no vector DB
- no OpenAI embeddings
- no full topic reread at agent startup
- agents start and function from memory pack

## Success criteria

- Agents recall project context without `read-topic`
- Agents know their role/topic
- Agents know current blockers
- Agents know what not to do
- Memory pack can be regenerated idempotently
- No vector DB / OpenAI embeddings required

## Relationship to Karpathy-style LLM Knowledge Bases

We adopt the core idea: raw sources are compiled by LLM into Markdown knowledge.

But our target is operational project memory for agents, not a personal research notebook.

Obsidian or other viewers may be added later on top of Markdown files, after longer real-world testing. They are not storage dependencies.

---

## Note on `onboard-project.py`

`onboard-project.py` is a helper for bootstrap/tool sync, not the main product flow. It may be used to set up the environment initially, but the core product is the memory pack extraction and agent startup behavior described above.
