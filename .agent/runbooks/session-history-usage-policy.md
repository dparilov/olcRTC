# Policy: Session History Usage

## Purpose
Session history (Telegram batches, agent transcripts) is raw input for memory extraction.
It must NOT be treated as ground truth for decisions.

## Allowed Uses
- Source evidence for candidate extraction
- Reconstructing context when working memory is incomplete
- Auditing past decisions

## Prohibited Uses
- Do not copy-paste history into `promoted/` directly
- Do not use history as a substitute for reading `AGENT_CONTEXT.md`
- Do not re-process already-archived batches (check `last-batch` header)

## Batch Tracking
Each memory file carries `<!-- last-batch: N -->` in its header.
Always pass `--batch` or let `archive-batch-v2.py` infer the next batch number.
