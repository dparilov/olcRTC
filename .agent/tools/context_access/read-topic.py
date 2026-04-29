#!/usr/bin/env python3
"""
read-topic.py — Читает историю топика через Pyrogram userbot.

Использование:
  python3 scripts/context_access/read-topic.py <topic_id|topic_name> [options]

Примеры:
  python3 scripts/context_access/read-topic.py telemost
  python3 scripts/context_access/read-topic.py 7301 --limit 300
  python3 scripts/context_access/read-topic.py 7301 --since-id 15800
  python3 scripts/context_access/read-topic.py 7301 --batch-format
  python3 scripts/context_access/read-topic.py 7301 --chat-id -1001234567890 --checkpoint-dir /tmp/cp
  python3 scripts/context_access/read-topic.py 7301 --config .agent/config.yaml

Выходные форматы:
  (default)       raw transcript — строки вида [DD.MM HH:MM] sender: text
  --batch-format  структурированный вывод для передачи в archive-batch-v2.py

Переменные окружения:
  OPENCLAW_AGENTS         путь к ~/.openclaw/agents/ (default: ~/.openclaw/agents)
  OPENCLAW_CHECKPOINT_DIR путь к директории чекпоинтов (default: .agent/checkpoints/ или legacy)
  PYROGRAM_SESSION        путь к файлу .session (default: ~/.openclaw/workspace/ops/userbot)
  PYROGRAM_VENV           путь к site-packages pyrogram (auto-detect если не задан)

Конфигурационный файл (.agent/config.yaml):
  checkpoint_dir: ~/project/.agent/checkpoints
  pyrogram_session: ~/.openclaw/workspace/ops/userbot
  agents_base: ~/.openclaw/agents

  Приоритет: CLI arg > env var > config file > auto-detect > legacy fallback
"""
import argparse
import asyncio
import importlib.util
import os
import re
import sys
import tempfile
from datetime import datetime, timezone
from pathlib import Path


# ---------------------------------------------------------------------------
# Project-root auto-detection
# ---------------------------------------------------------------------------

def detect_project_root(_script_path: "Path | None" = None) -> "Path | None":
    """
    If this script lives at <root>/.agent/tools/context_access/read-topic.py,
    return <root>. Otherwise return None.

    This lets the script auto-configure paths when installed via
    setup.sh --install-scripts copy|symlink.

    Args:
        _script_path: Override __file__ for unit testing only.
    """
    here = (_script_path if _script_path is not None else Path(__file__)).resolve().parent
    if (
        here.name == "context_access"
        and here.parent.name == "tools"
        and here.parent.parent.name == ".agent"
    ):
        return here.parent.parent.parent
    return None


# ---------------------------------------------------------------------------
# Config file
# ---------------------------------------------------------------------------

def load_agent_config(
    config_path: "str | None" = None,
    project_root: "Path | None" = None,
    _script_path: "Path | None" = None,
) -> dict:
    """
    Load .agent/config.yaml and return its contents as a dict.

    Config file location priority:
      1. config_path argument (from --config CLI arg)
      2. project_root / .agent / config.yaml
      3. Auto-detected project root (detect_project_root())

    Returns empty dict if file not found, not readable, or not parseable.
    Paths in values are NOT expanded here — callers apply .expanduser().

    Supported keys:
      checkpoint_dir    — directory for checkpoint files
      pyrogram_session  — path to Pyrogram .session file
      agents_base       — path to OpenClaw agents directory
    """
    if config_path:
        p = Path(config_path).expanduser()
    elif project_root:
        p = Path(project_root) / ".agent" / "config.yaml"
    else:
        proj = detect_project_root(_script_path)
        if proj:
            p = proj / ".agent" / "config.yaml"
        else:
            return {}

    if not p.exists():
        return {}

    try:
        # Try PyYAML first (may not be installed)
        import yaml  # type: ignore
        return yaml.safe_load(p.read_text()) or {}
    except ImportError:
        pass
    except Exception:
        return {}

    # Fallback: minimal "key: value" parser (no PyYAML dependency)
    result: dict = {}
    try:
        for line in p.read_text().splitlines():
            line = line.strip()
            if not line or line.startswith("#"):
                continue
            if ":" in line:
                k, _, v = line.partition(":")
                k = k.strip()
                v = v.strip()
                if k and v:
                    result[k] = v
    except Exception:
        return {}
    return result


