#!/usr/bin/env python3
"""
manage-candidates.py — L1 Candidate Knowledge manager.

Manages the intermediate candidate layer between raw fact extraction (L1) and
working memory (L2). Each candidate is a YAML entry with a status lifecycle.

Schema version: 1 (see docs/CANDIDATE_SCHEMA.md)

Candidate statuses:
  candidate        → freshly extracted, not yet reviewed
  auto-promoted    → meets auto-promotion criteria, written to L2
  needs-approval   → high-risk type, requires human approval before promotion
  approved         → manually approved, written to L2
  rejected         → explicitly discarded
  obsolete         → superseded by a newer candidate on the same topic
  duplicate        → semantically equivalent to an existing L2 fact

Usage:
  python3 manage-candidates.py <topic_id|topic_name> --add <facts-file> \\
      [--source-kind session_history] [--source-ref "batch 12"] \\
      [--locator "msg 42"] [--confidence medium] [--risk low] \\
      [--project myproject] [--summary "why this matters"]
  python3 manage-candidates.py <topic_id|topic_name> --list
  python3 manage-candidates.py <topic_id|topic_name> --promote-auto [--dry-run]
  python3 manage-candidates.py <topic_id|topic_name> --approve <candidate-id>
  python3 manage-candidates.py <topic_id|topic_name> --reject <candidate-id> [--reason "text"]
  python3 manage-candidates.py <topic_id|topic_name> --show <candidate-id>
  python3 manage-candidates.py <topic_id|topic_name> --status
  python3 manage-candidates.py <topic_id|topic_name> --validate

Environment variables:
  OPENCLAW_AGENTS   path to ~/.openclaw/agents/
  OPENCLAW_MEMORY   path to .agent/memory/ (default: .agent/memory relative to cwd)
"""
from __future__ import annotations

import argparse
import importlib.util
import os
import re
import sys
import uuid
from datetime import datetime, timezone
from pathlib import Path

from io_utils import atomic_write_text, locked_path, write_text_in_lock

# ---------------------------------------------------------------------------
# PyYAML is a hard requirement — no fallback (Fix A2)
# ---------------------------------------------------------------------------
try:
    import yaml
except ImportError:
    print(
        "ERROR: PyYAML is required but not installed.\n"
        "  pip install 'pyyaml>=6.0'\n"
        "  or: pip install -r requirements.txt",
        file=sys.stderr,
    )
    sys.exit(1)

SCHEMA_VERSION = 1

DEFAULT_AGENTS_BASE = Path.home() / ".openclaw" / "agents"

# ---------------------------------------------------------------------------
# Type sets
# ---------------------------------------------------------------------------

HIGH_RISK_TYPES = {
    "architecture_decision",
    "process_rule",
    "constraint",
    "agent_policy",
    "security_note",
    "rejected_approach",
}

AUTO_PROMOTE_TYPES = {
    "fact",
    "person",
    "project_state",
    "preference",
    "resolved_issue",
}

VALID_TYPES = HIGH_RISK_TYPES | AUTO_PROMOTE_TYPES

VALID_CONFIDENCE = {"low", "medium", "high"}
VALID_RISK = {"low", "medium", "high"}
VALID_STATUS = {
    "candidate", "auto-promoted", "needs-approval",
    "approved", "rejected", "duplicate", "obsolete",
}
TERMINAL_STATUS = {"auto-promoted", "approved", "rejected", "duplicate", "obsolete"}

# Fix 3 — canonical set used by argparse choices AND schema validation
VALID_EVIDENCE_KINDS = {
    "session_history",
    "repo_doc",
    "pr",
    "review",
    "pyrogram",
    "memory_md",
    "manual",
    "candidate",
}

# ---------------------------------------------------------------------------
# High-risk keyword scan — blocks auto-promotion regardless of type (Fix A1)
# ---------------------------------------------------------------------------

HIGH_RISK_KEYWORDS = {
    "architecture", "canonical", "source of truth",
    "deprecated", "deprecate", "suspended",
    "security", "secret", "credential", "token", "key",
    "billing", "deployment", "production",
    "permission", "agent policy",
    "merge", "release", "auto-merge",
    "human approval",
    "password", "private", "auth",
    "delete", "drop", "destroy", "wipe", "truncate",
    "prod", "live",
    "gdpr", "pii", "personal data", "compliance", "legal",
}

