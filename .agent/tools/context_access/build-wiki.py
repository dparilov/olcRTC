#!/usr/bin/env python3
"""
build-wiki.py — L3 Shared Knowledge Vault builder.

Reads all memory/topic-<id>.md files and generates a cross-referenced,
searchable wiki in memory/wiki/. Neutral — no agent-specific logic.

Usage:
  python3 build-wiki.py --memory-dir .agent/memory
  python3 build-wiki.py --memory-dir .agent/memory --clean
  python3 build-wiki.py --memory-dir .agent/memory --topic 7301

Output:
  memory/wiki/index.md          — master index with all topics + stats
  memory/wiki/topic-<id>.md     — per-topic wiki page (clean facts only)
  memory/wiki/by-type/          — facts grouped by type (decisions, constraints, etc.)
  memory/wiki/WIKI_META.json    — build metadata (last-built, topic count, fact count)

Environment variables:
  OPENCLAW_AGENTS   path to ~/.openclaw/agents/ (for topic name resolution)
"""
from __future__ import annotations

import argparse
import importlib.util
import json
import os
import re
import hashlib
import subprocess
import sys
from datetime import datetime, timezone
from pathlib import Path
from typing import TypedDict

from io_utils import atomic_write_text

DEFAULT_AGENTS_BASE = Path.home() / ".openclaw" / "agents"

WIKI_SCHEMA_VERSION = 2


# ---------------------------------------------------------------------------
# WikiFact — structured provenance record for a single memory fact
# ---------------------------------------------------------------------------

class WikiFact(TypedDict):
    id: str               # deterministic: wiki-fact-<topic_id>-b<batch_n|unknown>-l<line_number>
    text: str             # fact text, stripped of leading "- "
    topic_id: str
    source_file: str      # relative path to memory file
    line_number: int      # 1-based line number in source_file
    batch_n: int | None   # None when fact appears outside any batch heading
    batch_date: str | None  # YYYY-MM-DD from batch heading, or None
    session_id: str | None  # from "— session X" in batch heading, or None
    is_conflict: bool     # True when line starts with "- ⚠️"
    fact_type: str        # filled by classify_fact(); empty string until classified


# ---------------------------------------------------------------------------
# Fact parsing
# ---------------------------------------------------------------------------

_BATCH_RE = re.compile(
    r"^## \[(\d{4}-\d{2}-\d{2})\] Batch (\d+)"
    r"(?:\s+[—\-]+\s+session\s+(\S+))?"
)
_COMPACT_RE = re.compile(r"^## \[(\d{4}-\d{2}-\d{2})\] Batch -1")


def parse_facts(
    content: str,
    source_file: str = "",
    topic_id: str = "",
) -> list[WikiFact]:
    """Parse facts from topic-<id>.md content with full provenance metadata.

    Rules:
    - Only top-level bullet lines (starting with "- ") are facts.
      Lines with leading whitespace (nested bullets) are skipped.
    - Lines starting with "- ⚠️" are stored as is_conflict=True facts,
      not skipped.
    - Facts outside any batch heading get batch_n=None, batch_date=None,
      session_id=None.
    - line_number is 1-based.
    """
    facts: list[WikiFact] = []
    current_batch_n: int | None = None
    current_batch_date: str | None = None
    current_session_id: str | None = None

    for lineno, line in enumerate(content.splitlines(), start=1):
        # Check for batch heading
        bm = _BATCH_RE.match(line)
        if bm:
            current_batch_date = bm.group(1)
            current_batch_n = int(bm.group(2))
            current_session_id = bm.group(3) or None
            continue

        # Compact/promotion batch heading (Batch -1)
        if _COMPACT_RE.match(line):
            m = _COMPACT_RE.match(line)
            current_batch_date = m.group(1) if m else None
            current_batch_n = None
            current_session_id = None
            continue

        # Only top-level bullets (no leading whitespace)
        if not line.startswith("- "):
            continue

        is_conflict = line.startswith("- \u26a0\ufe0f")  # "- ⚠️"

        text = line[2:].strip()  # strip leading "- "
        if not text:
            continue

        batch_label = str(current_batch_n) if current_batch_n is not None else "unknown"
        fact_id = f"wiki-fact-{topic_id}-b{batch_label}-l{lineno}"

        facts.append(WikiFact(
            id=fact_id,
            text=text,
            topic_id=topic_id,
            source_file=source_file,
            line_number=lineno,
            batch_n=current_batch_n,
            batch_date=current_batch_date,
            session_id=current_session_id,
            is_conflict=is_conflict,
            fact_type="",  # filled by caller via classify_fact()
        ))

    return facts