# ---------------------------------------------------------------------------
# Path resolution helpers
# ---------------------------------------------------------------------------

def find_pyrogram(_script_path: "Path | None" = None) -> "str | None":
    """Locate pyrogram in known venv locations or system.

    Priority:
      1. PYROGRAM_VENV env var (explicit override)
      2. OpenClaw workspace .venv
      3. Project-local .venv (when running from .agent/tools/context_access/)

    Args:
        _script_path: Override __file__ for unit testing only.
    """
    candidates = []
    # 1. Explicit env var takes highest priority
    venv_env = os.environ.get("PYROGRAM_VENV", "")
    if venv_env:
        candidates.append(Path(venv_env).expanduser())
    # 2. OpenClaw workspace defaults
    candidates.extend([
        Path.home() / ".openclaw" / "workspace" / ".venv" / "lib" / "python3.12" / "site-packages",
        Path.home() / ".openclaw" / "workspace" / ".venv" / "lib" / "python3.11" / "site-packages",
    ])
    # 3. Project-local .venv (when running from .agent/tools/context_access/)
    proj = detect_project_root(_script_path)
    if proj:
        for pyver in ("3.12", "3.11", "3.10", "3.9"):
            candidates.append(proj / ".venv" / "lib" / f"python{pyver}" / "site-packages")
    for c in candidates:
        if c.exists() and (c / "pyrogram").exists():
            return str(c)
    return None  # rely on system python path


def resolve_checkpoint_dir(
    cli_arg: "str | None" = None,
    _script_path: "Path | None" = None,
    _config: "dict | None" = None,
) -> Path:
    """
    Resolve the directory used for checkpoint files.

    Priority:
      1. --checkpoint-dir CLI argument
      2. OPENCLAW_CHECKPOINT_DIR environment variable
      3. config file: checkpoint_dir key
      4. Auto-detect: <project_root>/.agent/checkpoints/
         (when script runs from .agent/tools/context_access/)
      5. Legacy fallback: ~/.openclaw/workspace/ops

    Args:
        cli_arg:      Value from --checkpoint-dir CLI arg.
        _script_path: Override script path for unit testing only.
        _config:      Pre-loaded config dict (avoids re-reading file in tests).
    """
    if cli_arg:
        return Path(cli_arg).expanduser()
    env = os.environ.get("OPENCLAW_CHECKPOINT_DIR")
    if env:
        return Path(env).expanduser()
    cfg = _config if _config is not None else {}
    cfg_val = cfg.get("checkpoint_dir")
    if cfg_val:
        return Path(cfg_val).expanduser()
    proj = detect_project_root(_script_path)
    if proj:
        return proj / ".agent" / "checkpoints"
    return Path.home() / ".openclaw" / "workspace" / "ops"


def agents_base(
    cli_arg: "str | None" = None,
    _config: "dict | None" = None,
) -> Path:
    """
    Resolve the agents base directory.

    Priority:
      1. --agents-base CLI argument
      2. OPENCLAW_AGENTS environment variable
      3. config file: agents_base key
      4. Default: ~/.openclaw/agents
    """
    if cli_arg:
        return Path(cli_arg).expanduser()
    env = os.environ.get("OPENCLAW_AGENTS")
    if env:
        return Path(env).expanduser()
    cfg = _config if _config is not None else {}
    cfg_val = cfg.get("agents_base")
    if cfg_val:
        return Path(cfg_val).expanduser()
    return Path.home() / ".openclaw" / "agents"


def find_session_file(
    session_file: "str | None" = None,
    _config: "dict | None" = None,
) -> "tuple[str, str]":
    """
    Locate userbot .session file.

    Priority:
      1. session_file parameter (from --session-file CLI arg)
      2. PYROGRAM_SESSION environment variable
      3. config file: pyrogram_session key
      4. Default location: ~/.openclaw/workspace/ops/userbot.session
      5. Glob search under ~/.openclaw/workspace/
    """
    explicit = session_file
    if not explicit:
        explicit = os.environ.get("PYROGRAM_SESSION")
    if not explicit:
        cfg = _config if _config is not None else {}
        explicit = cfg.get("pyrogram_session")
    if explicit:
        p = Path(explicit).expanduser()
        return str(p.parent), p.name.replace(".session", "")
    default = Path.home() / ".openclaw" / "workspace" / "ops"
    if (default / "userbot.session").exists():
        return str(default), "userbot"
    # search common locations
    import glob
    found = glob.glob(
        str(Path.home() / ".openclaw" / "workspace" / "**" / "*.session"),
        recursive=True,
    )
    if found:
        p = Path(found[0])
        return str(p.parent), p.stem
    raise SystemExit(
        "ERROR: userbot .session file not found. "
        "Set PYROGRAM_SESSION env var, use --session-file, "
        "or add pyrogram_session to .agent/config.yaml."
    )