_KW_PATTERN = re.compile(
    r"\b(" + "|".join(re.escape(kw) for kw in HIGH_RISK_KEYWORDS) + r")\b",
    re.IGNORECASE,
)


def contains_high_risk_keyword(text: str) -> str | None:
    """Return the first matched keyword, or None."""
    m = _KW_PATTERN.search(text)
    return m.group(0) if m else None


# ---------------------------------------------------------------------------
# Schema v1 helpers
# ---------------------------------------------------------------------------

def candidate_id() -> str:
    today = datetime.now(timezone.utc).strftime("%Y%m%d")
    return f"CAND-{today}-{str(uuid.uuid4())[:8].upper()}"


def candidates_file(memory_dir: Path, topic_id: str) -> Path:
    cdir = memory_dir / "candidates"
    cdir.mkdir(parents=True, exist_ok=True)
    return cdir / f"topic-{topic_id}-candidates.yaml"


def load_candidates(path: Path) -> list[dict]:
    if not path.exists():
        return []
    text = path.read_text(encoding="utf-8")
    return yaml.safe_load(text) or []


def save_candidates(path: Path, candidates: list[dict]) -> None:
    """Write candidates. Caller MUST hold locked_path(path)."""
    write_text_in_lock(
        path,
        yaml.dump(candidates, allow_unicode=True, sort_keys=False),
    )


def now_iso() -> str:
    return datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def make_evidence_entry(
    kind: str,
    ref: str,
    locator: str,
    topic_id: str | None = None,
) -> dict:
    if kind not in VALID_EVIDENCE_KINDS:
        raise ValueError(f"Unknown evidence kind {kind!r}. Valid: {sorted(VALID_EVIDENCE_KINDS)}")
    if not ref or not ref.strip():
        raise ValueError("evidence ref must be a non-empty string (e.g. 'batch 12' or file path)")
    if not locator or not locator.strip():
        raise ValueError("evidence locator must be a non-empty string (e.g. 'batch:12:msg:47' or 'line:10')")
    entry: dict = {
        "kind": kind,
        "ref": ref.strip(),
        "locator": locator.strip(),
        "observed_at": now_iso(),
    }
    if topic_id:
        entry["topic_id"] = topic_id
    return entry


def validate_evidence_entry(ev: dict) -> list[str]:
    """Return list of errors for a single evidence entry."""
    errs: list[str] = []
    if not isinstance(ev, dict):
        return ["evidence entry must be a dict"]
    kind = ev.get("kind", "")
    if not kind:
        errs.append("evidence.kind is required")
    elif kind not in VALID_EVIDENCE_KINDS:
        errs.append(f"evidence.kind {kind!r} is not a valid kind; must be one of {sorted(VALID_EVIDENCE_KINDS)}")
    if not ev.get("ref", "").strip():
        errs.append("evidence.ref must be non-empty (human-readable source reference)")
    if not ev.get("locator", "").strip():
        errs.append("evidence.locator must be non-empty (machine-usable position reference)")
    if not ev.get("observed_at", ""):
        errs.append("evidence.observed_at is required")
    return errs


def classify_type(fact: str) -> str:
    """Heuristic type classification from fact text."""
    f = fact.lower()
    if any(w in f for w in ("decided", "decision", "chose", "architecture", "design")):
        return "architecture_decision"
    if any(w in f for w in ("must", "never", "always", "required", "cannot", "constraint")):
        return "constraint"
    if any(w in f for w in ("rule", "policy", "process", "procedure")):
        return "process_rule"
    if any(w in f for w in ("prefers", "preference", "likes", "wants")):
        return "preference"
    if any(w in f for w in ("fixed", "resolved", "solved", "closed", "shipped", "released")):
        return "resolved_issue"
    return "fact"


def derive_classification(fact_type: str, confidence: str, risk: str, claim: str) -> dict:
    """Compute classification block from field values.

    Auto-promotion requires ALL of: type in AUTO_PROMOTE_TYPES, risk=low,
    confidence in {medium, high}, no high-risk keyword in claim.
    risk=medium is NOT auto-promotable (per docs/CANDIDATE_SCHEMA.md).
    """
    kw = contains_high_risk_keyword(claim)
    if fact_type in HIGH_RISK_TYPES:
        return {
            "auto_promotable": False,
            "needs_human_approval": True,
            "reason": f"Type '{fact_type}' always requires human approval.",
        }
    # Fix 1: only risk=low is auto-promotable; medium and high both require review
    if risk != "low":
        return {
            "auto_promotable": False,
            "needs_human_approval": True,
            "reason": f"Risk is '{risk}'; only risk=low qualifies for auto-promotion.",
        }
    if confidence == "low":
        return {
            "auto_promotable": False,
            "needs_human_approval": True,
            "reason": "Confidence is low; manual review required.",
        }
    if kw:
        return {
            "auto_promotable": False,
            "needs_human_approval": True,
            "reason": f"Claim contains high-risk keyword: '{kw}'.",
        }
    return {
        "auto_promotable": True,
        "needs_human_approval": False,
        "reason": f"Type '{fact_type}', risk={risk}, confidence={confidence}.",
    }