def extract_facts(content: str, *, include_conflicts: bool = False) -> list[dict]:
    """Backward-compatible wrapper around parse_facts().

    Returns list of {text, batch_n, is_conflict} dicts as before.
    By default skips conflict facts (is_conflict=True) to preserve
    pre-D1 rendering behaviour. Pass include_conflicts=True to get all.
    """
    raw = parse_facts(content, source_file="", topic_id="")
    if not include_conflicts:
        raw = [r for r in raw if not r["is_conflict"]]
    return [
        {
            "text": f"- {r['text']}",  # restore "- " prefix for callers that expect it
            "batch_n": r["batch_n"],
            "is_conflict": r["is_conflict"],
        }
        for r in raw
    ]


# ---------------------------------------------------------------------------
# Fact classification
# ---------------------------------------------------------------------------

def classify_fact(text: str) -> str:
    """Classify fact into wiki section."""
    t = text.lower()
    if any(w in t for w in ("decided", "decision", "chose", "architecture", "design pattern")):
        return "decisions"
    if any(w in t for w in ("must", "never", "always", "required", "cannot", "constraint")):
        return "constraints"
    if any(w in t for w in ("rule", "policy", "process", "procedure", "workflow")):
        return "process"
    if any(w in t for w in ("shipped", "released", "fixed", "resolved", "deployed")):
        return "resolved"
    if any(w in t for w in ("prefers", "preference", "likes", "wants", "style")):
        return "preferences"
    if any(w in t for w in ("blocker", "blocked", "pending", "waiting", "issue", "bug")):
        return "blockers"
    return "general"


# ---------------------------------------------------------------------------
# Memory header parsing
# ---------------------------------------------------------------------------

def parse_memory_header(content: str) -> dict:
    """Extract metadata from <!-- last-batch: N | last-write: TS | ... --> header."""
    m = re.search(r"<!--\s*(.+?)\s*-->", content)
    if not m:
        return {}
    meta = {}
    for part in m.group(1).split("|"):
        kv = part.strip().split(":", 1)
        if len(kv) == 2:
            meta[kv[0].strip()] = kv[1].strip()
    return meta


def topic_name_from_file(path: Path) -> str:
    """Extract topic name from first heading in file."""
    for line in path.read_text(encoding="utf-8", errors="replace").splitlines():
        if line.startswith("# "):
            return line[2:].strip()
    return path.stem


# ---------------------------------------------------------------------------
# Git SHA helper
# ---------------------------------------------------------------------------

def _get_git_sha(repo_dir: Path | None = None) -> str | None:
    """Return short HEAD git SHA, or None if not in a git repo."""
    try:
        result = subprocess.run(
            ["git", "rev-parse", "--short", "HEAD"],
            capture_output=True,
            text=True,
            cwd=str(repo_dir) if repo_dir else None,
            timeout=5,
        )
        if result.returncode == 0:
            return result.stdout.strip() or None
    except Exception:
        pass
    return None


# ---------------------------------------------------------------------------
# Source path helper
# ---------------------------------------------------------------------------

def _display_source_path(path: "Path", memory_dir: "Path") -> str:
    """Return a portable relative path for storing in WIKI_META / WikiFact.

    Relative to memory_dir.parent so paths look like 'memory/topic-7301.md'.
    Falls back to str(path) when path is not under memory_dir.parent.
    """
    try:
        return str(path.relative_to(memory_dir.parent))
    except ValueError:
        return str(path)


# ---------------------------------------------------------------------------
# Source file metadata helpers
# ---------------------------------------------------------------------------

def _file_sha256(path: Path) -> str:
    """Return hex SHA-256 of file contents."""
    h = hashlib.sha256()
    with path.open("rb") as fh:
        for chunk in iter(lambda: fh.read(65536), b""): h.update(chunk)
    return h.hexdigest()


