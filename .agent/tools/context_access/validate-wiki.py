#!/usr/bin/env python3
"""validate-wiki.py — Pre-live integrity checker for L3 Knowledge Vault.

Usage:
  python3 validate-wiki.py --memory-dir .agent/memory
  python3 validate-wiki.py --memory-dir .agent/memory --json
  python3 validate-wiki.py --memory-dir .agent/memory --strict
  python3 validate-wiki.py --memory-dir .agent/memory --write-report PATH

Exit: 0=ok, 1=errors (or warnings with --strict)
"""
from __future__ import annotations
import argparse, hashlib, json, sys
from datetime import datetime, timezone
from pathlib import Path

ERROR, WARN, OK = "error", "warning", "ok"

class Finding:
    def __init__(self, level, code, message):
        self.level, self.code, self.message = level, code, message
    def as_dict(self): return {"level":self.level,"code":self.code,"message":self.message}
    def __repr__(self):
        sym={"error":"x","warning":"!","ok":"v"}.get(self.level,"?")
        return f"  [{sym}] {self.message}"

def _sha256(path):
    h=hashlib.sha256()
    with path.open("rb") as f:
        for chunk in iter(lambda:f.read(65536),b""): h.update(chunk)
    return h.hexdigest()

def _parse_iso(ts):
    for fmt in ("%Y-%m-%dT%H:%M:%SZ","%Y-%m-%dT%H:%M:%S"):
        try: return datetime.strptime(ts,fmt).replace(tzinfo=timezone.utc)
        except ValueError: pass
    return None

def iter_source_entries(meta):
    """Yield only dict entries from source_files; silently skip non-dicts."""
    sf=meta.get("source_files",[])
    if not isinstance(sf,list): return
    for entry in sf:
        if isinstance(entry,dict): yield entry

def check_meta_exists(wiki_dir,findings):
    p=wiki_dir/"WIKI_META.json"
    if not p.exists(): findings.append(Finding(ERROR,"meta_missing",f"WIKI_META.json not found: {p}")); return None
    try: return json.loads(p.read_text(encoding="utf-8"))
    except Exception as e: findings.append(Finding(ERROR,"meta_invalid_json",f"invalid JSON: {e}")); return None

def check_schema_version(meta,findings):
    v=meta.get("wiki_schema_version")
    if v is None: findings.append(Finding(ERROR,"schema_version_missing","missing wiki_schema_version"))
    elif not isinstance(v,int) or v<2: findings.append(Finding(ERROR,"schema_version_low",f"version={v!r}, need int>=2"))

def check_source_files_structure(meta,findings):
    sf=meta.get("source_files")
    if sf is None: findings.append(Finding(ERROR,"source_files_missing","missing source_files")); return
    if not isinstance(sf,list): findings.append(Finding(ERROR,"source_files_not_list","source_files not list")); return
    req={"path","topic_id","fact_count","last_batch","sha256","mtime"}
    for i,e in enumerate(sf):
        if not isinstance(e,dict): findings.append(Finding(ERROR,"source_files_entry_not_dict",f"sf[{i}] not dict")); continue
        miss=req-e.keys()
        if miss: findings.append(Finding(ERROR,"source_files_entry_missing_keys",f"sf[{i}] missing {sorted(miss)}"))

def check_facts_structure(meta,findings):
    facts=meta.get("facts")
    if facts is None: findings.append(Finding(ERROR,"facts_missing","missing facts")); return []
    if not isinstance(facts,list): findings.append(Finding(ERROR,"facts_not_list","facts not list")); return []
    req={"id","text","topic_id","source_file","line_number","batch_n","batch_date","session_id","is_conflict","fact_type"}
    bad=[]
    for i,f in enumerate(facts):
        if not isinstance(f,dict): findings.append(Finding(ERROR,"fact_not_dict",f"facts[{i}] not dict")); bad.append(i); continue
        miss=req-f.keys()
        if miss: findings.append(Finding(ERROR,"fact_missing_fields",f"facts[{i}] missing {sorted(miss)}"))
    return [f for i,f in enumerate(facts) if i not in bad]

def check_source_files_exist(meta,memory_dir,findings):
    resolved={}
    for entry in iter_source_entries(meta):
        sp=entry.get("path",""); c=memory_dir.parent/sp
        if not c.exists():
            c2=Path(sp)
            if c2.exists(): c=c2
            else: findings.append(Finding(ERROR,"source_file_missing",f"not found: {sp!r}")); continue
        resolved[sp]=c
    return resolved