def build_candidate_v1(
    *,
    claim: str,
    topic_id: str,
    created_by: str,
    evidence: list[dict],
    fact_type: str | None = None,
    confidence: str = "medium",
    risk: str = "low",
    project: str | None = None,
    summary: str | None = None,
    suggested_target: str | None = None,
) -> dict:
    """Build a schema v1 candidate dict."""
    if fact_type is None:
        fact_type = classify_type(claim)
    classification = derive_classification(fact_type, confidence, risk, claim)
    status = "candidate" if classification["auto_promotable"] else "needs-approval"

    cand: dict = {
        "schema_version": SCHEMA_VERSION,
        "id": candidate_id(),
        "created_at": now_iso(),
        "created_by": created_by,
        "topic_id": topic_id,
        "type": fact_type,
        "claim": claim,
        "confidence": confidence,
        "risk": risk,
        "classification": classification,
        "evidence": evidence,
        "status": status,
        "human_review": {
            "required": classification["needs_human_approval"],
            "decision": None,
            "reviewer": None,
            "reviewed_at": None,
            "notes": None,
        },
    }
    if project:
        cand["project"] = project
    if summary:
        cand["summary"] = summary
    if suggested_target:
        cand["suggested_targets"] = [suggested_target]
    return cand


# ---------------------------------------------------------------------------
# Schema validation (Fix A1)
# ---------------------------------------------------------------------------

def validate_candidate_v1(c: dict) -> list[str]:
    """Return list of validation errors; empty list means valid.

    Validates schema_version, required fields, enum values, and deep
    evidence entry structure (Fix 2).
    """
    errors: list[str] = []
    if c.get("schema_version") != 1:
        errors.append("schema_version must be 1")
    for field in ("id", "created_at", "created_by", "type", "claim",
                  "confidence", "risk", "classification", "evidence", "status"):
        if not c.get(field):
            errors.append(f"required field missing or empty: {field}")
    if c.get("type") and c["type"] not in VALID_TYPES:
        errors.append(f"unknown type: {c['type']!r}")
    if c.get("confidence") and c["confidence"] not in VALID_CONFIDENCE:
        errors.append(f"confidence must be one of {VALID_CONFIDENCE}")
    if c.get("risk") and c["risk"] not in VALID_RISK:
        errors.append(f"risk must be one of {VALID_RISK}")
    if c.get("status") and c["status"] not in VALID_STATUS:
        errors.append(f"status must be one of {VALID_STATUS}")
    # Fix 2 — deep evidence validation
    evidence = c.get("evidence")
    if isinstance(evidence, list):
        if len(evidence) == 0:
            errors.append("evidence list must be non-empty")
        else:
            for i, ev in enumerate(evidence):
                ev_errs = validate_evidence_entry(ev)
                for e in ev_errs:
                    errors.append(f"evidence[{i}]: {e}")
    return errors


# ---------------------------------------------------------------------------
# Auto-promotion gate (Fix A1)
# ---------------------------------------------------------------------------