# ---------------------------------------------------------------------------
# Checkpoint I/O
# ---------------------------------------------------------------------------

def checkpoint_path(topic_id: str, checkpoint_dir: "Path | None" = None) -> Path:
    """Path to sub-batch checkpoint file for this topic."""
    d = checkpoint_dir if checkpoint_dir is not None else resolve_checkpoint_dir()
    d.mkdir(parents=True, exist_ok=True)
    return d / f"read-topic-checkpoint-{topic_id}.json"


def load_checkpoint(topic_id: str, checkpoint_dir: "Path | None" = None) -> "int | None":
    """Return last processed message_id from checkpoint, or None."""
    import json as _json
    cp = checkpoint_path(topic_id, checkpoint_dir)
    if cp.exists():
        try:
            data = _json.loads(cp.read_text())
            return int(data["last_message_id"])
        except Exception:
            return None
    return None


def save_checkpoint(
    topic_id: str,
    last_message_id: int,
    sub_batch: int,
    checkpoint_dir: "Path | None" = None,
) -> None:
    """Atomically write checkpoint after processing a sub-batch.

    Uses temp file + os.replace so no partial write is ever visible.
    Temp file is cleaned up on any write error.
    """
    import json as _json
    cp = checkpoint_path(topic_id, checkpoint_dir)
    content = _json.dumps(
        {
            "topic_id": topic_id,
            "last_message_id": last_message_id,
            "sub_batch": sub_batch,
            "ts": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        },
        ensure_ascii=False,
    )
    # Write to a sibling temp file, then atomically replace
    tmp_fd, tmp_path = tempfile.mkstemp(dir=cp.parent, suffix=".tmp")
    try:
        with os.fdopen(tmp_fd, "w", encoding="utf-8") as fh:
            fh.write(content)
        os.replace(tmp_path, cp)
    except Exception:
        try:
            os.unlink(tmp_path)
        except OSError:
            pass
        raise
    print(
        f"[read-topic] Checkpoint saved: last_message_id={last_message_id} sub_batch={sub_batch}",
        file=sys.stderr,
    )


def clear_checkpoint(topic_id: str, checkpoint_dir: "Path | None" = None) -> None:
    cp = checkpoint_path(topic_id, checkpoint_dir)
    if cp.exists():
        cp.unlink()
        print(f"[read-topic] Checkpoint cleared for topic {topic_id}", file=sys.stderr)


# ---------------------------------------------------------------------------
# Import resolve_topic_id from archive-batch-v2 (hyphen in name → importlib)
# ---------------------------------------------------------------------------

def import_archive_batch():
    script = Path(__file__).parent / "archive-batch-v2.py"
    if not script.exists():
        raise SystemExit(f"ERROR: archive-batch-v2.py not found at {script}")
    spec = importlib.util.spec_from_file_location("archive_batch_v2", script)
    mod = importlib.util.module_from_spec(spec)
    sys.modules["archive_batch_v2"] = mod  # required for @dataclass to resolve module
    spec.loader.exec_module(mod)
    return mod


# ---------------------------------------------------------------------------
# Discover chat_id from session metadata for a given topic_id
# ---------------------------------------------------------------------------

def discover_chat_id(topic_id: str, base: Path) -> "str | None":
    """Extract Telegram chat_id from session files for this topic."""
    import glob
    patterns = [str(base / "*" / "sessions" / f"*-topic-{topic_id}.jsonl")]
    paths: list = []
    for p in patterns:
        paths.extend(glob.glob(p))
    if not paths:
        return None
    for path in sorted(paths):
        text = Path(path).read_text(errors="replace")
        m = re.search(r'"chat_id"\s*:\s*"?(?:telegram:)?(-?\d+)"?', text)
        if m:
            return m.group(1)
        # fallback: conversation_label
        m = re.search(r'conversation_label[^:]*:\s*"[^"]*id:(-?\d+)', text)
        if m:
            return m.group(1)
    return None