def check_line_numbers(facts,resolved,findings):
    lc={sp:sum(1 for _ in rp.open(encoding="utf-8",errors="replace")) for sp,rp in resolved.items()}
    for f in facts:
        sp,ln=f.get("source_file",""),f.get("line_number")
        if sp not in lc: continue
        if not isinstance(ln,int) or ln<1: findings.append(Finding(ERROR,"line_number_invalid",f"{f.get('id','?')} ln={ln!r}"))
        elif ln>lc[sp]: findings.append(Finding(ERROR,"line_number_out_of_range",f"{f.get('id','?')} ln={ln}>{lc[sp]}"))

def check_fact_text_at_line(facts,resolved,findings):
    fl={sp:rp.read_text(encoding="utf-8",errors="replace").splitlines() for sp,rp in resolved.items()}
    for f in facts:
        sp,ln,text=f.get("source_file",""),f.get("line_number"),f.get("text","")
        if sp not in fl or not isinstance(ln,int): continue
        w=range(max(0,ln-3),min(len(fl[sp]),ln+2))
        if not any(text in fl[sp][i] for i in w): findings.append(Finding(WARN,"fact_text_not_near_line",f"{f.get('id','?')} text not near line {ln}"))

def check_fact_count_consistency(meta,facts,findings):
    actual={}
    for f in facts: sp=f.get("source_file",""); actual[sp]=actual.get(sp,0)+1
    for e in iter_source_entries(meta):
        sp,d,r=e.get("path",""),e.get("fact_count",0),actual.get(e.get("path",""),0)
        if d!=r: findings.append(Finding(ERROR,"fact_count_mismatch",f"{sp!r}: declared={d} actual={r}"))

def check_conflict_facts_count(meta,facts,findings):
    d=meta.get("conflict_facts")
    if d is None: findings.append(Finding(WARN,"conflict_facts_missing","missing conflict_facts")); return
    a=sum(1 for f in facts if f.get("is_conflict"))
    if d!=a: findings.append(Finding(ERROR,"conflict_facts_mismatch",f"declared={d} actual={a}"))

def check_per_topic_last_batch(meta,facts,findings):
    pt=meta.get("per_topic_last_batch")
    if pt is None: findings.append(Finding(WARN,"per_topic_last_batch_missing","missing per_topic_last_batch")); return
    actual={}
    for f in facts:
        tid,bn=f.get("topic_id",""),f.get("batch_n")
        if bn is None: actual.setdefault(tid,None)
        else: actual[tid]=max(actual.get(tid) or 0,bn) if actual.get(tid) is not None else bn
    for tid,d in pt.items():
        if tid not in actual: continue
        r=actual.get(tid)
        if d!=r: findings.append(Finding(ERROR,"per_topic_last_batch_mismatch",f"{tid!r}: declared={d!r} actual={r!r}"))

def check_topic_pages_exist(meta,wiki_dir,findings):
    for e in iter_source_entries(meta):
        tid=e.get("topic_id",""); p=wiki_dir/f"topic-{tid}.md"
        if not p.exists(): findings.append(Finding(ERROR,"topic_page_missing",f"topic {tid!r}: {p}"))

def check_by_type_pages_exist(facts,wiki_dir,findings):
    """Use sanitized facts list (output of check_facts_structure)."""
    types={f.get("fact_type","") for f in facts if f.get("fact_type")}
    td=wiki_dir/"by-type"
    for ft in sorted(types):
        p=td/f"{ft}.md"
        if not p.exists(): findings.append(Finding(ERROR,"by_type_page_missing",f"{p}"))

def check_sha256_freshness(meta,resolved,findings):
    for e in iter_source_entries(meta):
        sp,sha=e.get("path",""),e.get("sha256")
        if sha is None: continue
        rp=resolved.get(sp)
        if rp is None: continue
        if _sha256(rp)!=sha: findings.append(Finding(WARN,"source_sha256_mismatch",f"{sp!r} sha256 changed (stale)"))

def check_mtime_freshness(meta,resolved,findings):
    """Warn if any source file is newer than built_at."""
    ba=_parse_iso(meta.get("built_at","") or "")
    if ba is None: return
    for e in iter_source_entries(meta):
        sp=e.get("path",""); rp=resolved.get(sp)
        if rp is None: continue
        try: mt=datetime.fromtimestamp(rp.stat().st_mtime,tz=timezone.utc)
        except: continue
        if mt>ba: findings.append(Finding(WARN,"source_mtime_newer",f"{sp!r} mtime newer than built_at"))

