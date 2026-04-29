"""
io_utils.py — Transactional file I/O for openclaw-agent-memory-infra.

Public API
----------
write_text_in_lock(path, content, *, encoding)
    Atomic temp → fsync → os.replace WITHOUT acquiring a lock.
    Caller must hold locked_path(path) first.

locked_path(path, lock_dir=None)
    Context manager: acquire exclusive lock for the entire block.
    Use for read→modify→write transactions:
        with locked_path(target) as p:
            current = p.read_text() if p.exists() else ""
            new = transform(current)
            write_text_in_lock(p, new)

atomic_write_text(path, content, *, encoding, lock_dir)
    Convenience: locked_path + write_text_in_lock in one call.
    For standalone writes that don't need a surrounding transaction.

atomic_append_text(path, content, *, encoding, lock_dir)
    Convenience: locked_path + read-existing + write_text_in_lock.

Lock file location
------------------
Default : <parent>/.locks/<filename>.<path-hash8>.lock
Override: OPENCLAW_MEMORY_LOCK_DIR env var   OR   lock_dir= kwarg.

Path hash in the lock filename prevents collisions when two different
files share the same basename in a shared lock directory.

Concurrency model
-----------------
Each lock uses a threading.Lock (within-process thread safety) AND
fcntl.LOCK_EX (cross-process safety). Both must be released before
another thread/process can acquire the lock.
"""
from __future__ import annotations

import fcntl
import hashlib
import os
import tempfile
import threading
from contextlib import contextmanager
from pathlib import Path
from typing import Optional

_ENV_LOCK_DIR = "OPENCLAW_MEMORY_LOCK_DIR"
_LOCK_SUBDIR = ".locks"

# Per-lock-path threading.Lock objects — ensures thread safety within one process.
_THREAD_LOCKS: dict[str, threading.Lock] = {}
_THREAD_LOCKS_MU = threading.Lock()


def _get_thread_lock(lock_path: Path) -> threading.Lock:
    key = str(lock_path)
    with _THREAD_LOCKS_MU:
        if key not in _THREAD_LOCKS:
            _THREAD_LOCKS[key] = threading.Lock()
        return _THREAD_LOCKS[key]


def _resolve_lock_dir(path: Path, lock_dir: Optional[Path]) -> Path:
    if lock_dir is not None:
        return Path(lock_dir)
    env = os.environ.get(_ENV_LOCK_DIR)
    if env:
        return Path(env)
    return path.parent / _LOCK_SUBDIR


def _lock_path(path: Path, lock_dir: Optional[Path]) -> Path:
    ld = _resolve_lock_dir(path, lock_dir)
    ld.mkdir(parents=True, exist_ok=True)
    # Include 8-char hash of the absolute path to avoid collisions in shared lock dirs.
    path_hash = hashlib.sha256(str(path.resolve()).encode()).hexdigest()[:8]
    safe = f"{path.name.replace('/', '_')}.{path_hash}.lock"
    return ld / safe


@contextmanager
def _acquire_lock(path: Path, lock_dir: Optional[Path]):
    """Acquire thread lock + exclusive flock for *path*."""
    lp = _lock_path(path, lock_dir)
    lp.parent.mkdir(parents=True, exist_ok=True)
    tl = _get_thread_lock(lp)
    with tl:  # within-process thread safety
        with open(lp, "w") as lf:
            fcntl.flock(lf, fcntl.LOCK_EX)  # cross-process safety
            try:
                yield
            finally:
                fcntl.flock(lf, fcntl.LOCK_UN)


# ---------------------------------------------------------------------------
# Public API
# ---------------------------------------------------------------------------

def write_text_in_lock(
    path: "Path | str",
    content: str,
    *,
    encoding: str = "utf-8",
) -> None:
    """Write *content* to *path* atomically (temp → fsync → os.replace).

    Does NOT acquire a lock — the caller must hold ``locked_path(path)``
    before calling this function.  For standalone single-file writes that
    do not participate in a larger transaction, use ``atomic_write_text()``.
    """
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, tmp = tempfile.mkstemp(dir=path.parent, prefix=f".{path.name}.tmp.")
    try:
        with os.fdopen(fd, "w", encoding=encoding) as fh:
            fh.write(content)
            fh.flush()
            os.fsync(fh.fileno())
        os.replace(tmp, path)
    except Exception:
        try:
            os.unlink(tmp)
        except OSError:
            pass
        raise


@contextmanager
def locked_path(
    path: "Path | str",
    lock_dir: Optional[Path] = None,
):
    """Context manager: hold an exclusive lock on *path* for the entire block.

    Use this to protect read→modify→write transactions so that no other
    process or thread can slip in between the read and the write::

        with locked_path(memory_file) as p:
            current = p.read_text(encoding="utf-8") if p.exists() else ""
            new_content = transform(current)
            write_text_in_lock(p, new_content)

    Inside the block, use ``write_text_in_lock()`` to write atomically
    without re-acquiring the lock.
    """
    path = Path(path)
    with _acquire_lock(path, lock_dir):
        yield path


def atomic_write_text(
    path: "Path | str",
    content: str,
    *,
    encoding: str = "utf-8",
    lock_dir: Optional[Path] = None,
) -> None:
    """Write *content* to *path* atomically under an exclusive lock.

    Sequence: acquire lock → write to temp → fsync → os.replace → release.

    For transactions requiring read→modify→write, prefer::

        with locked_path(path) as p:
            current = p.read_text() if p.exists() else ""
            ...
            write_text_in_lock(p, new_content)
    """
    with locked_path(path, lock_dir):
        write_text_in_lock(path, content, encoding=encoding)


def atomic_append_text(
    path: "Path | str",
    content: str,
    *,
    encoding: str = "utf-8",
    lock_dir: Optional[Path] = None,
) -> None:
    """Append *content* to *path* atomically under an exclusive lock.

    Sequence: acquire lock → read existing → write (existing+content) to
    temp → fsync → os.replace → release.
    """
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    with locked_path(path, lock_dir):
        existing = path.read_text(encoding=encoding) if path.exists() else ""
        write_text_in_lock(path, existing + content, encoding=encoding)
