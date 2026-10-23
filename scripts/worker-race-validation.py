"""Race-instrumented parents with an uninstrumented, capped production worker.

No test is skipped and no worker limit is increased. The child artifact is built
without race instrumentation; the report explicitly preserves that boundary.
Requires an existing native C compiler and Go. No installation is performed.
"""
import argparse
from datetime import datetime, timezone
import hashlib
import importlib.util
import json
import os
import platform
from pathlib import Path
import shutil
import signal
import subprocess

ROOT = Path(__file__).resolve().parents[1]
_paths_spec = importlib.util.spec_from_file_location("canary_validation_paths", Path(__file__).with_name("validation_paths.py"))
_paths_module = importlib.util.module_from_spec(_paths_spec)
_paths_spec.loader.exec_module(_paths_module)
filesystem_path = _paths_module.filesystem_path


def fingerprints(source):
    source = filesystem_path(source)
    paths = [source / "go.mod", source / "go.sum"]
    paths += sorted((source / "apps/node-go").rglob("*.go"))
    paths += sorted((source / "apps/node-go").rglob("*.json"))
    paths += sorted((source / "apps/node-go").rglob("*.js"))
    return {p.relative_to(source).as_posix(): hashlib.sha256(p.read_bytes()).hexdigest() for p in paths}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--context", type=Path, default=ROOT)
    parser.add_argument("--run", help="Optional focused test expression")
    parser.add_argument("--core", action="store_true", help="Instrument the full Canary parent package with the same capped production worker")
    args = parser.parse_args()
    if platform.system() not in ("Windows", "Linux") or platform.machine().lower() not in ("amd64", "x86_64"):
        raise SystemExit("This runner currently requires native Windows/Linux amd64")
    source = args.context.resolve()
    if not (source / "go.mod").is_file():
        raise SystemExit("A Canary source context is required")
    suffix = ".exe" if os.name == "nt" else ""
    portable = ROOT / (".canary/tools/go/bin/go" + suffix)
    go = str(portable) if portable.is_file() else shutil.which("go")
    if not go:
        raise SystemExit("Go is required")
    env = dict(os.environ)
    if os.name == "nt":
        spec = importlib.util.spec_from_file_location("native_race", ROOT / "scripts/windows-race-validation.py")
        native = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(native)
        env = native.environment()  # Existing verified compiler and short AF_UNIX fixture paths.
    env.update({"GOOS": "windows" if os.name == "nt" else "linux", "GOARCH": "amd64",
                "GOTOOLCHAIN": "local", "GOWORK": "off", "GOFLAGS": "-mod=readonly"})
    output = ROOT / ".canary/worker-race" / datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%S%fZ")
    output.mkdir(parents=True, exist_ok=False)
    worker = output / ("canary-worker" + suffix)
    before = fingerprints(source)
    scope = "Canary core" if args.core else "checks/jsonquery/services/probeworker/process"
    report = {"source": str(source), "sourceBefore": before, "checks": {},
              "runnerSha256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
              "scope": "Parent " + scope + " is race instrumented; production worker child is not. Fixed memory/CPU/protocol controls unchanged."}

    def run(label, command, overrides=None):
        print(label, flush=True)
        child = subprocess.Popen(command, cwd=source, env={**env, **(overrides or {})},
                                 stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                                 creationflags=subprocess.CREATE_NO_WINDOW if os.name == "nt" else 0,
                                 start_new_session=os.name != "nt")
        record = {"command": command, "pid": child.pid,
                  "startedAt": datetime.now(timezone.utc).isoformat()}
        report["checks"][label] = record
        (output / "summary.json").write_text(json.dumps(report, indent=2) + "\n")
        try:
            raw, _ = child.communicate(timeout=1800 if args.core else 600)
        except BaseException:
            # Terminate only the exact process tree/session created above.
            if child.poll() is None:
                if os.name == "nt":
                    subprocess.run(["taskkill.exe", "/PID", str(child.pid), "/T", "/F"],
                                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                                   creationflags=subprocess.CREATE_NO_WINDOW)
                else:
                    os.killpg(child.pid, signal.SIGKILL)
            raw, _ = child.communicate()
            (output / (label + ".log")).write_bytes(raw)
            record.update({"exitCode": child.returncode, "interrupted": True})
            (output / "summary.json").write_text(json.dumps(report, indent=2) + "\n")
            raise
        result = subprocess.CompletedProcess(command, child.returncode, stdout=raw)
        (output / (label + ".log")).write_bytes(raw)
        record.update({"exitCode": result.returncode, "finishedAt": datetime.now(timezone.utc).isoformat()})
        (output / "summary.json").write_text(json.dumps(report, indent=2) + "\n")
        return result

    build = run("build-production-worker", [go, "build", "-mod=readonly", "-trimpath", "-o", str(worker),
                                            "./apps/node-go/cmd/canary"], {"CGO_ENABLED": "0"})
    if build.returncode:
        (output / "summary.json").write_text(json.dumps(report, indent=2) + "\n")
        raise SystemExit("Production worker build failed; see " + str(output))
    report["workerSha256"] = hashlib.sha256(worker.read_bytes()).hexdigest()
    run("worker-build-info", [go, "version", "-m", str(worker)])
    fixture = output / ("process-fixture" + suffix)
    built_fixture = run("build-process-fixture", [go, "test", "-c", "-mod=readonly", "-o", str(fixture),
                                                   "./apps/node-go/internal/probeworker/process"], {"CGO_ENABLED": "0"})
    if built_fixture.returncode:
        (output / "summary.json").write_text(json.dumps(report, indent=2) + "\n")
        raise SystemExit("Process fixture build failed; see " + str(output))
    report["processFixtureSha256"] = hashlib.sha256(fixture.read_bytes()).hexdigest()
    assignment = "uptime-canary/apps/node-go/internal/probeworker/process.raceWorkerExecutable=" + worker.as_posix()
    fixture_assignment = "uptime-canary/apps/node-go/internal/probeworker/process.raceFixtureExecutable=" + fixture.as_posix()
    flags = "-X " + json.dumps(assignment) + " -X " + json.dumps(fixture_assignment)
    command = [go, "test", "-race", "-tags=canary_worker_race", "-ldflags", flags,
               "-json", "-count=1", "-timeout=20m" if args.core else "-timeout=5m"]
    if args.run:
        command += ["-run", args.run]
    command += ["./apps/node-go/internal/canary"] if args.core else ["./apps/node-go/internal/checks", "./apps/node-go/internal/jsonquery", "./apps/node-go/internal/services", "./apps/node-go/internal/probeworker", "./apps/node-go/internal/probeworker/process"]
    result = run("race-parents", command, {"CGO_ENABLED": "1"})
    events = []
    for line in result.stdout.decode("utf-8", errors="replace").splitlines():
        try:
            events.append(json.loads(line))
        except json.JSONDecodeError:
            pass
    report.update({"sourceUnchanged": fingerprints(source) == before,
                   "workerUnchanged": hashlib.sha256(worker.read_bytes()).hexdigest() == report["workerSha256"],
                   "fixtureUnchanged": hashlib.sha256(fixture.read_bytes()).hexdigest() == report["processFixtureSha256"],
                   "dataRaceWarnings": result.stdout.count(b"WARNING: DATA RACE"),
                   "passedTestEvents": sum(e.get("Action") == "pass" and "Test" in e for e in events),
                   "failedTests": [e for e in events if e.get("Action") == "fail" and "Test" in e],
                   "skippedTests": [e for e in events if e.get("Action") == "skip" and "Test" in e]})
    report["passed"] = result.returncode == 0 and report["sourceUnchanged"] and report["workerUnchanged"] and report["fixtureUnchanged"] and not report["dataRaceWarnings"]
    (output / "summary.json").write_text(json.dumps(report, indent=2) + "\n")
    print(json.dumps({"evidence": str(output), **{k: report[k] for k in ("passed", "sourceUnchanged", "passedTestEvents", "dataRaceWarnings", "failedTests", "skippedTests")}}, indent=2), flush=True)
    raise SystemExit(0 if report["passed"] else 1)


if __name__ == "__main__":
    main()
