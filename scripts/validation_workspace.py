"""Shared disposable-output policy for root-owned validation commands."""
import json
import os
from pathlib import Path
import re

ROOT = Path(__file__).resolve().parents[1]


def tap_counts(raw):
    counts = {name: int(value) for name, value in re.findall(
        r"^# (tests|pass|fail|cancelled|skipped|todo) (\d+)\s*$", raw, re.M)}
    if (set(counts) != {"tests", "pass", "fail", "cancelled", "skipped", "todo"}
            or counts["tests"] <= 0 or counts["pass"] != counts["tests"]
            or any(counts[k] for k in ("fail", "cancelled", "skipped", "todo"))):
        raise ValueError("Missing, skipped or unsuccessful TAP test results")
    return counts


def guarded_output(name):
    if not re.fullmatch(r"[a-z][a-z0-9-]{0,63}", name):
        raise ValueError("Invalid validation output name")
    base = ROOT / ".canary/work/current/outputs"
    cache = ROOT / ".canary/cache/go-build"
    if (not (ROOT / ".canary/bounded-validation.lock").is_file()
            or not base.is_dir() or base.is_symlink()
            or Path(os.environ.get("GOCACHE", "")).resolve() != cache.resolve()
            or Path(os.environ.get("GOTMPDIR", "")).resolve() != (base.parent / "tmp").resolve()):
        raise RuntimeError("Run through python scripts/bounded_validation.py")
    output = base / name
    output.mkdir(exist_ok=False)
    return output


def retain_summary(name, value):
    """Overwrite one small result, never retain a growing artifact collection."""
    if not re.fullmatch(r"[a-z][a-z0-9-]{0,63}", name):
        raise ValueError("Invalid validation summary name")
    raw = (json.dumps(value, indent=2, sort_keys=True) + "\n").encode()
    if len(raw) > 128 * 1024:
        raise ValueError("Validation summary exceeds 128 KiB")
    path = ROOT / ".canary" / (name + "-current.json")
    if path.is_symlink():
        raise ValueError("Validation summary must be a regular file")
    path.write_bytes(raw)
