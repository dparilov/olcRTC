# Policy: Portability

## Scope
All scripts and tooling in this repository must run without modification on:
- Linux (Ubuntu 20.04+)
- macOS (12+, both Intel and Apple Silicon)
- Any environment where Python 3.9+ and Bash 4+ are available

## Rules
1. **No GNU-only coreutils** — use Python fallbacks for `realpath`, `date -d`, etc.
2. **No hardcoded paths** — use `SCRIPT_DIR`, `TARGET`, env vars
3. **No global installs** — scripts install to `.agent/tools/`, never to `~/.local/bin`
4. **Python stdlib only** — no third-party pip dependencies in core scripts
5. **POSIX-safe heredocs** — avoid nested heredocs; use Python writes for complex templates

## Testing Portability
Run `bash -n setup.sh` and `python3 -m py_compile scripts/context_access/*.py`
before every commit.
