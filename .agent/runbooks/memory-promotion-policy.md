# Policy: Memory Promotion

## Auto-promotion Criteria
A candidate may be auto-promoted when ALL of the following hold:
- `risk: low`
- `confidence: medium` or higher
- No high-risk keywords (secret, token, password, production, gdpr)
- `status: needs-approval` is NOT set

## Manual Approval Required
- `risk: medium` or `high`
- Type: `architecture-decision`, `constraint`, `process-rule`
- Any claim containing high-risk keywords

## Process
1. Run `python .agent/tools/context_access/manage-candidates.py <topic> --promote-auto --memory-dir .agent/memory`
2. Review `.agent/memory/reports/pending-approval.md` for manual items
3. After human approval, run `python .agent/tools/context_access/manage-candidates.py <topic> --approve <candidate-id> --memory-dir .agent/memory`

## After Promotion
- Promoted facts are appended to `.agent/memory/topic-<id>.md` as a new batch.
- Candidate YAML status is updated to `auto-promoted` or `approved`.
- After promotion, run `build-wiki.py` and `validate-wiki.py`.
