#!/usr/bin/env python3
"""
initial-index.py — Automatic initial indexer for OpenClaw agent session history.

Scans JSONL session files, detects sensitive data patterns (without storing values),
clusters messages by topic, and writes tiered memory artifacts to .agent/memory/index/.

Usage:
    python initial-index.py --topic <topic_id> [--agents-base DIR] [--output-dir DIR]
                            [--window-size N] [--dry-run] [--tier {A,B,C,all}]
"""

import argparse
import importlib.util
import json
import os
import re
import sys
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

# ---------------------------------------------------------------------------
# Sensitive data patterns — values are NEVER stored, only category + location
# ---------------------------------------------------------------------------
SENSITIVE_PATTERNS: list[tuple[re.Pattern, str]] = [
    (re.compile(r"(?i)(password|passwd|pwd)\s*[=:]\s*\S+"), "credential"),
    (re.compile(r"(?i)(api[_-]?key|apikey|api[_-]?secret)\s*[=:]\s*\S+"), "api_key"),
    (re.compile(r"(?i)(token|auth[_-]?token|bearer)\s*[=:]\s*\S+"), "token"),
    (re.compile(r"(?i)(secret|private[_-]?key)\s*[=:]\s*\S+"), "secret"),
    (re.compile(r"\b[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}\b"), "email"),
    (re.compile(r"\b(?:\d{1,3}\.){3}\d{1,3}\b"), "ip_address"),
    (re.compile(r"(?i)ssh-(?:rsa|ed25519|ecdsa)\s+[A-Za-z0-9+/=]{20,}"), "ssh_key"),
    (re.compile(r"-----BEGIN [A-Z ]+KEY-----"), "pem_key"),
    (re.compile(r"(?i)(access[_-]?key[_-]?id|aws[_-]?access)\s*[=:]\s*\S+"), "aws_credential"),
    (re.compile(r"\b[0-9a-fA-F]{32,64}\b"), "hash_or_token"),
]

# ---------------------------------------------------------------------------
# Cluster keywords — 7 cluster types, 3 HIGH-RISK
# ---------------------------------------------------------------------------
CLUSTER_KEYWORDS: dict[str, list[str]] = {
    "architecture_decision": [
        "architecture", "design decision", "design pattern", "refactor",
        "we decided", "chose to", "going with", "will use", "migration",
        "trade-off", "tradeoff", "approach", "strategy",
    ],
    "security_access": [
        "permission", "access control", "auth", "authentication", "authorization",
        "role", "secret", "credential", "token", "cert", "certificate",
        "firewall", "acl", "iam", "policy",
    ],
    "process_rule": [
        "always", "never", "rule:", "convention:", "standard:", "must ",
        "should not", "forbidden", "required", "guideline", "protocol",
        "workflow", "process:", "procedure",
    ],
    "bug_fix": [
        "bug", "fix", "error", "exception", "crash", "traceback", "issue",
        "broken", "regression", "patch", "hotfix", "workaround",
    ],
    "feature_work": [
        "feature", "implement", "add support", "new endpoint", "new function",
        "new class", "new module", "new file", "new script", "adding",
    ],
    "configuration": [
        "config", "setting", "env ", "environment variable", ".env",
        "yaml", "json config", "ini ", "toml", "setup", "install",
    ],
    "data_schema": [
        "schema", "model", "field", "column", "table", "index", "migration",
        "dataclass", "pydantic", "serializer", "type hint", "struct",
    ],
}

HIGH_RISK_CLUSTERS = {"architecture_decision", "security_access", "process_rule"}

# Keywords that suggest a Tier B operational fact (low-risk, auto-promotable)
OPERATIONAL_KEYWORDS = [
    "the project is", "we use", "we are using", "our stack", "repo is",
    "located at", "stored in", "the path is", "the directory is",
    "port ", "host ", "runs on", "deployed on", "version ", "branch ",
]


# ---------------------------------------------------------------------------
# Importlib loader — same pattern as manage-candidates.py
# ---------------------------------------------------------------------------
def _import_archive_batch(script_dir: Path):
    """Import archive-batch-v2.py via importlib."""
    ab_path = script_dir / "archive-batch-v2.py"
    if not ab_path.exists():
        raise FileNotFoundError(f"archive-batch-v2.py not found at {ab_path}")
    spec = importlib.util.spec_from_file_location("archive_batch", ab_path)
    mod = importlib.util.module_from_spec(spec)
    sys.modules["archive_batch"] = mod  # register before exec_module
    spec.loader.exec_module(mod)
    return mod


