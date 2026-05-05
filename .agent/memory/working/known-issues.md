# Known Issues

_Last updated: 2026-05-05 (seeded from session JSONL + AGENT_CONTEXT.md)_

---

| Issue | Severity | Status | Notes |
|-------|----------|--------|-------|
| SendMessage cross-topic blocked | **High** | Open | Error: `{"status":"error","tool":"message","error":"message required"}` for all 5 payload shapes tried from infra topic (15222) to coder (7301) and reviewer (13350). Cross-topic agent notification non-functional. Do not upgrade OpenClaw (pinned 2026.4.23). Schema/plugin mismatch requires investigation. |
| All topic-7301 windows are tier-c | **High** | Pending @pariloff review | 0 tier-b / 36 tier-c — all require manual approval before promotion. VPS credential batches in topic-7301 history are the blocking reason. Auto-promotion not possible until reviewed. |
| working/ and promoted/ empty (pre-2026-05-05) | **High** | Partially resolved | No extracted facts existed in any working/ file before this session. current-state.md and known-issues.md now seeded. promoted/ still empty — no facts have been formally promoted. |
| SendMessage schema: `SendMessage` field required | **Medium** | Open | `mcp__oc__SendMessage` action=send requires top-level `SendMessage` field (not `message` or `text`). Confirmed by repeated errors. Tool schema differs from what infra agent expected. |
| ip_address over-indexing in topic-7301 sensitive_map | Low | Open | Single VPS IP appears ~370× (per-occurrence count, not unique-IP count). Deduplication needed before map is used for redaction. Safe to read as-is; would produce noisy results if applied naively to redact. |
| wiki/ not built | Medium | Pending | No wiki artifact exists. Requires at least one L2 promotion first. Do not build wiki before promotion is approved. |
| OpenClaw runtime chunk incident 2026-05-04 | Medium | Documented | Root cause: gateway hook blocking. Incident file: `docs/INCIDENT_OPENCLAW_2026_5_RUNTIME_CHUNKS_2026-05-04.md` in openclaw-agent-memory-infra repo (commit `45722e4`). OpenClaw pinned at 2026.4.23. |
| OpenClaw memory-core vector search unavailable | **Low — Non-blocking** | Closed (reclassified) | No OpenAI API key configured; SQLite schema empty. Reclassified 2026-05-05: file-first memory is canonical; vector search is optional acceleration only. No action required. |
| PR3 gitignore decision not in decisions/ | Low | Open | PR3 merged to olcRTC master (commit `48d6d69`); `.agent/memory/` protected at `.gitignore` line 253 for all 3 topics. Decision should be formally recorded as ADR in `.agent/decisions/`. _[Pending @pariloff approval to write ADR]_ |
| File-first memory architectural decision not in decisions/ | Medium | Open | Decision made 2026-05-05: file-first canonical, vector DB optional. Not yet written as ADR. _[Pending @pariloff approval to write ADR]_ |
| infra-agent routing (topic:15222) | Low | Open | Topic 15222 (OpenClaw_infra) has no dedicated agent routing rule. Handled by `main` agent. Consider adding dedicated binding if persistent infra agent session is needed. |