# ---------------------------------------------------------------------------
# Pyrogram reader with FloodWait retry
# ---------------------------------------------------------------------------

async def fetch_messages(
    chat_id: int,
    topic_id: int,
    limit: int,
    since_id: "int | None",
    workdir: str,
    session_name: str,
) -> list:
    """Fetch messages from Telegram topic. Returns list of (date, msg_id, sender, text)."""
    from pyrogram import Client
    from pyrogram.errors import FloodWait

    app = Client(session_name, workdir=workdir)
    messages: list = []

    max_retries = 4
    for attempt in range(max_retries):
        try:
            async with app:
                fetch_limit = min(limit * 4, 10000)  # fetch extra, filter by topic
                async for msg in app.get_chat_history(chat_id, limit=fetch_limit):
                    thread_id = (
                        getattr(msg, "message_thread_id", None)
                        or getattr(msg, "reply_to_message_id", None)
                    )
                    if topic_id == 0:
                        if thread_id is not None and thread_id != 0:
                            continue
                    else:
                        if msg.id != topic_id and thread_id != topic_id:
                            continue

                    if since_id is not None and msg.id <= since_id:
                        continue

                    text = msg.text or msg.caption or ""
                    if not text and msg.media:
                        text = f"[{msg.media}]"

                    sender = "unknown"
                    if msg.from_user:
                        sender = (
                            msg.from_user.first_name
                            or msg.from_user.username
                            or str(msg.from_user.id)
                        )
                    elif msg.sender_chat:
                        sender = msg.sender_chat.title or "chat"

                    messages.append((msg.date, msg.id, sender, text))
                    if len(messages) >= limit:
                        break
            break  # success

        except FloodWait as e:
            wait = e.value
            if attempt < max_retries - 1:
                print(
                    f"[read-topic] FloodWait {wait}s (attempt {attempt+1}/{max_retries}), waiting...",
                    file=sys.stderr,
                )
                await asyncio.sleep(wait + 2)
            else:
                raise SystemExit(f"ERROR: FloodWait {wait}s exceeded max retries") from e

    messages.sort(key=lambda x: x[0])
    return messages


# ---------------------------------------------------------------------------
# Output formatters
# ---------------------------------------------------------------------------

def print_raw(messages: list, chat_id: int, topic_id: int) -> None:
    print(f"=== Топик {topic_id} в чате {chat_id} ({len(messages)} сообщений) ===\n")
    for date, mid, sender, text in messages:
        preview = text[:500].replace("\n", " ")
        print(f"[{date.strftime('%d.%m %H:%M')}] {sender}: {preview}")
    print("\n=== END ===")
    if messages:
        last_id = messages[-1][1]
        print(f"\n# last-message-id: {last_id}", file=sys.stderr)


