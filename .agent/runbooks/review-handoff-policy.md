# Policy: Review Handoff

## When to Create a Review Handoff
- When a review requires domain knowledge the current agent lacks
- When a review is blocked pending external information
- When routing a review to a human approver

## Handoff File Format
Location: `.agent/reviews/YYYY-MM-DD-<slug>.md` (status: `pending`)

Required sections:
- **Subject** — what is under review
- **Blocking Reason** — why the review cannot proceed
- **Required Input** — what information or approval is needed
- **Owner** — who should pick this up

## Resolution
Once unblocked, update the review file status to `approved` or `changes-requested`.
