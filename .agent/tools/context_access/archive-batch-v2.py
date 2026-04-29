#!/usr/bin/env python3
"""
archive-batch-v2.py — deduplicating OpenClaw topic transcript batch reader.

Reads OpenClaw JSONL transcript files for a Telegram topic, merges them
chronologically, deduplicates overlapping reset-file history, and prints a
bounded batch for later memory extraction.

This script is read-only unless --mark-done or --reset is used. It does not
write memory markdown files.

Usage:
  python3 scripts/context_access/archive-batch-v2.py <topic_id|topic_name> --status
  python3 scripts/context_access/archive-batch-v2.py <topic_id|topic_name> --total
  python3 scripts/context_access/archive-batch-v2.py <topic_id|topic_name> --batch 0
  python3 scripts/context_access/archive-batch-v2.py <topic_id|topic_name> --batch 0 --batch-size 100
  python3 scripts/context_access/archive-batch-v2.py <topic_id|topic_name> --mark-done 0

  topic_id is REQUIRED — numeric ID (e.g. 7301) or topic name (e.g. telemost).
  Running without topic_id is not allowed.

Dedupe strategy:
  1. If Telegram inbound metadata is present in message text, dedupe by
     telegram:<chat_id>:<topic_id>:<message_id>:<role>.
  2. Otherwise fallback to role + minute timestamp bucket + normalized content hash.
"""
from __future__ import annotations

import argparse
import glob
import hashlib
import json
import os
import re
import sys
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

from io_utils import atomic_write_text, atomic_append_text, locked_path, write_text_in_lock

DEFAULT_AGENTS_BASE = Path.home() / ".openclaw" / "agents"
DEFAULT_PROGRESS_DIR = Path.home() / ".openclaw" / "workspace" / "ops"


@dataclass(frozen=True)
class Message:
    ts_ms: int
    role: str
    text: str
    seq: str
    path: str
    dedupe_key: str


def iso_to_ms(value: str) -> int:
    try:
        s = value.rstrip("Z")
        fmt = "%Y-%m-%dT%H:%M:%S.%f" if "." in s else "%Y-%m-%dT%H:%M:%S"
        return int(datetime.strptime(s, fmt).replace(tzinfo=timezone.utc).timestamp() * 1000)
    except Exception:
        return 0


def ts_to_str(ts_ms: int) -> str:
    if not ts_ms:
        return "?"
    try:
        return datetime.fromtimestamp(ts_ms / 1000, tz=timezone.utc).strftime("%Y-%m-%d %H:%M UTC")
    except Exception:
        return "?"


def content_text(content: Any) -> str:
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        parts: list[str] = []
        for item in content:
            if isinstance(item, dict) and item.get("type") == "text":
                parts.append(str(item.get("text", "")))
        return "\n".join(parts)
    return str(content or "")


def parse_jsonl_line(line: str) -> tuple[str, str, int, str] | None:
    try:
        obj = json.loads(line)
    except Exception:
        return None

    # Newer OpenClaw format.
    if obj.get("type") == "message" and isinstance(obj.get("message"), dict):
        inner = obj["message"]
        role = str(inner.get("role", ""))
        if role not in ("user", "assistant"):
            return None
        text = content_text(inner.get("content", ""))
        ts_raw = obj.get("timestamp", "")
        ts_ms = iso_to_ms(ts_raw) if isinstance(ts_raw, str) else int(ts_raw or 0)
        seq = str(obj.get("id", "?"))
        return role, text, ts_ms, seq

    # Older OpenClaw format.
    role = str(obj.get("role", ""))
    if role not in ("user", "assistant"):
        return None
    text = content_text(obj.get("content", ""))
    ts_raw = obj.get("timestamp", 0)
    ts_ms = iso_to_ms(ts_raw) if isinstance(ts_raw, str) else int(ts_raw or 0)
    seq = str(obj.get("__openclaw", {}).get("seq", "?"))
    return role, text, ts_ms, seq


