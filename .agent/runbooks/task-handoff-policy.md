# Policy: Task Handoff

## When to Create a Handoff
- Before ending a session with incomplete work
- When handing a task to a different agent role
- After a blocking issue is discovered

## Handoff File Format
Location: `.agent/handoffs/YYYY-MM-DD-HHMMSS-<slug>.md`

Required sections:
- **Status** — what was done, what remains
- **Blockers** — anything preventing continuation
- **Next Steps** — ordered list for the receiving agent
- **Context Refs** — files the next agent must read

## On Session Start
Check `.agent/handoffs/` for the most recent file before reading anything else.