def print_batch_format(messages: list, chat_id: int, topic_id: int) -> None:
    """Output structured transcript block for downstream piping into archive-batch-v2.py."""
    now = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%S")
    print(f"## Transcript — {now}")
    print(f"## Source: telegram:{chat_id}:{topic_id} | messages: {len(messages)}")
    if messages:
        first_ts = messages[0][0].strftime("%Y-%m-%dT%H:%M:%S")
        last_ts = messages[-1][0].strftime("%Y-%m-%dT%H:%M:%S")
        last_id = messages[-1][1]
        print(f"## Range: {first_ts} → {last_ts} | last-id: {last_id}")
    print()
    for date, mid, sender, text in messages:
        ts = date.strftime("%Y-%m-%dT%H:%M")
        lines = text.strip().split("\n")
        for i, line in enumerate(lines):
            prefix = f"[{ts}] {sender}: " if i == 0 else " " * (len(ts) + len(sender) + 4)
            print(prefix + line)
    print()
    print("## END TRANSCRIPT")
    if messages:
        print("# Pipe this output to fact-extraction, then: archive-batch-v2.py <topic> --write", file=sys.stderr)
        print(f"# last-message-id: {messages[-1][1]}", file=sys.stderr)


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def main():
    parser = argparse.ArgumentParser(
        description="Read Telegram topic history via Pyrogram userbot"
    )
    parser.add_argument("topic", help="Topic ID (numeric) or topic name (e.g. telemost)")
    parser.add_argument("--limit", type=int, default=500, help="Max messages to fetch (default: 500)")
    parser.add_argument("--since-id", type=int, default=None, help="Only fetch messages after this message ID")
    parser.add_argument("--chat-id", type=str, default=None, help="Override chat_id (skip auto-discovery)")
    parser.add_argument("--batch-format", action="store_true", help="Output structured transcript for write-pipeline")
    parser.add_argument("--sub-batch-size", type=int, default=200,
                        help="Max messages per output sub-batch (default: 200).")
    parser.add_argument("--resume", action="store_true",
                        help="Resume from checkpoint: use last saved message ID as --since-id.")
    parser.add_argument("--clear-checkpoint", action="store_true",
                        help="Clear checkpoint file for this topic and exit.")
    # Portable path overrides
    parser.add_argument("--config", default=None,
                        help="Path to .agent/config.yaml. "
                             "Auto-detected from project root when running from .agent/tools/context_access/.")
    parser.add_argument("--checkpoint-dir", default=None,
                        help="Directory for checkpoint files. "
                             "Overrides OPENCLAW_CHECKPOINT_DIR env var and config. "
                             "Auto-detected as .agent/checkpoints/ when running from project tree.")
    parser.add_argument("--agents-base", default=None,
                        help="Path to OpenClaw agents dir. "
                             "Overrides OPENCLAW_AGENTS env var and config (default: ~/.openclaw/agents).")
    parser.add_argument("--session-file", default=None,
                        help="Path to Pyrogram .session file. "
                             "Overrides PYROGRAM_SESSION env var and config.")
    args = parser.parse_args()

    # Load config first (used as fallback in all resolver functions)
    cfg = load_agent_config(config_path=args.config)

    # Resolve portable paths up front
    cp_dir = resolve_checkpoint_dir(args.checkpoint_dir, _config=cfg)
    base = agents_base(args.agents_base, _config=cfg)
    ab = import_archive_batch()

    # Handle --clear-checkpoint early
    if args.clear_checkpoint:
        topic_id_str_early = ab.resolve_topic_id(args.topic, base)
        clear_checkpoint(topic_id_str_early, cp_dir)
        return

    # 1. resolve topic name → numeric ID
    topic_id_str = ab.resolve_topic_id(args.topic, base)
    topic_id_int = int(topic_id_str)

    # --resume: load since_id from checkpoint
    if args.resume and args.since_id is None:
        saved = load_checkpoint(topic_id_str, cp_dir)
        if saved is not None:
            print(f"[read-topic] Resuming from checkpoint: since_id={saved}", file=sys.stderr)
            args.since_id = saved
        else:
            print("[read-topic] No checkpoint found, reading from beginning", file=sys.stderr)

    # 2. discover chat_id
    chat_id_str = args.chat_id or discover_chat_id(topic_id_str, base)
    if not chat_id_str:
        raise SystemExit(
            f"ERROR: could not discover chat_id for topic {topic_id_str}. "
            "Use --chat-id to specify explicitly."
        )
    chat_id_int = int(chat_id_str)

    # 3. locate pyrogram
    venv = find_pyrogram()
    if venv:
        sys.path.insert(0, venv)

    # 4. locate session file
    workdir, session_name = find_session_file(args.session_file, _config=cfg)

    print(
        f"[read-topic] chat={chat_id_int} topic={topic_id_int} limit={args.limit} since_id={args.since_id}",
        file=sys.stderr,
    )

    # 5. fetch
    messages = asyncio.run(
        fetch_messages(
            chat_id=chat_id_int,
            topic_id=topic_id_int,
            limit=args.limit,
            since_id=args.since_id,
            workdir=workdir,
            session_name=session_name,
        )
    )

    # 6. sub-batch split + checkpoint
    sub = args.sub_batch_size
    total = len(messages)
    if total > sub:
        output_msgs = messages[:sub]
        remaining = total - sub
        last_id = output_msgs[-1][1]
        print(
            f"[read-topic] {total} messages fetched, outputting sub-batch 0 ({sub} msgs). "
            f"Remaining: {remaining}. Run with --resume to continue.",
            file=sys.stderr,
        )
        save_checkpoint(topic_id_str, last_id, sub_batch=0, checkpoint_dir=cp_dir)
    else:
        output_msgs = messages
        if args.resume:
            clear_checkpoint(topic_id_str, cp_dir)

    # 7. output
    if args.batch_format:
        print_batch_format(output_msgs, chat_id_int, topic_id_int)
    else:
        print_raw(output_msgs, chat_id_int, topic_id_int)


if __name__ == "__main__":
    main()