def can_auto_promote(c: dict) -> tuple[bool, str]:
    """
    Return (True, "") if the candidate passes all promotion gates.
    Return (False, reason) otherwise.

    Gates (all must pass):
      1. status == "candidate"
      2. schema v1 valid (including deep evidence check)
      3. type in AUTO_PROMOTE_TYPES
      4. risk == "low"  (medium is NOT auto-promotable — Fix 1)
      5. confidence != "low"
      6. evidence non-empty with valid entries
      7. no high-risk keyword in claim
      8. classification.auto_promotable == True
    """
    if c.get("status") != "candidate":
        return False, f"status is '{c.get('status')}', expected 'candidate'"

    errors = validate_candidate_v1(c)
    if errors:
        return False, "schema validation failed: " + "; ".join(errors)

    if c["type"] not in AUTO_PROMOTE_TYPES:
        return False, f"type '{c['type']}' is never auto-promotable"

    # Fix 1: only risk=low qualifies; reject medium and high explicitly
    if c["risk"] != "low":
        return False, f"risk is '{c['risk']}'; only risk=low qualifies for auto-promotion"

    if c["confidence"] == "low":
        return False, "confidence is low"

    if not c.get("evidence"):
        return False, "evidence list is empty"

    kw = contains_high_risk_keyword(str(c.get("claim", "")))
    if kw:
        return False, f"claim contains high-risk keyword: '{kw}'"

    classification = c.get("classification", {})
    if not classification.get("auto_promotable", False):
        return False, classification.get("reason", "classification.auto_promotable is false")

    return True, ""


# ---------------------------------------------------------------------------
# Schema v0 migration
# ---------------------------------------------------------------------------

def migrate_legacy(c: dict) -> dict:
    """Upgrade a schema_version 0 (or missing) candidate to v1 in-place.

    Fix 4 — behavior is explicitly IN-MEMORY ONLY. This function does NOT
    write anything to disk. Use cmd_migrate_legacy() / --migrate-legacy to
    persist the result.

    Migration rules:
    - Adds schema_version=1, confidence, risk, classification, human_review.
    - Evidence defaults to [{kind: manual, ref: legacy, locator: migrated-v0}];
      locator is non-empty so it passes deep validation.
    - Forces status=needs-approval for all non-terminal candidates regardless
      of type (conservative — human must review migrated facts).
    - Terminal statuses (auto-promoted, approved, rejected, duplicate,
      obsolete) are preserved unchanged.
    """
    if c.get("schema_version") == 1:
        return c
    c["schema_version"] = 1
    c.setdefault("confidence", "medium")
    c.setdefault("risk", "medium")
    # Fix 2/4: default evidence has a non-empty locator so it passes validation
    c.setdefault("evidence", [{
        "kind": "manual",
        "ref": "legacy-migration",
        "locator": "migrated-v0",
        "observed_at": now_iso(),
    }])
    c.setdefault("classification", {
        "auto_promotable": False,
        "needs_human_approval": True,
        "reason": "Migrated from schema v0; human review required before promotion.",
    })
    c.setdefault("human_review", {
        "required": True,
        "decision": None,
        "reviewer": None,
        "reviewed_at": None,
        "notes": None,
    })
    # Force needs-approval for all non-terminal statuses (conservative)
    if c.get("status") not in TERMINAL_STATUS:
        c["status"] = "needs-approval"
    return c


def load_and_migrate(path: Path) -> list[dict]:
    """Load candidates and apply in-memory migration for v0 entries.

    Fix 4 — this is a read-time compatibility shim only. Changes are NOT
    persisted to disk. Call cmd_migrate_legacy() / --migrate-legacy to save.
    """
    candidates = load_candidates(path)
    migrated = [migrate_legacy(c) for c in candidates]
    return migrated


def cmd_reclassify(
    topic_id: str,
    candidate_id: str,
    memory_dir: Path,
    fact_type: str | None = None,
    risk: str | None = None,
    status: str | None = None,
    dry_run: bool = False,
) -> int:
    """Reclassify an existing candidate: override fact_type, risk, and/or status.

    Needed because auto-extracted candidates may all be typed as 'fact'.
    Example:
        python manage-candidates.py 7301 --reclassify abc123 \
            --fact-type testing_strategy --risk medium --status needs-approval
    """
    candidates = _load_candidates(topic_id, memory_dir)
    match = next((c for c in candidates if c.get("id") == candidate_id), None)
    if match is None:
        print(f"ERROR: candidate {candidate_id!r} not found for topic {topic_id}", file=sys.stderr)
        return 1

    changed: list[str] = []

    # Apply field overrides
    if fact_type is not None and match.get("type") != fact_type:
        match["type"] = fact_type
        changed.append(f"type={fact_type}")

    if risk is not None and match.get("risk") != risk:
        match["risk"] = risk
        changed.append(f"risk={risk}")

    # Recompute classification from current type+risk unless explicit status override
    if changed and status is None:
        classification = derive_classification(
            match.get("type", "fact"),
            match.get("confidence", "medium"),
            match.get("risk", "medium"),
            match.get("claim", ""),
        )
        new_status = classification["status"]
        if match.get("status") != new_status:
            match["status"] = new_status
            changed.append(f"status={new_status} (auto-recomputed)")

    # Explicit status override wins over auto-recomputed
    if status is not None and match.get("status") != status:
        match["status"] = status
        changed.append(f"status={status} (override)")

    if not changed:
        print(f"No changes for candidate {candidate_id}")
        return 0

    match["reclassified_at"] = datetime.now(timezone.utc).isoformat()

    if dry_run:
        print(f"DRY RUN — would update {candidate_id}: {', '.join(changed)}")
        return 0

    _save_candidates(topic_id, candidates, memory_dir)
    print(f"Reclassified {candidate_id}: {', '.join(changed)}")
    return 0


