"""Build current source in place and run real-process proofs inside the storage guard.

One disposable binary and private fixture databases are deleted by the guard.
Only a small overwritten result is retained; no source snapshot is created.
"""
import argparse
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess
import time

from bounded_validation import source_digest
from validation_workspace import ROOT, guarded_output, retain_summary, tap_counts


def legacy_fixture(source, output):
    """Three declared historical overlays, not a copied source tree or fallback."""
    reference = "23e4b13cecd9a2bb7d393867cfa8a257b7325d36"
    selected = ["apps/node-go/internal/canary/wire_pages.go",
                "apps/node-go/internal/jsonquery/check.go",
                "apps/node-go/internal/jsonquery/number.go"]
    changed = subprocess.check_output(["git", "diff", reference, "--name-only", "--", "apps/node-go"], cwd=source, text=True).splitlines()
    changed += subprocess.check_output(["git", "ls-files", "--others", "--exclude-standard", "apps/node-go"], cwd=source, text=True).splitlines()
    unsupported = [p for p in changed if p.endswith(".go") and not p.endswith("_test.go")
        and p not in selected and not p.startswith(("apps/node-go/internal/jsonquery/jsvm/", "apps/node-go/third_party/goja/"))]
    if unsupported:
        raise ValueError("Shared application source diverged from the protocol-13 fixture: " + ", ".join(unsupported))
    overlay, hashes = {}, {}
    for index, path in enumerate(selected):
        raw = subprocess.check_output(["git", "show", reference + ":" + path], cwd=source)
        backing = output / ("legacy-source-" + str(index) + ".go")
        backing.write_bytes(raw)
        overlay[str(source / path)] = str(backing)
        hashes[path] = hashlib.sha256(raw).hexdigest()
    path = output / "legacy-overlay.json"
    path.write_text(json.dumps({"Replace": overlay}) + "\n", encoding="utf-8")
    return path, {"referenceCommit": reference, "overlaySha256": hashes,
        "scope": "Three historical Go files with unchanged shared application code and current readonly modules; controlled compatibility fixture, not an original release artifact"}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--context", type=Path, default=ROOT,
                        help="Existing source checkout; never copied or snapshotted")
    parser.add_argument("--test", action="append", help="Proof basename, e.g. push.test.ts")
    parser.add_argument("--upgrade", action="store_true", help="Only the controlled protocol-13-to-14 compatibility proof; requires retained Git history")
    args = parser.parse_args()
    output = guarded_output("process-proofs")
    source = args.context.resolve()
    go = ROOT / (".canary/tools/go/bin/go.exe" if os.name == "nt" else ".canary/tools/go/bin/go")
    node = shutil.which("node")
    if not go.is_file() or not node or not (ROOT / "node_modules/tsx/package.json").is_file():
        raise SystemExit("Existing pinned Go, Node and installed tsx are required")
    if not (source / "go.mod").is_file():
        raise SystemExit("A source checkout is required")
    tests = sorted((ROOT / "apps/proof/test").glob("*.test.ts"))
    if args.upgrade:
        if args.test:
            parser.error("--upgrade and --test cannot be combined")
        tests = [ROOT / "apps/proof/test/protocol-upgrade.compat.ts"]
    if args.test:
        if any(Path(name).name != name or not name.endswith(".test.ts") for name in args.test):
            raise SystemExit("--test requires a proof basename")
        selected = set(args.test)
        tests = [p for p in tests if p.name in selected]
        if {p.name for p in tests} != selected:
            raise SystemExit("Requested proof file is absent")
    if not tests:
        raise SystemExit("No process proof files were found")
    binary = output / ("canary-proof.exe" if os.name == "nt" else "canary-proof")
    env = {k: v for k, v in os.environ.items() if not k.startswith("CANARY_") and k != "NODE_OPTIONS"}
    env.update({"GOTOOLCHAIN": "local", "GOWORK": "off", "GOENV": "off", "CGO_ENABLED": "0",
                "GOPROXY": "off", "GOSUMDB": "off", "TSX_DISABLE_CACHE": "1",
                "GOOS": "windows" if os.name == "nt" else "linux", "GOARCH": "amd64"})
    before = source_digest(source)
    fixtures_before = source_digest(ROOT)
    report = {"source": str(source), "sourceBefore": before, "platform": platform.platform(),
              "proofs": [p.name for p in tests], "commands": {}, "releaseSignoff": False,
              "startedAt": datetime.now(timezone.utc).isoformat(), "passed": False}

    def run(label, command, cwd, timeout):
        print(label, flush=True)
        start = time.monotonic()
        # Inherit the guard's process group and cache/temporary environment.
        result = subprocess.run(command, cwd=cwd, env=env, stdout=subprocess.PIPE,
                                stderr=subprocess.STDOUT, timeout=timeout,
                                creationflags=subprocess.CREATE_NO_WINDOW if os.name == "nt" else 0)
        (output / (label + ".log")).write_bytes(result.stdout)
        print(result.stdout.decode("utf-8", errors="replace")[-128000:], flush=True)
        report["commands"][label] = {"command": command, "exitCode": result.returncode,
                                      "seconds": round(time.monotonic() - start, 3)}
        return result

    try:
        if args.upgrade:
            overlay, report["legacyFixture"] = legacy_fixture(source, output)
            legacy = output / ("canary-legacy.exe" if os.name == "nt" else "canary-legacy")
            built = run("build-legacy-fixture", [str(go), "build", "-mod=readonly", "-overlay", str(overlay),
                "-trimpath", "-o", str(legacy), "./apps/node-go/cmd/canary"], source, 180)
            if built.returncode:
                return built.returncode
            env["CANARY_LEGACY_BIN"] = str(legacy)
            report["legacySha256"] = hashlib.sha256(legacy.read_bytes()).hexdigest()
        built = run("build", [str(go), "build", "-mod=readonly", "-trimpath", "-o", str(binary),
                              "./apps/node-go/cmd/canary"], source, 180)
        if built.returncode:
            return built.returncode
        report["binarySha256"] = hashlib.sha256(binary.read_bytes()).hexdigest()
        env["CANARY_BIN"] = str(binary)
        checked = run("proofs", [node, "--import", "tsx", "--test", "--test-concurrency=1",
                                  "--test-reporter=tap", *map(str, tests)], ROOT, 600)
        report["counts"] = tap_counts(checked.stdout.decode("utf-8", errors="replace"))
        report["passed"] = checked.returncode == 0
    except (OSError, subprocess.TimeoutExpired, ValueError) as error:
        report["error"] = str(error)
    finally:
        report["sourceUnchanged"] = source_digest(source) == before
        report["fixturesUnchanged"] = source_digest(ROOT) == fixtures_before
        report["binaryUnchanged"] = not report.get("binarySha256") or (
            hashlib.sha256(binary.read_bytes()).hexdigest() == report["binarySha256"])
        report["passed"] = report["passed"] and all(report[k] for k in (
            "sourceUnchanged", "fixturesUnchanged", "binaryUnchanged"))
        report["finishedAt"] = datetime.now(timezone.utc).isoformat()
        retain_summary("upgrade-proofs" if args.upgrade else "process-proofs", report)
        print(json.dumps(report, indent=2), flush=True)
    return 0 if report["passed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