def extract_inbound_meta(text: str) -> dict[str, str] | None:
    """Extract minimal trusted-looking inbound metadata embedded in user text.

    Transcript user messages contain an OpenClaw-generated metadata envelope.
    We only need chat/topic/message ids for dedupe. If parsing fails, return None.
    """
    message_id = re.search(r'"message_id"\s*:\s*"?(\d+)"?', text)
    if not message_id:
        return None

    # Prefer explicit chat_id if present; otherwise parse from conversation_label.
    chat_id = re.search(r'"chat_id"\s*:\s*"?(?:telegram:)?(-?\d+)"?', text)
    if not chat_id:
        chat_id = re.search(r'conversation_label"\s*:\s*"[^"]*id:(-?\d+)', text)

    topic_id = re.search(r'"topic_id"\s*:\s*"?(\d+)"?', text)
    if not topic_id:
        topic_id = re.search(r'conversation_label"\s*:\s*"[^"]*topic:(\d+)', text)

    return {
        "message_id": message_id.group(1),
        "chat_id": chat_id.group(1) if chat_id else "unknown-chat",
        "topic_id": topic_id.group(1) if topic_id else "unknown-topic",
    }


def normalize_text(text: str) -> str:
    return re.sub(r"\s+", " ", text).strip()


def dedupe_key(role: str, text: str, ts_ms: int) -> str:
    meta = extract_inbound_meta(text)
    if meta:
        return f"telegram:{meta['chat_id']}:{meta['topic_id']}:{meta['message_id']}:{role}"
    bucket_ms = (ts_ms // 60000) * 60000 if ts_ms else 0
    digest = hashlib.sha256(normalize_text(text).encode("utf-8", "replace")).hexdigest()[:24]
    return f"fallback:{role}:{bucket_ms}:{digest}"


def discover_topic_names(agents_base: Path) -> dict[str, str]:
    """Scan session files to build a topic_name -> topic_id map.

    Reads the first 20 lines of each primary JSONL file looking for
    Telegram metadata containing topic_id and topic_name fields.
    Returns a dict mapping lowercase topic name to numeric topic id string.
    Multiple names may map to the same id (aliases are fine).
    """
    name_to_id: dict[str, str] = {}
    pattern = str(agents_base / "*" / "sessions" / "*.jsonl")
    for path in sorted(glob.glob(pattern)):
        if ".trajectory." in path or ".reset." in path:
            continue
        try:
            with open(path, encoding="utf-8", errors="replace") as f:
                checked = 0
                for line in f:
                    if checked >= 20:
                        break
                    parsed = parse_jsonl_line(line.strip())
                    if not parsed:
                        continue
                    role, text, _, _ = parsed
                    if role != "user":
                        checked += 1
                        continue
                    tid_m = re.search(r'"topic_id"\s*:\s*"?(\d+)"?', text)
                    tname_m = re.search(r'"topic_name"\s*:\s*"([^"]+)"', text)
                    if tid_m and tname_m:
                        tid = tid_m.group(1)
                        tname = tname_m.group(1).strip()
                        # Store both the original name (lowercased) and a simplified slug.
                        name_to_id[tname.lower()] = tid
                        slug = re.sub(r"[^a-z0-9]+", "-", tname.lower()).strip("-")
                        if slug and slug != tname.lower():
                            name_to_id[slug] = tid
                        break
                    checked += 1
        except Exception:
            continue
    return name_to_id


def resolve_topic_id(raw: str, agents_base: Path) -> str:
    """Resolve a topic id or name to a numeric topic id string.

    If raw is already numeric, return as-is.
    Otherwise scan session files for a matching topic_name.
    Raises SystemExit with a helpful message if not found.
    """
    if re.match(r"^\d+$", raw):
        return raw

    name_map = discover_topic_names(agents_base)
    key = raw.lower()

    # Exact match.
    if key in name_map:
        resolved = name_map[key]
        print(f"Resolved topic name '{raw}' -> topic_id:{resolved}", file=sys.stderr)
        return resolved

    # Partial match as last resort (only if unambiguous).
    matches = [(n, i) for n, i in name_map.items() if key in n or n in key]
    if len(matches) == 1:
        resolved = matches[0][1]
        print(
            f"Resolved topic name '{raw}' -> '{matches[0][0]}' -> topic_id:{resolved} (partial match)",
            file=sys.stderr,
        )
        return resolved

    available = "\n  ".join(f"{n} -> {i}" for n, i in sorted(name_map.items()))
    raise SystemExit(
        f"ERROR: topic name '{raw}' not found in session files.\n"
        f"Available topic names:\n  {available or '(none found)'}\n"
        f"Tip: Use numeric topic ID directly, or check {agents_base}/*/sessions/"
    )


def find_topic_paths(topic_id: str, agents_base: Path) -> list[str]:
    found: set[str] = set()
    for pattern in (
        str(agents_base / "*" / "sessions" / f"*-topic-{topic_id}.jsonl"),
        str(agents_base / "*" / "sessions" / f"*-topic-{topic_id}.jsonl.reset.*"),
    ):
        for p in glob.glob(pattern):
            if ".trajectory." not in p:
                found.add(p)
    return sorted(found, key=os.path.getmtime)


def load_messages(topic_id: str, agents_base: Path) -> tuple[list[Message], int, int, list[str]]:
    paths = find_topic_paths(topic_id, agents_base)
    if not paths:
        raise SystemExit(f"ERROR: no session files found for topic {topic_id} under {agents_base}")

    raw_count = 0
    duplicate_count = 0
    seen: dict[str, Message] = {}

    for path in paths:
        with open(path, encoding="utf-8", errors="replace") as f:
            for line in f:
                parsed = parse_jsonl_line(line.strip())
                if not parsed:
                    continue
                role, text, ts_ms, seq = parsed
                raw_count += 1
                if not normalize_text(text):
                    duplicate_count += 1
                    continue
                key = dedupe_key(role, text, ts_ms)
                msg = Message(ts_ms=ts_ms, role=role, text=text, seq=seq, path=path, dedupe_key=key)
                old = seen.get(key)
                if old is None:
                    seen[key] = msg
                else:
                    duplicate_count += 1
                    # Keep the earliest timestamp; if equal, keep current only if it has more text.
                    if (msg.ts_ms and old.ts_ms and msg.ts_ms < old.ts_ms) or len(msg.text) > len(old.text):
                        seen[key] = msg

    messages = sorted(seen.values(), key=lambda m: (m.ts_ms, m.role, m.dedupe_key))
    return messages, raw_count, duplicate_count, paths


def progress_file(progress_dir: Path, topic_id: str) -> Path:
    return progress_dir / f"archive-progress-{topic_id}-v2.json"


def load_progress(path: Path, topic_id: str) -> dict[str, Any]:
    if path.exists():
        try:
            return json.loads(path.read_text())
        except Exception:
            pass
    return {"topic_id": topic_id, "last_completed_batch": -1, "total_batches": None, "updated": None}


def save_progress(path: Path, progress: dict[str, Any]) -> None:
    """Write progress atomically. Caller must hold locked_path(path)."""
    write_text_in_lock(path, json.dumps(progress, ensure_ascii=False, indent=2) + "\n")


def infer_next_batch_n_from_content(
    content: str,
    progress_file: "Path | None",
    topic_id: str,
    explicit_batch: int | None = None,
) -> int:
    """Infer next batch number from already-read memory content + optional progress file.

    Designed to be called inside the write lock so content is always fresh.
    """
    if explicit_batch is not None:
        return explicit_batch

    candidates: list[int] = [-1]  # ensures result >= 0

    # Source 1: progress file
    if progress_file is not None:
        prog = load_progress(progress_file, topic_id)
        candidates.append(int(prog.get("last_completed_batch", -1)))

    # Source 2: <!-- last-batch: N | ... --> memory header
    m = re.search(r"last-batch:\s*(\d+)", content)
    if m:
        candidates.append(int(m.group(1)))

    # Source 3: ## [date] Batch N — session ... section headers
    for m in re.finditer(r"## \[.*?\] Batch (\d+)", content):
        candidates.append(int(m.group(1)))

    return max(candidates) + 1


def infer_next_batch_n(
    memory_file: Path,
    progress_file: Path,
    topic_id: str,
    explicit_batch: int | None = None,
) -> int:
    """Return the next batch number to write, without reading session files.

    For dry-run / display: reads memory file outside the lock.
    For real writes, use write_batch_to_memory(batch_n=None) which calls
    infer_next_batch_n_from_content() inside the locked transaction.
    """
    if explicit_batch is not None:
        return explicit_batch

    content = ""
    if memory_file.exists():
        try:
            content = memory_file.read_text(encoding="utf-8")
        except OSError:
            pass

    return infer_next_batch_n_from_content(content, progress_file, topic_id)


def print_stats(topic_id: str, paths: list[str], raw_count: int, deduped_count: int, duplicate_count: int, batch_size: int, total_batches: int) -> None:
    print("=" * 60)
    print(f"ARCHIVE SOURCE STATS  [topic:{topic_id}]")
    print(f"  Session files : {len(paths)}")
    print(f"  Raw messages  : {raw_count}")
    print(f"  Duplicates    : {duplicate_count}")
    print(f"  Deduped msgs  : {deduped_count}")
    print(f"  Batch size    : {batch_size}")
    print(f"  Total batches : {total_batches}")
    print("=" * 60)


def message_preview(text: str, max_chars: int) -> str:
    if max_chars <= 0:
        return text
    return text if len(text) <= max_chars else text[:max_chars] + "\n...[truncated]"




def read_facts(source: str) -> list[str]:
    """Read fact lines from a file path or stdin ('-')."""
    if source == "-":
        raw = sys.stdin.read()
    else:
        raw = Path(source).read_text(encoding="utf-8", errors="replace")
    return [line for line in raw.splitlines() if line.strip()]


def extract_existing_bullets(content: str) -> list[tuple[str, int]]:
    """Extract (bullet_text, batch_number) tuples from existing memory file content."""
    bullets: list[tuple[str, int]] = []
    current_batch = -1
    batch_re = re.compile(r"^## \[\d{4}-\d{2}-\d{2}\] Batch (\d+)")
    canonical_re = re.compile(r"^## Canonical facts")
    for line in content.splitlines():
        m = batch_re.match(line)
        if m:
            current_batch = int(m.group(1))
            continue
        if canonical_re.match(line):
            current_batch = 0
            continue
        stripped = line.strip()
        if stripped.startswith("- ") and not stripped.startswith("- \u26a0\ufe0f"):
            bullets.append((stripped, current_batch))
    return bullets


def significant_words(text: str) -> set[str]:
    stopwords = {
        "\u044d\u0442\u043e\u0442", "\u044d\u0442\u043e\u043c", "\u044d\u0442\u0438\u043c", "\u044d\u0442\u043e\u0439",
        "\u0447\u0435\u0440\u0435\u0437", "\u0442\u0430\u043a\u0436\u0435", "\u043a\u043e\u0433\u0434\u0430", "\u043f\u043e\u0441\u043b\u0435",
        "\u043f\u0435\u0440\u0435\u0434", "\u043c\u0435\u0436\u0434\u0443",
        "\u043a\u043e\u0442\u043e\u0440\u044b\u0435", "\u043a\u043e\u0442\u043e\u0440\u044b\u0439",
        "\u043a\u043e\u0442\u043e\u0440\u043e\u0433\u043e", "\u043a\u043e\u0442\u043e\u0440\u043e\u0439",
        "which", "their", "about", "after", "before", "where", "there",
        "these", "those", "have", "with", "from", "that", "this", "will",
        "been",
        "\u0431\u044b\u043b\u0438", "\u0431\u044b\u043b\u043e", "\u0431\u0443\u0434\u0435\u0442", "\u0435\u0441\u0442\u044c",
        "\u0438\u0441\u043f\u043e\u043b\u044c\u0437\u0443\u0435\u0442\u0441\u044f", "\u044f\u0432\u043b\u044f\u0435\u0442\u0441\u044f",
    }
    words = re.findall(r"\b\w{5,}\b", text.lower())
    return {w for w in words if w not in stopwords}


def detect_conflicts(
    new_facts: list[str],
    existing_bullets: list[tuple[str, int]],
) -> dict[int, tuple[str, str, int]]:
    """Return dict mapping new_fact_index -> (new_fact, conflicting_line, existing_batch_n).

    A conflict is flagged when 2+ significant words from a new fact also appear in an
    existing bullet AND the lines are not near-identical AND word overlap ratio > 0.5.
    Heuristic only — false negatives expected at Phase 1.
    """
    conflicts: dict[int, tuple[str, str, int]] = {}
    for i, new_fact in enumerate(new_facts):
        if not new_fact.strip().startswith("- "):
            continue
        new_words = significant_words(new_fact)
        if len(new_words) < 2:
            continue
        best_match: tuple[str, int] | None = None
        best_overlap = 0
        for existing_line, batch_n in existing_bullets:
            existing_words = significant_words(existing_line)
            overlap = len(new_words & existing_words)
            if overlap < 2 or overlap <= best_overlap:
                continue
            norm_new = normalize_text(new_fact.lower())
            norm_existing = normalize_text(existing_line.lower())
            if norm_new == norm_existing:
                continue
            ratio = overlap / max(len(new_words), len(existing_words), 1)
            if ratio > 0.5:
                best_match = (existing_line, batch_n)
                best_overlap = overlap
        if best_match:
            conflicts[i] = (new_fact, best_match[0], best_match[1])
    return conflicts


def resolve_memory_file(
    memory_file: "Path | None",
    memory_dir: "Path | None",
    topic_id: str,
) -> Path:
    if memory_file:
        return memory_file
    if memory_dir:
        return memory_dir / f"topic-{topic_id}.md"
    raise SystemExit("ERROR: --write requires --memory-file <path> or --memory-dir <dir>")


def write_audit_log(
    memory_file: Path,
    topic_id: str,
    batch_n: int,
    session_id: str,
    facts: list[str],
    source_paths: list[str] | None = None,
) -> Path:
    """Append-only L0 audit log entry written BEFORE any memory file mutation.

    Location: <memory_dir>/raw/topic-<id>-audit.log
    Format: JSON-lines, one entry per archive operation.
    Never modified after write — append only.
    """
    import json as _json
    audit_dir = memory_file.parent / "raw"
    audit_dir.mkdir(parents=True, exist_ok=True)
    audit_path = audit_dir / f"topic-{topic_id}-audit.log"

    entry = {
        "ts": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "session_id": session_id,
        "batch_n": batch_n,
        "topic_id": topic_id,
        "fact_count": sum(1 for f in facts if f.strip()),
        "facts": [f for f in facts if f.strip()],
        "sources": [os.path.basename(p) for p in (source_paths or [])],
    }
    atomic_append_text(audit_path, _json.dumps(entry, ensure_ascii=False) + "\n")

    print(f"L0 audit: wrote entry to {audit_path}", file=sys.stderr)
    return audit_path


def write_batch_to_memory(
    memory_file: Path,
    topic_id: str,
    batch_n: int | None,
    session_id: str,
    facts: list[str],
    existing_bullets: list[tuple[str, int]] | None = None,
    source_paths: list[str] | None = None,
    progress_file: Path | None = None,
) -> int:
    """Append a new batch section to the memory file.

    Idempotent: if session_id is already present, does nothing and returns 0.
    Performs heuristic conflict detection and annotates conflicting facts.
    Updates the <!-- last-batch: ... --> metadata comment in the file header.
    Returns number of non-empty fact lines written.
    """
    now_utc = datetime.now(timezone.utc)
    date_str = now_utc.strftime("%Y-%m-%d")
    timestamp_str = now_utc.strftime("%Y-%m-%dT%H:%MZ")

    with locked_path(memory_file):
        existing_content = memory_file.read_text(encoding="utf-8") if memory_file.exists() else ""

        # Resolve batch_n inside the lock from fresh content (A5: no session file scan).
        if batch_n is None:
            batch_n = infer_next_batch_n_from_content(existing_content, progress_file, topic_id)

        # Idempotency guard
        if f"session {session_id}" in existing_content:
            print(
                f"SKIP: session {session_id} already present in {memory_file} (idempotent)",
                file=sys.stderr,
            )
            return 0

        # L0: write audit log BEFORE any memory mutation
        write_audit_log(
            memory_file=memory_file,
            topic_id=topic_id,
            batch_n=batch_n,
            session_id=session_id,
            facts=facts,
            source_paths=source_paths,
        )

        # Recompute bullets from the fresh read for accurate conflict detection
        fresh_bullets = extract_existing_bullets(existing_content)
        conflicts = detect_conflicts(facts, fresh_bullets)

        # Build the new section
        section_lines: list[str] = [f"\n## [{date_str}] Batch {batch_n} \u2014 session {session_id}"]
        fact_count = 0
        for i, fact in enumerate(facts):
            if not fact.strip():
                continue
            section_lines.append(fact)
            fact_count += 1
            if i in conflicts:
                _, conflicting_line, conflict_batch = conflicts[i]
                trimmed = conflicting_line[:80] + ("..." if len(conflicting_line) > 80 else "")
                section_lines.append(
                    f"  - \u26a0\ufe0f CONFLICT: Batch {conflict_batch} \u0443\u043a\u0430\u0437\u044b\u0432\u0430\u043b: {trimmed}"
                )
        section_lines.append("")
        section = "\n".join(section_lines)

        # Build updated metadata comment
        meta_re = re.compile(r"<!-- last-batch:.*?-->")
        range_match = re.search(r"batches: (\d+)[\u2013\-](\d+)", existing_content)
        first_batch = range_match.group(1) if range_match else str(batch_n)
        new_meta = (
            f"<!-- last-batch: {batch_n} | last-write: {timestamp_str}"
            f" | batches: {first_batch}\u2013{batch_n} -->"
        )

        # Initialise file when empty/new
        if not memory_file.exists() or not existing_content.strip():
            memory_file.parent.mkdir(parents=True, exist_ok=True)
            existing_content = f"# Memory: topic-{topic_id}\n\n"

        if meta_re.search(existing_content):
            new_content = meta_re.sub(new_meta, existing_content) + section
        else:
            # Insert metadata after the title line
            parts = existing_content.split("\n", 2)
            if parts and parts[0].startswith("# Memory"):
                new_content = parts[0] + "\n\n" + new_meta + "\n" + "\n".join(parts[1:]) + section
            else:
                new_content = new_meta + "\n" + existing_content + section

        write_text_in_lock(memory_file, new_content)

    print(f"Written: {fact_count} facts to {memory_file} (Batch {batch_n}, session {session_id})")
    if conflicts:
        print(f"Conflicts flagged: {len(conflicts)}")
    return fact_count

def main() -> int:
    parser = argparse.ArgumentParser(
        description="Deduplicating archive batch reader for OpenClaw topic transcripts.",
        epilog="topic_id is required — numeric (e.g. 7301) or topic name (e.g. telemost).",
    )
    parser.add_argument(
        "topic_id",
        help="Numeric Telegram topic ID or topic name (e.g. 'telemost', 'OpenClaw_infra'). REQUIRED.",
    )
    parser.add_argument("--batch", type=int)
    parser.add_argument("--batch-size", type=int, default=100)
    parser.add_argument("--total", action="store_true")
    parser.add_argument("--status", action="store_true")
    parser.add_argument("--mark-done", type=int)
    parser.add_argument("--reset", action="store_true")
    parser.add_argument(
        "--max-text", type=int, default=0,
        help="truncate message text in batch output; 0 means no truncation",
    )
    parser.add_argument(
        "--write", metavar="FACTS_FILE",
        help="Append extracted facts to memory file. Path to facts file or '-' for stdin.",
    )
    parser.add_argument(
        "--session-id",
        help="Session ID for idempotency dedup. Auto-generated UUID if not provided.",
    )
    parser.add_argument(
        "--memory-file", type=Path,
        help="Explicit path to memory/<topic>.md output file.",
    )
    parser.add_argument(
        "--memory-dir", type=Path,
        help="Directory for memory files; auto-named topic-<id>.md.",
    )
    parser.add_argument(
        "--auto-mark-done", action="store_true",
        help="Automatically --mark-done the batch after a successful --write.",
    )
    parser.add_argument(
        "--compact", action="store_true",
        help="Print memory file formatted for LLM compaction (read-only, no writes).",
    )
    parser.add_argument(
        "--dry-run", action="store_true",
        help="With --write: show what would be written (memory file, session_id, "
             "fact_count, conflicts) without mutating any file. Safe to run repeatedly.",
    )
    parser.add_argument("--agents-base", type=Path, default=DEFAULT_AGENTS_BASE)
    parser.add_argument("--progress-dir", type=Path, default=DEFAULT_PROGRESS_DIR)
    args = parser.parse_args()


    # Resolve topic name -> numeric id before any further processing.
    args.topic_id = resolve_topic_id(args.topic_id, args.agents_base)

    pfile = progress_file(args.progress_dir, args.topic_id)


    if args.compact:
        memory_file = resolve_memory_file(args.memory_file, args.memory_dir, args.topic_id)
        if not memory_file.exists():
            raise SystemExit(f"ERROR: memory file not found: {memory_file}")
        content = memory_file.read_text(encoding="utf-8")
        bullet_count = sum(1 for l in content.splitlines() if l.strip().startswith("- "))
        conflict_count = sum(1 for l in content.splitlines() if "⚠️ CONFLICT" in l)
        print(f"COMPACT INPUT  [topic:{args.topic_id}]")
        print(f"  Memory file  : {memory_file}")
        print(f"  Total bullets: {bullet_count}")
        print(f"  Conflicts    : {conflict_count}")
        print(f"  Size         : {len(content)} chars")
        print("=" * 60)
        print(content)
        print("=" * 60)
        print("# Pipe this to LLM compaction, then write back with --write and --session-id compact-<date>")
        return 0

    if args.write:
        import uuid
        facts = read_facts(args.write)
        if not facts:
            print("ERROR: no facts provided (empty input)", file=sys.stderr)
            return 1

        session_id = args.session_id or str(uuid.uuid4())[:12]
        memory_file = resolve_memory_file(args.memory_file, args.memory_dir, args.topic_id)

        # TODO(PR6): add --source-kind / --source-ref / --source-locator flags
        #   so standalone --write calls can record structured audit provenance.

        # Pre-compute batch_n for dry-run display and auto-mark-done tracking.
        # The real write resolves batch_n atomically inside the locked transaction.
        batch_n = infer_next_batch_n(
            memory_file=memory_file,
            progress_file=pfile,
            topic_id=args.topic_id,
            explicit_batch=args.batch,
        )

        if args.dry_run:
            existing_content = memory_file.read_text(encoding="utf-8") if memory_file.exists() else ""
            existing_bullets = extract_existing_bullets(existing_content)
            conflicts = detect_conflicts(facts, existing_bullets)
            non_empty = sum(1 for f in facts if f.strip())
            print(f"[dry-run] --write would write to : {memory_file}")
            print(f"[dry-run]   session_id : {session_id}")
            print(f"[dry-run]   batch_n    : {batch_n}")
            print(f"[dry-run]   facts      : {non_empty}")
            print(f"[dry-run]   conflicts  : {len(conflicts)}")
            for idx, (new_fact, existing_line, conflict_batch) in conflicts.items():
                trimmed = new_fact[:60] + ("..." if len(new_fact) > 60 else "")
                print(f"[dry-run]   conflict[{idx}]: '{trimmed}' vs batch {conflict_batch}")
            print("[dry-run] L0 audit log : NOT written")
            print("[dry-run] Progress file: NOT updated")
            print("[dry-run] No files written.")
            return 0

        # Real write: batch_n=None → resolved atomically inside the locked transaction.
        written = write_batch_to_memory(
            memory_file=memory_file,
            topic_id=args.topic_id,
            batch_n=args.batch,  # None if not given → infer inside lock
            session_id=session_id,
            facts=facts,
            source_paths=None,  # A5: --write decoupled from session files
            progress_file=pfile,
        )

        if written > 0 and args.auto_mark_done:
            with locked_path(pfile):
                fresh_progress = load_progress(pfile, args.topic_id)
                if batch_n > fresh_progress.get("last_completed_batch", -1):
                    fresh_progress["last_completed_batch"] = batch_n
                    fresh_progress["updated"] = datetime.now(timezone.utc).strftime("%Y-%m-%d %H:%M UTC")
                    save_progress(pfile, fresh_progress)
                    print(f"Auto-marked batch {batch_n} as done (topic:{args.topic_id})")

        return 0

    if args.reset:
        if pfile.exists():
            pfile.unlink()
            print(f"Removed {pfile}")
        else:
            print(f"No v2 progress file found: {pfile}")
        return 0

    if args.mark_done is not None:
        with locked_path(pfile):
            progress = load_progress(pfile, args.topic_id)
            if args.mark_done > progress.get("last_completed_batch", -1):
                progress["last_completed_batch"] = args.mark_done
                progress["updated"] = datetime.now(timezone.utc).strftime("%Y-%m-%d %H:%M UTC")
                save_progress(pfile, progress)
                print(f"Marked batch {args.mark_done} as done (topic:{args.topic_id}, v2)")
            else:
                print(f"Batch {args.mark_done} was already marked done (topic:{args.topic_id}, v2)")
        return 0

    messages, raw_count, duplicate_count, paths = load_messages(args.topic_id, args.agents_base)
    deduped_count = len(messages)
    total_batches = (deduped_count + args.batch_size - 1) // args.batch_size

    progress = load_progress(pfile, args.topic_id)
    if progress.get("total_batches") != total_batches:
        progress["total_batches"] = total_batches
        # Do not write on --status/--total; keep these read-only.

    if args.total:
        print(
            f"topic:{args.topic_id} raw_msgs:{raw_count} deduped_msgs:{deduped_count} "
            f"duplicates:{duplicate_count} batch_size:{args.batch_size} total_batches:{total_batches}"
        )
        return 0

    if args.status:
        last = int(progress.get("last_completed_batch", -1))
        done = max(0, last + 1)
        pct = round(done / total_batches * 100) if total_batches else 0
        print(f"Archive progress v2  [topic:{args.topic_id}]")
        print(f"  Batches done  : {done}/{total_batches}  ({pct}%)")
        print(f"  Raw messages  : {raw_count}")
        print(f"  Deduped msgs   : {deduped_count}")
        print(f"  Duplicates     : {duplicate_count}")
        print(f"  Next batch    : {last + 1}" if done < total_batches else "  Status        : COMPLETE")
        print(f"  Progress file : {pfile}")
        if progress.get("updated"):
            print(f"  Last updated  : {progress['updated']}")
        return 0

    batch_n = args.batch if args.batch is not None else int(progress.get("last_completed_batch", -1)) + 1
    if batch_n >= total_batches:
        print(f"COMPLETE — all {total_batches} batches processed (topic:{args.topic_id}, v2)")
        return 0

    start = batch_n * args.batch_size
    end = min(start + args.batch_size, deduped_count)
    batch = messages[start:end]

    print_stats(args.topic_id, paths, raw_count, deduped_count, duplicate_count, args.batch_size, total_batches)
    print(f"BATCH {batch_n}/{total_batches - 1}  [topic:{args.topic_id}]")
    print(f"  Messages : {start}–{end - 1}  ({len(batch)} total, deduped index)")
    print(f"  From     : {ts_to_str(batch[0].ts_ms) if batch else '?'}")
    print(f"  To       : {ts_to_str(batch[-1].ts_ms) if batch else '?'}")
    print(f"  Progress : {max(0, int(progress.get('last_completed_batch', -1)) + 1)}/{total_batches} done before this batch")
    print("=" * 60)

    for msg in batch:
        print()
        print(f"[{msg.role.upper()} {ts_to_str(msg.ts_ms)}]")
        print(f"dedupe_key: {msg.dedupe_key}")
        print(f"source: {os.path.basename(msg.path)} seq:{msg.seq}")
        print(message_preview(msg.text, args.max_text))

    print()
    print("=" * 60)
    print(f"END BATCH {batch_n}/{total_batches - 1}  [topic:{args.topic_id}]")
    print(f"To mark done after archive write: python3 {Path(__file__).as_posix()} {args.topic_id} --mark-done {batch_n}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