def _file_mtime_iso(path: Path) -> str:
    """Return ISO-8601 UTC mtime of path."""
    mtime = path.stat().st_mtime
    return datetime.fromtimestamp(mtime, tz=timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


# ---------------------------------------------------------------------------
# Provenance rendering
# ---------------------------------------------------------------------------

def format_provenance(fact: WikiFact) -> str:
    """Return an italic markdown provenance tail for a rendered wiki fact.

    Format (2-space indent so it nests under the bullet):
      _Source: `<source_file>` · topic `<topic_id>` · Batch <N|unknown>
               · <batch_date> · session `<session_id>` · line <N> [· conflict]_
    """
    parts: list[str] = [
        f"`{fact['source_file']}`",
        f"topic `{fact['topic_id']}`",
    ]
    if fact["batch_n"] is not None:
        parts.append(f"Batch {fact['batch_n']}")
    else:
        parts.append("Batch unknown")
    if fact["batch_date"]:
        parts.append(fact["batch_date"])
    if fact["session_id"]:
        parts.append(f"session `{fact['session_id']}`")
    parts.append(f"line {fact['line_number']}")
    if fact["is_conflict"]:
        parts.append("conflict")
    return "  _Source: " + " · ".join(parts) + "_"


def render_fact_bullet(fact: WikiFact) -> str:
    """Return a markdown bullet line for a fact, without provenance tail.

    Avoids double-prefixing ⚠️ when parse_facts() already stored the
    conflict marker inside fact["text"].
    """
    text = fact["text"]
    if fact["is_conflict"]:
        if text.startswith("⚠️"):
            return f"- {text}"
        return f"- ⚠️ {text}"
    return f"- {text}"


# ---------------------------------------------------------------------------
# Wiki page builders
# ---------------------------------------------------------------------------

def build_topic_page(
    topic_id: str,
    memory_file: Path,
    wiki_dir: Path,
    dry_run: bool = False,
    wiki_facts: "list[WikiFact] | None" = None,
) -> dict:
    """Build wiki/topic-<id>.md from memory file with provenance. Returns stats.

    If wiki_facts is provided (pre-computed by main()), uses them directly.
    Otherwise parses the file internally (standalone / test use).
    """
    content = memory_file.read_text(encoding="utf-8", errors="replace")
    header = parse_memory_header(content)
    title = topic_name_from_file(memory_file)

    if wiki_facts is None:
        wf_local = parse_facts(content, source_file=memory_file.name, topic_id=topic_id)
        for wf in wf_local:
            wf["fact_type"] = classify_fact(wf["text"])
    else:
        wf_local = wiki_facts

    regular = [f for f in wf_local if not f["is_conflict"]]
    conflicts = [f for f in wf_local if f["is_conflict"]]

    now_str = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    last_write = header.get("last-write", "unknown")

    conflict_note = f" | Conflicts: {len(conflicts)}" if conflicts else ""
    lines = [
        f"# {title}",
        "",
        f"> Topic ID: `{topic_id}` | Last memory write: `{last_write}` | Facts: {len(regular)}{conflict_note}",
        f"> Wiki built: `{now_str}`",
        "",
    ]

    # Group regular facts by type
    section_order = ["decisions", "constraints", "process", "preferences", "resolved", "blockers", "general"]
    section_titles = {
        "decisions": "## Decisions & Architecture",
        "constraints": "## Constraints",
        "process": "## Process & Rules",
        "preferences": "## Preferences",
        "resolved": "## Resolved Issues",
        "blockers": "## Blockers",
        "general": "## General Facts",
    }
    by_type: dict[str, list[WikiFact]] = {}
    for f in regular:
        by_type.setdefault(f["fact_type"], []).append(f)

    for section in section_order:
        if section in by_type:
            lines.append(section_titles[section])
            lines.append("")
            for f in by_type[section]:
                lines.append(render_fact_bullet(f))
                lines.append(format_provenance(f))
                lines.append("")
            lines.append("")

    # Conflicts section — always rendered visibly in D2
    if conflicts:
        lines.append("## ⚠️ Conflicts")
        lines.append("")
        for f in conflicts:
            lines.append(render_fact_bullet(f))
            lines.append(format_provenance(f))
            lines.append("")
        lines.append("")

    wiki_file = wiki_dir / f"topic-{topic_id}.md"
    if not dry_run:
        atomic_write_text(wiki_file, "\n".join(lines))

    return {
        "topic_id": topic_id,
        "title": title,
        "fact_count": len(regular),
        "conflict_count": len(conflicts),
        "last_write": last_write,
        "wiki_file": str(wiki_file),
        "by_type": {k: len(v) for k, v in by_type.items()},
    }


def build_by_type_pages(
    all_facts_by_type: "dict[str, list[WikiFact]]",
    wiki_dir: Path,
    dry_run: bool = False,
) -> None:
    """Build wiki/by-type/<type>.md pages aggregating WikiFacts across all topics.

    Each rendered fact includes a provenance tail (source, topic, batch, date, session).
    """
    type_dir = wiki_dir / "by-type"
    if not dry_run:
        type_dir.mkdir(exist_ok=True)

    section_titles = {
        "decisions": "Architecture Decisions",
        "constraints": "Constraints",
        "process": "Process & Rules",
        "preferences": "Preferences",
        "resolved": "Resolved Issues",
        "blockers": "Blockers",
        "general": "General Facts",
    }

    for fact_type, entries in all_facts_by_type.items():
        title = section_titles.get(fact_type, fact_type.title())
        topic_count = len({e["topic_id"] for e in entries})
        lines = [
            f"# {title}",
            "",
            f"> {len(entries)} facts across {topic_count} topic(s)",
            "",
        ]

        # Group by topic
        by_topic: dict[str, list[WikiFact]] = {}
        for e in entries:
            by_topic.setdefault(e["topic_id"], []).append(e)

        for tid, tfacts in sorted(by_topic.items()):
            lines.append(f"## Topic {tid}")
            lines.append("")
            for f in tfacts:
                lines.append(render_fact_bullet(f))
                lines.append(format_provenance(f))
                lines.append("")
            lines.append("")

        if not dry_run:
            atomic_write_text(type_dir / f"{fact_type}.md", "\n".join(lines))


def build_index(topic_stats: list[dict], wiki_dir: Path, dry_run: bool = False) -> None:
    """Build wiki/index.md — master index."""
    now_str = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    total_facts = sum(s["fact_count"] for s in topic_stats)

    lines = [
        "# Knowledge Vault — Index",
        "",
        f"> Built: `{now_str}` | Topics: {len(topic_stats)} | Total facts: {total_facts}",
        "",
        "## Topics",
        "",
        "| Topic ID | Title | Facts | Last Write | Wiki Page |",
        "|----------|-------|-------|------------|-----------|",
    ]

    for s in sorted(topic_stats, key=lambda x: x["topic_id"]):
        wiki_link = f"[view](topic-{s['topic_id']}.md)"
        lines.append(
            f"| `{s['topic_id']}` | {s['title']} | {s['fact_count']} | `{s['last_write']}` | {wiki_link} |"
        )

    lines += [
        "",
        "## By Type",
        "",
    ]

    type_dir = wiki_dir / "by-type"
    if type_dir.exists():
        for tf in sorted(type_dir.glob("*.md")):
            lines.append(f"- [{tf.stem.replace('-', ' ').title()}](by-type/{tf.name})")
    lines.append("")

    if not dry_run:
        atomic_write_text(wiki_dir / "index.md", "\n".join(lines))


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def main() -> int:
    parser = argparse.ArgumentParser(
        description="L3 Knowledge Vault builder — generates cross-referenced wiki from memory files."
    )
    parser.add_argument("--memory-dir", type=Path, default=Path(".agent/memory"),
                        help="Memory directory (default: .agent/memory)")
    parser.add_argument("--topic", default=None,
                        help="Build wiki for single topic only (numeric ID or name)")
    parser.add_argument("--clean", action="store_true",
                        help="Remove wiki/ directory before rebuilding")
    parser.add_argument("--dry-run", action="store_true",
                        help="Show what would be built without writing any files. "
                             "Prints per-topic fact counts and file paths. Safe to run repeatedly.")
    parser.add_argument("--agents-base", type=Path, default=DEFAULT_AGENTS_BASE)
    args = parser.parse_args()

    memory_dir = args.memory_dir
    if not memory_dir.exists():
        raise SystemExit(f"ERROR: memory directory not found: {memory_dir}")

    wiki_dir = memory_dir / "wiki"

    if args.clean and wiki_dir.exists():
        if args.dry_run:
            print(f"[dry-run] --clean would remove: {wiki_dir}")
        else:
            import shutil
            shutil.rmtree(wiki_dir)
            print(f"Cleaned {wiki_dir}")

    if not args.dry_run:
        wiki_dir.mkdir(parents=True, exist_ok=True)

    # Find memory files
    if args.topic:
        # Resolve topic name if needed
        ab_script = Path(__file__).parent / "archive-batch-v2.py"
        if ab_script.exists():
            spec = importlib.util.spec_from_file_location("archive_batch_v2", ab_script)
            mod = importlib.util.module_from_spec(spec)
            sys.modules["archive_batch_v2"] = mod
            spec.loader.exec_module(mod)
            topic_id = mod.resolve_topic_id(args.topic, args.agents_base)
        else:
            topic_id = args.topic
        memory_files = [memory_dir / f"topic-{topic_id}.md"]
        memory_files = [f for f in memory_files if f.exists()]
    else:
        memory_files = sorted(memory_dir.glob("topic-*.md"))

    if not memory_files:
        print("No memory files found. Run archive-batch-v2.py --write first.")
        return 0

    print(f"Building wiki from {len(memory_files)} memory file(s)...")

    topic_stats = []
    all_facts_by_type: dict[str, list[dict]] = {}
    all_wiki_facts: list[WikiFact] = []
    per_topic_last_batch: dict[str, int | None] = {}
    source_files_index: list[dict] = []

    git_sha = _get_git_sha(repo_dir=Path(__file__).parent)

    for mf in memory_files:
        # Extract topic ID from filename (numeric or non-numeric)
        m = re.match(r"topic-(.+)\.md", mf.name)
        if not m:
            continue
        tid = m.group(1)

        try:
            content = mf.read_text(encoding="utf-8", errors="replace")
            header = parse_memory_header(content)

            # Parse with full provenance
            source_path = _display_source_path(mf, memory_dir)
            wiki_facts = parse_facts(
                content,
                source_file=source_path,
                topic_id=tid,
            )
            # Classify and attach fact_type
            for wf in wiki_facts:
                wf["fact_type"] = classify_fact(wf["text"])

            all_wiki_facts.extend(wiki_facts)

            # Determine last batch for this topic
            batch_nums = [wf["batch_n"] for wf in wiki_facts if wf["batch_n"] is not None]
            last_batch = max(batch_nums) if batch_nums else None
            per_topic_last_batch[tid] = last_batch

            # Build source files index entry
            source_files_index.append({
                "path": source_path,
                "topic_id": tid,
                "fact_count": len(wiki_facts),
                "last_batch": last_batch,
                "sha256": _file_sha256(mf),
                "mtime": _file_mtime_iso(mf),
            })

            stats = build_topic_page(tid, mf, wiki_dir, dry_run=args.dry_run, wiki_facts=wiki_facts)
            topic_stats.append(stats)

            # Collect full WikiFacts for by-type provenance rendering
            for wf in wiki_facts:
                ft = wf["fact_type"]
                all_facts_by_type.setdefault(ft, []).append(wf)

            prefix = "[dry-run] " if args.dry_run else ""
            n_conflicts = stats.get("conflict_count", 0)
            conflict_note = f" ({n_conflicts} conflicts)" if n_conflicts else ""
            print(f"  {prefix}topic-{tid}: {stats['fact_count']} facts{conflict_note} → {stats['wiki_file']}")
        except Exception as e:
            print(f"  ERROR topic-{tid}: {e}", file=sys.stderr)

    build_by_type_pages(all_facts_by_type, wiki_dir, dry_run=args.dry_run)
    build_index(topic_stats, wiki_dir, dry_run=args.dry_run)

    total_facts = sum(s["fact_count"] for s in topic_stats)
    conflict_facts = sum(1 for wf in all_wiki_facts if wf["is_conflict"])

    if args.dry_run:
        print(f"\n[dry-run] Would build wiki:")
        print(f"[dry-run]   Topics:         {len(topic_stats)}")
        print(f"[dry-run]   Total facts:    {total_facts}")
        print(f"[dry-run]   Conflict facts: {conflict_facts}")
        print(f"[dry-run]   Index:          {wiki_dir / 'index.md'}")
        print(f"[dry-run]   By-type:        {wiki_dir / 'by-type/'}")
        print(f"[dry-run]   WIKI_META:      {wiki_dir / 'WIKI_META.json'}")
        print("[dry-run] No files written.")
        return 0

    # Write WIKI_META.json (schema v2)
    meta = {
        "wiki_schema_version": WIKI_SCHEMA_VERSION,
        "built_at": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "build_git_sha": git_sha,
        "topic_count": len(topic_stats),
        "total_facts": total_facts,
        "conflict_facts": conflict_facts,
        "source_files": source_files_index,
        "per_topic_last_batch": per_topic_last_batch,
        "topics": topic_stats,
        "facts": [dict(wf) for wf in all_wiki_facts],
    }
    atomic_write_text(wiki_dir / "WIKI_META.json", json.dumps(meta, ensure_ascii=False, indent=2))

    print(f"\nWiki built:")
    print(f"  Topics:         {len(topic_stats)}")
    print(f"  Total facts:    {total_facts}")
    print(f"  Conflict facts: {conflict_facts}")
    print(f"  Index:          {wiki_dir / 'index.md'}")
    print(f"  By-type:        {wiki_dir / 'by-type/'}")

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