def cmd_migrate_legacy(topic_id: str, memory_dir: Path, dry_run: bool = False) -> None:
    """Fix 4 — persist v0→v1 migration to disk."""
    cf = candidates_file(memory_dir, topic_id)
    with locked_path(cf):
        original = load_candidates(cf)
        v0_count = sum(1 for c in original if c.get("schema_version") != 1)
        if v0_count == 0:
            print(f"All {len(original)} candidates are already schema v1. Nothing to migrate.")
            return
        migrated = [migrate_legacy(c) for c in original]
        print(f"Migrating {v0_count}/{len(original)} legacy candidates to schema v1:")
        for orig, mig in zip(original, migrated):
            if orig.get("schema_version") != 1:
                print(f"  {mig['id']}  [{mig.get('type','?')}]  {str(mig.get('claim',''))[:60]}")
        if dry_run:
            print("\n[dry-run] No changes written.")
            return
        save_candidates(cf, migrated)
        print(f"\nPersisted migration → {cf}")


# ---------------------------------------------------------------------------
# Commands
# ---------------------------------------------------------------------------

def cmd_add(
    topic_id: str,
    facts_source: str,
    memory_dir: Path,
    session_id: str,
    *,
    source_kind: str,
    source_ref: str,
    locator: str,
    confidence: str,
    risk: str,
    project: str | None,
    summary: str | None,
    suggested_target: str | None,
) -> None:
    """Read facts from file or stdin, create v1 candidate entries."""
    if facts_source == "-":
        lines = sys.stdin.read().splitlines()
    else:
        lines = Path(facts_source).read_text(encoding="utf-8", errors="replace").splitlines()

    facts = [l.strip() for l in lines if l.strip() and l.strip().startswith("- ")]
    if not facts:
        print("ERROR: no bullet facts found (lines must start with '- ')", file=sys.stderr)
        sys.exit(1)

    evidence = [make_evidence_entry(source_kind, source_ref, locator, topic_id)]
    cf = candidates_file(memory_dir, topic_id)
    with locked_path(cf):
        existing = load_and_migrate(cf)

        added = 0
        for fact in facts:
            cand = build_candidate_v1(
                claim=fact,
                topic_id=topic_id,
                created_by=session_id,
                evidence=evidence,
                confidence=confidence,
                risk=risk,
                project=project,
                summary=summary,
                suggested_target=suggested_target,
            )
            existing.append(cand)
            added += 1
            gate_ok, gate_reason = can_auto_promote(cand)
            gate_label = "AUTO" if gate_ok else f"REVIEW ({gate_reason})"
            print(f"  {cand['id']}  [{cand['type']}]  [{gate_label}]  {fact[:72]}")

        save_candidates(cf, existing)
        print(f"\nAdded {added} candidates → {cf}")


def cmd_list(topic_id: str, memory_dir: Path) -> None:
    cf = candidates_file(memory_dir, topic_id)
    candidates = load_and_migrate(cf)
    if not candidates:
        print(f"No candidates for topic {topic_id}")
        return

    by_status: dict[str, list] = {}
    for c in candidates:
        s = c.get("status", "?")
        by_status.setdefault(s, []).append(c)

    print(f"CANDIDATES  [topic:{topic_id}]  total:{len(candidates)}")
    print("=" * 70)
    for status, items in sorted(by_status.items()):
        print(f"\n[{status.upper()}]  ({len(items)})")
        for c in items:
            claim = str(c.get("claim", ""))[:68]
            conf = c.get("confidence", "?")
            risk = c.get("risk", "?")
            print(f"  {c['id']}  [{c.get('type','?')}]  conf:{conf}  risk:{risk}  {claim}")


