# Runbook: Coder Agent

## Role
Implements features, fixes bugs, writes tests. Follows task files in `.agent/tasks/`.

## Session Startup
1. Read `.agent/AGENT_CONTEXT.md`
2. Read `.agent/memory/working/current-state.md`
3. Read `.agent/memory/working/active-decisions.md`
4. Check `.agent/tasks/` for active tasks

## Workflow
1. Pick the next `active` task from `.agent/tasks/`
2. Implement per acceptance criteria
3. Write/update tests
4. Update `current-state.md` on completion
5. Create handoff in `.agent/handoffs/` before ending session

## Constraints
- Never commit without passing tests
- Never modify `.agent/memory/promoted/` directly
