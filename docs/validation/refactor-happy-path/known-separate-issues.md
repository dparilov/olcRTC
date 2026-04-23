# Known Separate Issues

These are NOT part of refactor happy-path validation:

## Linux VP8 Data Frame Forwarding
- VP8 keepalive frames forwarded correctly Linux → Server
- VP8 data frames NOT forwarded Linux → Server
- Android data frames work correctly
- Documented in: `docs/validation/e2e-forensic-final.md`
- Status: open investigation, separate from refactor acceptance