def check_stored_mtime(meta,resolved,findings):
    """Validate stored mtime format and compare against actual file mtime."""
    for e in iter_source_entries(meta):
        sp,stored=e.get("path",""),e.get("mtime")
        if stored is None: continue
        parsed=_parse_iso(stored)
        if parsed is None:
            findings.append(Finding(WARN,"source_mtime_invalid_format",f"{sp!r} mtime={stored!r} cannot be parsed")); continue
        rp=resolved.get(sp)
        if rp is None: continue
        try: actual=datetime.fromtimestamp(rp.stat().st_mtime,tz=timezone.utc)
        except: continue
        if int(actual.timestamp())!=int(parsed.timestamp()):
            findings.append(Finding(WARN,"source_mtime_mismatch",
                f"{sp!r} stored={stored!r} actual={actual.strftime('%Y-%m-%dT%H:%M:%SZ')!r}"))

def write_report(report_path,findings,memory_dir):
    errs=[f for f in findings if f.level==ERROR]; warns=[f for f in findings if f.level==WARN]
    now=datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    lines=["# Wiki Audit Report","",f"> Generated: `{now}` | memory-dir: `{memory_dir}`",
           f"> Errors: {len(errs)} | Warnings: {len(warns)}",""]
    if errs: lines+=["## Errors",""]+[f"- **[{f.code}]** {f.message}" for f in errs]+[""]
    if warns: lines+=["## Warnings",""]+[f"- **[{f.code}]** {f.message}" for f in warns]+[""]
    if not errs and not warns: lines+=["## Result","","All checks passed.",""]
    report_path.parent.mkdir(parents=True,exist_ok=True)
    report_path.write_text("\n".join(lines),encoding="utf-8")

def _emit(findings,args):
    if args.json: print(json.dumps([f.as_dict() for f in findings],ensure_ascii=False,indent=2))
    else:
        errs=[f for f in findings if f.level==ERROR]; warns=[f for f in findings if f.level==WARN]
        if not errs and not warns: print("Wiki validation passed: no errors or warnings.")
        else: [print(repr(f)) for f in findings]; print(); print(f"Result: {len(errs)} error(s), {len(warns)} warning(s)")

def main():
    parser=argparse.ArgumentParser(
        description="Pre-live wiki integrity checker for L3 Knowledge Vault.",
        epilog="""\nChecks (errors unless noted):\n  meta_exists, schema_version, source_files_structure, facts_structure,\n  source_files_exist, line_numbers, fact_text_near_line [warn],\n  fact_count, conflict_facts, per_topic_last_batch, topic_pages,\n  by_type_pages, sha256_freshness [warn], mtime_freshness [warn],\n  stored_mtime [warn]\n\nSee docs/PRE_LIVE_CHECKLIST.md for full pre-live workflow.""",
        formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--memory-dir",type=Path,default=Path(".agent/memory"))
    parser.add_argument("--json",action="store_true")
    parser.add_argument("--strict",action="store_true")
    parser.add_argument("--write-report",type=Path,default=None,metavar="PATH")
    args=parser.parse_args()
    memory_dir=args.memory_dir
    if not memory_dir.exists(): print(f"ERROR: not found: {memory_dir}",file=sys.stderr); return 1
    wiki_dir=memory_dir/"wiki"; findings=[]
    meta=check_meta_exists(wiki_dir,findings)
    if meta is None: _emit(findings,args); return 1
    check_schema_version(meta,findings)
    check_source_files_structure(meta,findings)
    facts=check_facts_structure(meta,findings)
    resolved=check_source_files_exist(meta,memory_dir,findings)
    check_line_numbers(facts,resolved,findings)
    check_fact_text_at_line(facts,resolved,findings)
    check_fact_count_consistency(meta,facts,findings)
    check_conflict_facts_count(meta,facts,findings)
    check_per_topic_last_batch(meta,facts,findings)
    check_topic_pages_exist(meta,wiki_dir,findings)
    check_by_type_pages_exist(facts,wiki_dir,findings)
    check_sha256_freshness(meta,resolved,findings)
    check_mtime_freshness(meta,resolved,findings)
    check_stored_mtime(meta,resolved,findings)
    if args.write_report: write_report(args.write_report,findings,memory_dir)
    _emit(findings,args)
    errs=[f for f in findings if f.level==ERROR]; warns=[f for f in findings if f.level==WARN]
    if errs: return 1
    if args.strict and warns: return 1
    return 0

if __name__=="__main__":
    raise SystemExit(main())
