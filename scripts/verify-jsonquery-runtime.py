"""Verify the checked-in JSONata asset and pinned Goja source replacement."""
import hashlib
import json
from pathlib import Path
import re

ROOT = Path(__file__).resolve().parents[1]


def checked_file(directory, name):
    path = directory / name
    if path.is_symlink() or not path.resolve().is_relative_to(directory.resolve()):
        raise ValueError("Runtime source leaves its declared directory")
    return path.read_bytes()


def verify_goja(root=ROOT):
    vendor = root / "apps/node-go/third_party/goja"
    source = json.loads(checked_file(vendor, "CANARY_PATCH.json"))
    commit = source.get("commit", "")
    if (source["module"] != "github.com/dop251/goja" or len(source["upstreamFiles"]) != 81
            or not re.fullmatch(r"[0-9a-f]{40}", commit)
            or not re.fullmatch(r"v0\.0\.0-\d{14}-" + commit[:12], source["version"])
            or source["addedFile"] != "canary_own_data.go"):
        raise ValueError("Invalid Goja source inventory")
    for name, digest in {**source["upstreamFiles"], source["addedFile"]: source["addedFileSHA256"]}.items():
        if hashlib.sha256(checked_file(vendor, name)).hexdigest() != digest:
            raise ValueError("Goja source hash mismatch: " + name)
    expected = set(source["upstreamFiles"]) | {source["addedFile"], "CANARY_PATCH.json", "NOTICE.txt"}
    actual = set()
    for path in vendor.rglob("*"):
        if path.is_symlink():
            raise ValueError("Goja inventory contains a symbolic link")
        if path.is_file():
            actual.add(path.relative_to(vendor).as_posix())
    if actual != expected:
        raise ValueError("Goja source contains missing or unrecorded files")
    return source


def verify(root=ROOT):
    runtime = root / "apps/node-go/internal/jsonquery/jsvm"
    meta = json.loads(checked_file(runtime, "upstream.json"))
    if meta["module"] != "jsonata" or meta["version"] != "2.2.2" or not re.fullmatch(r"[0-9a-f]{40}", meta["commit"]):
        raise ValueError("Invalid runtime identity")
    for name, key in (("jsonata.js", "bundleSha256"), ("jsonata-source.js", "canonicalSha256"),
                      ("jsonata-guard.patch", "patchSha256"), ("resource-official-jsonata.js", "unmodifiedSha256"),
                      ("evaluate.go", "bridgeSha256"), ("native_guard.go", "finiteGuardSha256"),
                      ("numeric_string.go", "numericGuardSha256")):
        if hashlib.sha256(checked_file(runtime, name)).hexdigest() != meta[key]:
            raise ValueError("Runtime source hash mismatch: " + name)
    verify_goja(root)
    print("Verified JSONata asset/callback hashes and 82 pinned Goja source files")


if __name__ == "__main__":
    verify()
