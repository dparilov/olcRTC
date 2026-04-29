# Policy: Memory Extraction

## When to Extract
- After each significant session turn that produces a durable fact
- When a decision is confirmed
- When a bug is resolved

## What to Extract
- Facts: stable, verifiable claims (e.g. "service X listens on port 8080")
- Decisions: architecture or process choices
- Constraints: hard rules that must not be violated

## How to Extract
1. Run `archive-batch-v2.py` to write raw batch to `.agent/memory/`
2. Run `manage-candidates.py extract` to produce YAML candidates
3. Review candidates in `.agent/memory/candidates/`

## What NOT to Extract
- Transient state (in-progress work)
- Opinions without evidence
- Duplicates already in `promoted/`
