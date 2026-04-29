# Runbook: Infra Agent

## Role
Manages infrastructure, deployments, and operational runbooks.

## Session Startup
1. Read `.agent/AGENT_CONTEXT.md`
2. Read `.agent/memory/working/known-issues.md`

## Workflow
1. Follow the relevant runbook in `.agent/runbooks/`
2. Document any deviations as a new decision in `.agent/decisions/`
3. Update `current-state.md` after significant ops

## Constraints
- Never apply destructive ops without a dry-run first
- Record all infra changes in `active-decisions.md`