def cmd_status(topic_id: str, memory_dir: Path) -> None:
    cf = candidates_file(memory_dir, topic_id)
    candidates = load_and_migrate(cf)
    counts: dict[str, int] = {}
    for c in candidates:
        s = c.get("status", "?")
        counts[s] = counts.get(s, 0) + 1
    print(f"CANDIDATE STATUS  [topic:{topic_id}]")
    print(f"  Total: {len(candidates)}")
    for s, n in sorted(counts.items()):
        print(f"  {s:<20}: {n}")
    print(f"  File: {cf}")


def cmd_promote_auto(topic_id: str, memory_dir: Path, agents_base: Path, dry_run: bool = False) -> None:
    """Auto-promote candidates that pass all gates → write to L2 memory."""
    cf = candidates_file(memory_dir, topic_id)
    with locked_path(cf):
        candidates = load_and_migrate(cf)

        promotable = []
        blocked = []
        for c in candidates:
            ok, reason = can_auto_promote(c)
            if ok:
                promotable.append(c)
            elif c.get("status") == "candidate":
                blocked.append((c, reason))

        if blocked:
            print(f"BLOCKED ({len(blocked)} candidates):")
            for c, reason in blocked:
                print(f"  {c['id']}  [{c.get('type','?')}]  {reason}")

        if not promotable:
            print(f"\nNothing to auto-promote.")
            return

        print(f"\nAUTO-PROMOTE ({len(promotable)} candidates):")
        for c in promotable:
            print(f"  {c['id']}  [{c.get('type','?')}]  {str(c.get('claim',''))[:72]}")

        if dry_run:
            print("\n[dry-run] No changes written.")
            return

        ab = _import_archive_batch()
        session_id = f"promote-{datetime.now(timezone.utc).strftime('%Y%m%d-%H%M%S')}"
        facts = [str(c["claim"]) for c in promotable]
        memory_file = memory_dir / f"topic-{topic_id}.md"

        existing_content = memory_file.read_text(encoding="utf-8") if memory_file.exists() else ""
        existing_bullets = ab.extract_existing_bullets(existing_content)
        written = ab.write_batch_to_memory(
            memory_file=memory_file,
            topic_id=topic_id,
            batch_n=-1,
            session_id=session_id,
            facts=facts,
            existing_bullets=existing_bullets,
        )

        promoted_ids = {c["id"] for c in promotable}
        for c in candidates:
            if c["id"] in promoted_ids:
                c["status"] = "auto-promoted"
                c["promoted_at"] = now_iso()
                c["promoted_by"] = session_id

        save_candidates(cf, candidates)
        print(f"\nAuto-promoted {len(promotable)} → {written} facts written to {memory_file}")


def cmd_approve(
    topic_id: str,
    cand_id: str,
    memory_dir: Path,
    agents_base: Path,
    reviewer: str | None = None,
) -> None:
    """Manually approve a candidate → write to L2."""
    cf = candidates_file(memory_dir, topic_id)
    with locked_path(cf):
        candidates = load_and_migrate(cf)

        target = next((c for c in candidates if c.get("id") == cand_id), None)
        if not target:
            print(f"ERROR: candidate {cand_id} not found", file=sys.stderr)
            sys.exit(1)

        # Non-blocking fix: prevent --approve on already-terminal candidates
        if target.get("status") in TERMINAL_STATUS:
            print(
                f"ERROR: candidate {cand_id} is already in terminal state "
                f"'{target['status']}' and cannot be approved again.",
                file=sys.stderr,
            )
            sys.exit(1)

        errors = validate_candidate_v1(target)
        if errors:
            print(f"ERROR: candidate fails schema validation:", file=sys.stderr)
            for e in errors:
                print(f"  - {e}", file=sys.stderr)
            sys.exit(1)

        ab = _import_archive_batch()
        session_id = f"approve-{cand_id}"
        memory_file = memory_dir / f"topic-{topic_id}.md"
        existing_content = memory_file.read_text(encoding="utf-8") if memory_file.exists() else ""
        existing_bullets = ab.extract_existing_bullets(existing_content)
        ab.write_batch_to_memory(
            memory_file=memory_file,
            topic_id=topic_id,
            batch_n=-1,
            session_id=session_id,
            facts=[str(target["claim"])],
            existing_bullets=existing_bullets,
        )
        # TODO (PR 6): pass candidate_id and evidence metadata into write_batch_to_memory
        # so the L0 audit entry records provenance traceable back to this candidate.

        target["status"] = "approved"
        target["approved_at"] = now_iso()
        if reviewer:
            target["approved_by"] = reviewer
        target.setdefault("human_review", {})
        target["human_review"]["decision"] = "approved"
        target["human_review"]["reviewer"] = reviewer
        target["human_review"]["reviewed_at"] = now_iso()
        save_candidates(cf, candidates)
        print(f"Approved {cand_id} → written to {memory_file}")