# ---------------------------------------------------------------------------
# Detection helpers
# ---------------------------------------------------------------------------
def detect_sensitive(text: str, window_ref: str) -> list[dict]:
    """
    Scan text for sensitive data patterns.
    Returns list of {category, window_ref, count} — values are NEVER stored.
    """
    findings: dict[str, int] = {}
    for pattern, category in SENSITIVE_PATTERNS:
        matches = pattern.findall(text)
        if matches:
            findings[category] = findings.get(category, 0) + len(matches)
    return [
        {"category": cat, "window_ref": window_ref, "count": cnt}
        for cat, cnt in findings.items()
    ]


def detect_clusters(text: str) -> list[str]:
    """Return list of matching cluster type names for the given text."""
    text_lower = text.lower()
    matched = []
    for cluster_type, keywords in CLUSTER_KEYWORDS.items():
        for kw in keywords:
            if kw in text_lower:
                matched.append(cluster_type)
                break
    return matched


def is_operational_fact(text: str) -> bool:
    """Return True if text contains Tier B operational fact keywords."""
    text_lower = text.lower()
    return any(kw in text_lower for kw in OPERATIONAL_KEYWORDS)


# ---------------------------------------------------------------------------
# Windowing
# ---------------------------------------------------------------------------
def make_windows(messages: list, window_size: int) -> list[list]:
    """Split messages list into windows of at most window_size."""
    if not messages:
        return []
    windows = []
    for i in range(0, len(messages), window_size):
        windows.append(messages[i : i + window_size])
    return windows


def summarize_window(window: list, window_idx: int) -> dict:
    """
    Build a per-window summary dict.
    Includes message count, role distribution, cluster hits, sensitive flags,
    and whether any message looks like an operational fact.
    Values from message text are NOT included verbatim.
    """
    roles: dict[str, int] = {}
    all_clusters: dict[str, int] = {}
    sensitive_flags: list[dict] = []
    has_operational = False

    window_ref = f"w{window_idx:04d}"

    for msg in window:
        role = getattr(msg, "role", "unknown")
        roles[role] = roles.get(role, 0) + 1

        text = getattr(msg, "text", "") or ""
        if not text:
            continue

        # Cluster detection
        for cluster in detect_clusters(text):
            all_clusters[cluster] = all_clusters.get(cluster, 0) + 1

        # Sensitive detection
        for finding in detect_sensitive(text, window_ref):
            sensitive_flags.append(finding)

        # Operational fact
        if not has_operational and is_operational_fact(text):
            has_operational = True

    ts_start = getattr(window[0], "ts_ms", None) if window else None
    ts_end = getattr(window[-1], "ts_ms", None) if window else None

    return {
        "window_idx": window_idx,
        "window_ref": window_ref,
        "message_count": len(window),
        "roles": roles,
        "clusters": all_clusters,
        "high_risk_clusters": [c for c in all_clusters if c in HIGH_RISK_CLUSTERS],
        "sensitive_flags": sensitive_flags,
        "has_operational_fact": has_operational,
        "ts_start_ms": ts_start,
        "ts_end_ms": ts_end,
    }


# ---------------------------------------------------------------------------
# Artifact writers
# ---------------------------------------------------------------------------
def write_index_meta(output_dir: Path, topic_id: str, summaries: list[dict],
                     window_size: int, dry_run: bool) -> Path:
    """Write index_meta.json — top-level index statistics."""
    total_msgs = sum(s["message_count"] for s in summaries)
    all_clusters: dict[str, int] = {}
    for s in summaries:
        for c, n in s["clusters"].items():
            all_clusters[c] = all_clusters.get(c, 0) + n

    high_risk = [c for c in all_clusters if c in HIGH_RISK_CLUSTERS]
    sensitive_count = sum(
        len(s["sensitive_flags"]) for s in summaries
    )

    meta = {
        "schema_version": "1.0",
        "topic_id": topic_id,
        "indexed_at": datetime.now(timezone.utc).isoformat(),
        "total_messages": total_msgs,
        "total_windows": len(summaries),
        "window_size": window_size,
        "cluster_summary": all_clusters,
        "high_risk_clusters_detected": high_risk,
        "sensitive_patterns_detected": sensitive_count,
        "has_sensitive_data": sensitive_count > 0,
    }
    path = output_dir / "index_meta.json"
    if not dry_run:
        path.write_text(json.dumps(meta, indent=2))
    return path


