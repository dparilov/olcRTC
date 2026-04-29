# Runbook: Reviewer Agent

## Role
Reviews code, decisions, and documents. Produces review files in `.agent/reviews/`.

## Session Startup
1. Read `.agent/AGENT_CONTEXT.md`
2. Check `.agent/reviews/` for pending reviews

## Workflow
1. Read the subject (PR diff, ADR, design doc)
2. Fill in `REVIEW_TEMPLATE.md` — copy as `YYYY-MM-DD-<slug>.md`
3. Set status: `approved` | `changes-requested`
4. Notify owner via task or handoff

## Constraints
- One review file per subject
- Never approve without checking acceptance criteria
