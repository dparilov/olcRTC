# Memory System

## Layers

| Dir | Layer | Description |
|-----|-------|-------------|
| raw/ | L0 | Audit logs — auto-managed |
| candidates/ | L1 | YAML extraction candidates — auto-managed |
| working/ | L2 | Active working memory — agent-maintained |
| promoted/ | L3 | Promoted facts — stable |
| reports/ | — | Pending reviews and contradictions |
| wiki/ | L3 | Built knowledge vault (run build-wiki.py) |

## Working Memory Files
- `current-state.md` — What is happening right now
- `active-decisions.md` — Decisions in flight
- `known-issues.md` — Known bugs/blockers
- `unresolved-questions.md` — Open questions
- `glossary.md` — Project-specific terms
- `agent-operating-context.md` — Agent instructions and context