def write_timeline(output_dir: Path, summaries: list[dict], dry_run: bool) -> Path:
    """Write timeline.json — per-window timeline."""
    timeline = [
        {
            "window_ref": s["window_ref"],
            "window_idx": s["window_idx"],
            "message_count": s["message_count"],
            "ts_start_ms": s["ts_start_ms"],
            "ts_end_ms": s["ts_end_ms"],
            "roles": s["roles"],
            "clusters": list(s["clusters"].keys()),
            "high_risk": s["high_risk_clusters"],
        }
        for s in summaries
    ]
    path = output_dir / "timeline.json"
    if not dry_run:
        path.write_text(json.dumps(timeline, indent=2))
    return path


def write_cluster_map(output_dir: Path, summaries: list[dict], dry_run: bool) -> Path:
    """Write cluster_map.json — maps cluster types to window refs."""
    cluster_map: dict[str, list[str]] = {}
    for s in summaries:
        for cluster in s["clusters"]:
            cluster_map.setdefault(cluster, []).append(s["window_ref"])
    path = output_dir / "cluster_map.json"
    if not dry_run:
        path.write_text(json.dumps(cluster_map, indent=2))
    return path


def write_sensitive_map(output_dir: Path, summaries: list[dict], dry_run: bool) -> Path:
    """
    Write sensitive_map.json — categories + window refs only.
    Values are NEVER written. Only category + location.
    """
    sensitive_map: dict[str, list[str]] = {}
    for s in summaries:
        for flag in s["sensitive_flags"]:
            cat = flag["category"]
            sensitive_map.setdefault(cat, []).append(flag["window_ref"])
    path = output_dir / "sensitive_map.json"
    if not dry_run:
        path.write_text(json.dumps(sensitive_map, indent=2))
    return path


def write_recovery_index(output_dir: Path, topic_id: str, summaries: list[dict],
                          dry_run: bool) -> Path:
    """Write recovery_index.json — windows flagged for Tier B/C promotion."""
    tier_b_windows = [
        s["window_ref"]
        for s in summaries
        if s["has_operational_fact"] and not s["high_risk_clusters"]
    ]
    tier_c_windows = [
        s["window_ref"]
        for s in summaries
        if s["high_risk_clusters"]
    ]
    recovery = {
        "topic_id": topic_id,
        "tier_b_candidate_windows": tier_b_windows,
        "tier_c_candidate_windows": tier_c_windows,
        "tier_b_count": len(tier_b_windows),
        "tier_c_count": len(tier_c_windows),
        "note": (
            "Tier B windows contain operational facts with no high-risk clusters "
            "and may be auto-promoted. Tier C windows require human review."
        ),
    }
    path = output_dir / "recovery_index.json"
    if not dry_run:
        path.write_text(json.dumps(recovery, indent=2))
    return path


def write_window_file(output_dir: Path, summary: dict, dry_run: bool) -> Path:
    """Write per-window JSON file to windows/ subdirectory."""
    windows_dir = output_dir / "windows"
    if not dry_run:
        windows_dir.mkdir(parents=True, exist_ok=True)
    path = windows_dir / f"{summary['window_ref']}.json"
    if not dry_run:
        path.write_text(json.dumps(summary, indent=2))
    return path


