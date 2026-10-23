"""Compile an isolated source snapshot and run the real-process proof suite.

Source, fixture and binary fingerprints are checked before and after execution.
Only private proof databases/ports are used; the development node is untouched.
"""
import argparse
from datetime import datetime, timezone
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import platform
import re
import shutil
import signal
import subprocess

ROOT = Path(__file__).resolve().parents[1]
_paths_spec = importlib.util.spec_from_file_location("canary_validation_paths", Path(__file__).with_name("validation_paths.py"))
_paths_module = importlib.util.module_from_spec(_paths_spec)
_paths_spec.loader.exec_module(_paths_module)
filesystem_path = _paths_module.filesystem_path


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def fingerprints(folder):
    folder = filesystem_path(folder)
    return {p.relative_to(folder).as_posix(): sha(p)
            for p in sorted(folder.rglob("*")) if p.is_file()}


def write_json(path, value):
    path.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def fixture_fingerprints():
    paths = sorted(p for p in (ROOT / "apps/proof").rglob("*")
                   if p.is_file() and p.suffix in {".ts", ".json", ".pem"}
                   and not any(part in {"node_modules", "dist"} for part in p.parts))
    paths += sorted((ROOT / "packages/shared/src").rglob("*.ts"))
    return {p.relative_to(ROOT).as_posix(): sha(p) for p in paths}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--context", type=Path, help="Existing immutable source snapshot")
    parser.add_argument("--test", action="append", help="Proof basename, e.g. public-metadata.test.ts")
    args = parser.parse_args()
    if platform.system() not in ("Windows", "Linux") or platform.machine().lower() not in ("amd64", "x86_64"):
        raise SystemExit("This runner currently requires native Windows/Linux amd64")
    spec = importlib.util.spec_from_file_location("snapshot_runner", ROOT / "scripts/windows-race-validation.py")
    capture_runner = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(capture_runner)
    source = args.context.resolve() if args.context else capture_runner.capture()
    manifest_path = source.parent / "source-manifest.json"
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    if fingerprints(source) != manifest:
        raise SystemExit("Immutable source manifest mismatch")
    go = ROOT / (".canary/tools/go/bin/go.exe" if os.name == "nt" else ".canary/tools/go/bin/go")
    node = shutil.which("node")
    if not go.is_file() or not node:
        raise SystemExit("Pinned Go and Node are required")
    if not (ROOT / "node_modules/tsx/package.json").is_file():
        raise SystemExit("Run the documented npm installation before proof validation")
    fixture_before = fixture_fingerprints()
    output = ROOT / ".canary/process-proofs" / datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%S%fZ")
    output.mkdir(parents=True, exist_ok=False)
    binary = output / ("canary-proof.exe" if os.name == "nt" else "canary-proof")
    env = {k: v for k, v in os.environ.items() if not k.startswith("CANARY_") and k != "NODE_OPTIONS"}
    env.update({"GOTOOLCHAIN": "local", "GOWORK": "off", "GOENV": "off", "GOFLAGS": "-mod=readonly",
                "CGO_ENABLED": "0", "GOPROXY": "off", "GOSUMDB": "off",
                "GOOS": "windows" if os.name == "nt" else "linux", "GOARCH": "amd64"})
    cache = ROOT / ".canary/alpine-validation/windows-race/cache"
    if (cache / "modules").is_dir():
        env["GOMODCACHE"] = str(cache / "modules")
    report = {"source": str(source), "manifestSha256": sha(manifest_path),
              "fixturesBefore": fixture_before, "commands": {}, "releaseSignoff": False,
              "runnerSha256": sha(Path(__file__)), "platform": platform.platform()}

    def run(label, command, cwd, timeout):
        print(label, flush=True)
        log_path = output / (label + ".log")
        record = {"command": command, "cwd": str(cwd)}
        with log_path.open("wb") as stream:
            process = subprocess.Popen(command, cwd=cwd, env=env, stdout=stream, stderr=subprocess.STDOUT,
                                       creationflags=subprocess.CREATE_NO_WINDOW if os.name == "nt" else 0,
                                       start_new_session=os.name != "nt")
            record["pid"] = process.pid
            report["commands"][label] = record
            write_json(output / "summary.json", report)
            try:
                record["exitCode"] = process.wait(timeout=timeout)
            except BaseException:
                if os.name == "nt":
                    subprocess.run(["taskkill.exe", "/PID", str(process.pid), "/T", "/F"],
                                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                                   creationflags=subprocess.CREATE_NO_WINDOW)
                else:
                    os.killpg(process.pid, signal.SIGKILL)
                process.wait()
                record["interrupted"] = True
                write_json(output / "summary.json", report)
                raise
        write_json(output / "summary.json", report)
        return record["exitCode"]

    try:
        code = run("build", [str(go), "build", "-mod=readonly", "-trimpath", "-o", str(binary),
                             "./apps/node-go/cmd/canary"], source, 600)
        if code:
            raise SystemExit("Proof binary build failed: " + str(output))
        report["binarySha256"] = sha(binary)
        tests = sorted((ROOT / "apps/proof/test").glob("*.test.ts"))
        if args.test:
            if any(Path(name).name != name or not name.endswith(".test.ts") for name in args.test):
                raise SystemExit("--test requires a proof basename")
            selected = set(args.test)
            tests = [p for p in tests if p.name in selected]
            if {p.name for p in tests} != selected:
                raise SystemExit("Requested proof file is absent")
        if not tests:
            raise SystemExit("No process proof files were found")
        env["CANARY_BIN"] = str(binary)
        code = run("proofs", [node, "--import", "tsx", "--test", "--test-reporter=tap", *map(str, tests)], ROOT, 600)
        tap = (output / "proofs.log").read_text(encoding="utf-8", errors="replace")
        report["counts"] = {name: int(value) for name, value in
                            re.findall(r"^# (tests|pass|fail|cancelled|skipped|todo) (\d+)\s*$", tap, re.M)}
        if code == 0 and (report["counts"].get("tests", 0) == 0 or report["counts"].get("fail") != 0):
            code = 1
            report["outputError"] = "Missing or inconsistent TAP counts"
        report["result"] = "pass" if code == 0 else "fail"
    finally:
        report["sourceUnchanged"] = fingerprints(source) == manifest
        report["fixturesUnchanged"] = fixture_fingerprints() == fixture_before
        report["binaryUnchanged"] = not report.get("binarySha256") or sha(binary) == report["binarySha256"]
        report["finishedAt"] = datetime.now(timezone.utc).isoformat()
        write_json(output / "summary.json", report)
        print(str(output), flush=True)
    if not all(report[k] for k in ("sourceUnchanged", "fixturesUnchanged", "binaryUnchanged")):
        raise SystemExit("Validation inputs changed")
    raise SystemExit(code)


if __name__ == "__main__":
    main()