def cmd_reject(
    topic_id: str,
    cand_id: str,
    memory_dir: Path,
    reason: str | None = None,
    reviewer: str | None = None,
) -> None:
    cf = candidates_file(memory_dir, topic_id)
    with locked_path(cf):
        candidates = load_and_migrate(cf)
        target = next((c for c in candidates if c.get("id") == cand_id), None)
        if not target:
            print(f"ERROR: candidate {cand_id} not found", file=sys.stderr)
            sys.exit(1)
        target["status"] = "rejected"
        target["rejected_at"] = now_iso()
        if reason:
            target["rejected_reason"] = reason
        if reviewer:
            target["rejected_by"] = reviewer
        target.setdefault("human_review", {})
        target["human_review"]["decision"] = "rejected"
        target["human_review"]["reviewer"] = reviewer
        target["human_review"]["reviewed_at"] = now_iso()
        target["human_review"]["notes"] = reason
        save_candidates(cf, candidates)
    print(f"Rejected {cand_id}: {str(target.get('claim',''))[:80]}")


def cmd_show(topic_id: str, cand_id: str, memory_dir: Path) -> None:
    cf = candidates_file(memory_dir, topic_id)
    candidates = load_and_migrate(cf)
    target = next((c for c in candidates if c.get("id") == cand_id), None)
    if not target:
        print(f"ERROR: candidate {cand_id} not found", file=sys.stderr)
        sys.exit(1)
    print(yaml.dump(target, allow_unicode=True, sort_keys=False))

    ok, reason = can_auto_promote(target)
    if ok:
        print("AUTO-PROMOTION GATE: ✓ PASS")
    else:
        print(f"AUTO-PROMOTION GATE: ✗ BLOCKED — {reason}")


def cmd_validate(topic_id: str, memory_dir: Path) -> int:
    """Validate all candidates in the file; return exit code."""
    cf = candidates_file(memory_dir, topic_id)
    candidates = load_candidates(cf)
    total = len(candidates)
    errors_found = 0
    for c in candidates:
        errs = validate_candidate_v1(c)
        if errs:
            errors_found += 1
            print(f"INVALID  {c.get('id','?')}:")
            for e in errs:
                print(f"  - {e}")
    if errors_found == 0:
        print(f"All {total} candidates valid (schema v1).")
        return 0
    else:
        print(f"\n{errors_found}/{total} candidates have validation errors.")
        return 1


# ---------------------------------------------------------------------------
# Import archive-batch-v2 helpers
# ---------------------------------------------------------------------------