# ---------------------------------------------------------------------------
# Main indexer
# ---------------------------------------------------------------------------
def run_index(
    topic_id: str,
    agents_base: Path,
    output_dir: Path,
    window_size: int = 200,
    dry_run: bool = False,
    tier_filter: str = "all",
    verbose: bool = False,
) -> dict[str, Any]:
    """
    Core indexer. Loads messages, windows them, runs detection, writes artifacts.
    Returns summary dict.
    """
    script_dir = Path(__file__).parent
    ab = _import_archive_batch(script_dir)

    # Resolve topic ID (supports prefix matching)
    resolved = ab.resolve_topic_id(topic_id, agents_base)
    if not resolved:
        raise ValueError(f"Topic ID not found: {topic_id!r}")
    topic_id = resolved

    if verbose:
        print(f"[initial-index] resolved topic: {topic_id}", file=sys.stderr)

    # Load messages
    messages, _raw_count, _dup_count, _paths = ab.load_messages(topic_id, agents_base)
    if verbose:
        print(f"[initial-index] loaded {len(messages)} messages", file=sys.stderr)

    # Build windows
    windows = make_windows(messages, window_size)
    if verbose:
        print(f"[initial-index] {len(windows)} windows (size={window_size})", file=sys.stderr)

    # Summarize each window
    summaries = [summarize_window(w, idx) for idx, w in enumerate(windows)]

    # Ensure output dir exists
    if not dry_run:
        output_dir.mkdir(parents=True, exist_ok=True)

    # Write artifacts
    written: list[Path] = []

    if tier_filter in ("A", "all"):
        written.append(write_index_meta(output_dir, topic_id, summaries, window_size, dry_run))
        written.append(write_timeline(output_dir, summaries, dry_run))
        written.append(write_cluster_map(output_dir, summaries, dry_run))
        written.append(write_sensitive_map(output_dir, summaries, dry_run))
        for s in summaries:
            written.append(write_window_file(output_dir, s, dry_run))

    if tier_filter in ("B", "C", "all"):
        written.append(write_recovery_index(output_dir, topic_id, summaries, dry_run))

    total_sensitive = sum(len(s["sensitive_flags"]) for s in summaries)
    tier_b = sum(
        1 for s in summaries if s["has_operational_fact"] and not s["high_risk_clusters"]
    )
    tier_c = sum(1 for s in summaries if s["high_risk_clusters"])

    result = {
        "topic_id": topic_id,
        "total_messages": len(messages),
        "total_windows": len(windows),
        "sensitive_detections": total_sensitive,
        "tier_b_windows": tier_b,
        "tier_c_windows": tier_c,
        "artifacts_written": [str(p) for p in written] if not dry_run else [],
        "dry_run": dry_run,
    }

    if verbose:
        print(f"[initial-index] done: {result}", file=sys.stderr)

    return result


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------
def main() -> None:
    parser = argparse.ArgumentParser(
        description="Automatic initial indexer for OpenClaw agent session history.",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Examples:
  # Index a topic, write artifacts to default location
  python initial-index.py --topic abc123

  # Dry-run (no files written)
  python initial-index.py --topic abc123 --dry-run

  # Custom agents base and output
  python initial-index.py --topic abc123 \\
      --agents-base ~/.openclaw/agents \\
      --output-dir /tmp/index-out

  # Index only Tier A artifacts (structural, no promotion data)
  python initial-index.py --topic abc123 --tier A
        """,
    )
    parser.add_argument(
        "--topic",
        required=True,
        metavar="TOPIC_ID",
        help="Topic ID (or unambiguous prefix) to index.",
    )
    parser.add_argument(
        "--agents-base",
        default=os.path.expanduser("~/.openclaw/agents"),
        metavar="DIR",
        help="Base directory for agent session files. Default: ~/.openclaw/agents",
    )
    parser.add_argument(
        "--output-dir",
        default=None,
        metavar="DIR",
        help=(
            "Explicit output directory for index artifacts. "
            "Overrides --project-root / --memory-dir."
        ),
    )
    parser.add_argument(
        "--project-root",
        default=None,
        metavar="DIR",
        help=(
            "Project root directory. Artifacts go to "
            "<project-root>/.agent/memory/index/topic-<id>/. "
            "Defaults to current working directory."
        ),
    )
    parser.add_argument(
        "--memory-dir",
        default=None,
        metavar="DIR",
        help=(
            "Explicit .agent/memory directory. Artifacts go to "
            "<memory-dir>/index/topic-<id>/. "
            "Overrides --project-root."
        ),
    )
    parser.add_argument(
        "--window-size",
        type=int,
        default=200,
        metavar="N",
        help="Number of messages per window. Default: 200",
    )
    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="Run detection and windowing without writing any files.",
    )
    parser.add_argument(
        "--tier",
        choices=["A", "B", "C", "all"],
        default="all",
        help=(
            "Which tier artifacts to write. "
            "A=index only, B/C=recovery index, all=everything. Default: all"
        ),
    )
    parser.add_argument(
        "--verbose",
        action="store_true",
        help="Print progress to stderr.",
    )

    args = parser.parse_args()

    agents_base = Path(args.agents_base)
    if not agents_base.exists():
        print(f"WARN: agents-base does not exist: {agents_base}", file=sys.stderr)

    # Determine output dir — project-local by default
    if args.output_dir:
        output_dir = Path(args.output_dir)
    elif getattr(args, "memory_dir", None):
        output_dir = Path(args.memory_dir) / "index" / f"topic-{args.topic}"
    elif getattr(args, "project_root", None):
        output_dir = Path(args.project_root) / ".agent" / "memory" / "index" / f"topic-{args.topic}"
    else:
        output_dir = Path.cwd() / ".agent" / "memory" / "index" / f"topic-{args.topic}"

    try:
        result = run_index(
            topic_id=args.topic,
            agents_base=agents_base,
            output_dir=output_dir,
            window_size=args.window_size,
            dry_run=args.dry_run,
            tier_filter=args.tier,
            verbose=args.verbose,
        )
    except (FileNotFoundError, ValueError) as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        sys.exit(1)

    # Output JSON result to stdout
    print(json.dumps(result, indent=2))


if __name__ == "__main__":
    main()