def _import_archive_batch():
    script = Path(__file__).parent / "archive-batch-v2.py"
    spec = importlib.util.spec_from_file_location("archive_batch_v2", script)
    mod = importlib.util.module_from_spec(spec)
    sys.modules["archive_batch_v2"] = mod
    spec.loader.exec_module(mod)
    return mod


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def main() -> int:
    parser = argparse.ArgumentParser(
        description="L1 Candidate Knowledge manager (schema v1) — see docs/CANDIDATE_SCHEMA.md"
    )
    parser.add_argument("topic", help="Topic ID (numeric) or topic name")

    group = parser.add_mutually_exclusive_group(required=True)
    group.add_argument("--add", metavar="FACTS_FILE",
                       help="Add candidates from facts file (or '-' for stdin)")
    group.add_argument("--list", action="store_true", help="List all candidates")
    group.add_argument("--status", action="store_true", help="Show summary stats")
    group.add_argument("--promote-auto", action="store_true",
                       help="Auto-promote eligible candidates to L2 memory")
    group.add_argument("--approve", metavar="CANDIDATE_ID",
                       help="Manually approve candidate → write to L2")
    group.add_argument("--reject", metavar="CANDIDATE_ID", help="Reject a candidate")
    group.add_argument("--show", metavar="CANDIDATE_ID", help="Show full candidate details")
    group.add_argument("--validate", action="store_true",
                       help="Validate all candidates against schema v1 (read-only; does not persist migration)")
    group.add_argument("--migrate-legacy", action="store_true",
                       help="Persist v0→v1 schema migration to disk (use --dry-run to preview)")
    group.add_argument("--reclassify", metavar="CANDIDATE_ID",
                       help="Override type/risk/status on an existing candidate")

    # Provenance flags for --add (Fix A1 + Fix 3)
    parser.add_argument("--source-kind", default="session_history",
                        choices=sorted(VALID_EVIDENCE_KINDS),
                        help="Evidence kind (default: session_history)")
    parser.add_argument("--source-ref", default="",
                        help="Human-readable evidence reference, e.g. 'batch 12, message 47' (required for add)")
    parser.add_argument("--locator", default="",
                        help="Machine-usable position locator, e.g. 'batch:12:msg:47' or 'line:10' (required for add)")
    parser.add_argument("--confidence", default="medium", choices=["low", "medium", "high"],
                        help="Confidence level (default: medium)")
    # Non-blocking fix: default medium (safer than low) to make risk explicit
    parser.add_argument("--risk", default="medium", choices=["low", "medium", "high"],
                        help="Risk level (default: medium — use --risk low to enable auto-promotion)")
    parser.add_argument("--project", default=None, help="Project name")
    parser.add_argument("--summary", default=None, help="Human-readable context for reviewers")
    parser.add_argument("--suggested-target", default=None,
                        help="Target memory file path for promotion")

    # Reclassify extras
    parser.add_argument("--fact-type", default=None,
                        help="New fact type slug (for --reclassify)")
    parser.add_argument("--risk-override", default=None, dest="risk_override",
                        choices=["low", "medium", "high"],
                        help="Override risk level for --reclassify. "
                             "Use this instead of --risk to explicitly set medium.")
    parser.add_argument("--status-override", default=None, dest="status_override",
                        choices=["candidate", "auto-promoted", "needs-approval",
                                 "approved", "rejected", "obsolete", "duplicate"],
                        help="Force status (for --reclassify)")

    # Approve/reject extras
    parser.add_argument("--reviewer", default=None, help="Reviewer name/ID (for --approve/--reject)")
    parser.add_argument("--reason", default=None, help="Rejection reason (for --reject)")

    # Promote flags
    parser.add_argument("--dry-run", action="store_true",
                        help="Show what would be promoted without writing")

    parser.add_argument("--session-id", default=None, help="Session ID for traceability")
    parser.add_argument("--memory-dir", type=Path,
                        default=Path(".agent/memory"),
                        help="Memory directory (default: .agent/memory)")
    parser.add_argument("--agents-base", type=Path, default=DEFAULT_AGENTS_BASE)
    args = parser.parse_args()

    # Resolve topic name → ID
    ab = _import_archive_batch()
    topic_id = ab.resolve_topic_id(args.topic, args.agents_base)

    session_id = args.session_id or f"mgr-{datetime.now(timezone.utc).strftime('%Y%m%d-%H%M%S')}"
    memory_dir = args.memory_dir

    if args.add:
        cmd_add(
            topic_id, args.add, memory_dir, session_id,
            source_kind=args.source_kind,
            source_ref=args.source_ref,
            locator=args.locator,
            confidence=args.confidence,
            risk=args.risk,
            project=args.project,
            summary=args.summary,
            suggested_target=args.suggested_target,
        )
    elif args.list:
        cmd_list(topic_id, memory_dir)
    elif args.status:
        cmd_status(topic_id, memory_dir)
    elif args.promote_auto:
        cmd_promote_auto(topic_id, memory_dir, args.agents_base, dry_run=args.dry_run)
    elif args.approve:
        cmd_approve(topic_id, args.approve, memory_dir, args.agents_base, reviewer=args.reviewer)
    elif args.reject:
        cmd_reject(topic_id, args.reject, memory_dir, reason=args.reason, reviewer=args.reviewer)
    elif args.show:
        cmd_show(topic_id, args.show, memory_dir)
    elif args.validate:
        return cmd_validate(topic_id, memory_dir)
    elif args.migrate_legacy:
        cmd_migrate_legacy(topic_id, memory_dir, dry_run=args.dry_run)
    elif args.reclassify:
        return cmd_reclassify(
            topic_id, args.reclassify, memory_dir,
            fact_type=args.fact_type,
            risk=args.risk_override,
            status=args.status_override,
            dry_run=args.dry_run,
        )

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
