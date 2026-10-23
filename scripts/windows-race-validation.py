"""Run native Windows race tests against an isolated, checksummed source copy.

Requires the already verified portable w64devkit in .canary/tools/race-gcc.
No installer, persistent environment changes, user data or fixed ports are used.
"""
import argparse
import ctypes
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import platform
import subprocess
import tarfile
import time
from datetime import datetime, timezone

ROOT = Path(__file__).resolve().parents[1]
_paths_spec = importlib.util.spec_from_file_location("canary_validation_paths", Path(__file__).with_name("validation_paths.py"))
_paths_module = importlib.util.module_from_spec(_paths_spec)
_paths_spec.loader.exec_module(_paths_module)
filesystem_path = _paths_module.filesystem_path
BASE = ROOT / ".canary/alpine-validation/windows-race"
GO = ROOT / ".canary/tools/go/bin/go.exe"
CC = ROOT / ".canary/tools/race-gcc/extracted/w64devkit/bin/gcc.exe"


def utc():
    return datetime.now(timezone.utc).isoformat()


def write_json(path, value):
    path.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def capture():
    # Use exactly the checked-in Alpine harness's source selection without
    # executing its output-writing section or replacing earlier evidence.
    script = ROOT / "scripts/alpine-validation-snapshot.py"
    program = script.read_text(encoding="utf-8")
    prefix, marker, _ = program.partition("\nfor attempt in range(5):")
    if not marker:
        raise SystemExit("Snapshot script changed: review its capture logic first")
    namespace = {"__file__": str(script)}
    exec(compile(prefix, str(script), "exec"), namespace)
    paths = namespace["paths"]
    for _ in range(5):
        selected = paths()
        data = {p.relative_to(ROOT).as_posix(): p.read_bytes() for p in selected}
        time.sleep(2)
        if selected == paths() and all(p.read_bytes() == data[p.relative_to(ROOT).as_posix()] for p in selected):
            break
    else:
        raise SystemExit("Source kept changing; no snapshot captured")
    manifest = {name: hashlib.sha256(content).hexdigest() for name, content in data.items()}
    encoded = (json.dumps(manifest, indent=2, sort_keys=True) + "\n").encode()
    digest = hashlib.sha256(encoded).hexdigest()
    destination = BASE / "snapshots" / digest
    source = destination / "source"
    source.mkdir(parents=True, exist_ok=True)
    for name, content in data.items():
        path = filesystem_path(source / name)
        if path.exists() and path.read_bytes() != content:
            raise SystemExit("Existing immutable snapshot was modified")
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(content)
    (destination / "source-manifest.json").write_bytes(encoded)
    archive = destination / "source.tar.gz"
    if not archive.exists():
        with tarfile.open(archive, "w:gz") as output:
            for name, content in data.items():
                info = tarfile.TarInfo(name)
                info.size, info.mode, info.mtime = len(content), 0o644, 0
                output.addfile(info, io.BytesIO(content))
    write_json(destination / "capture.json", {
        "captured_at": utc(), "manifest_sha256": digest, "file_count": len(data),
        "source_bytes": sum(map(len, data.values())),
        "archive_sha256": hashlib.file_digest(archive.open("rb"), "sha256").hexdigest(),
        "selection_script_sha256": hashlib.sha256(program.encode()).hexdigest(),
        "stability_seconds": 2,
        "fixture_scope": "Go fixtures generate certificates/keys inline; proof PEM fixtures are excluded",
    })
    return source


def environment():
    if os.name != "nt" or not CC.is_file() or not GO.is_file():
        raise SystemExit("Native Windows, portable Go and verified extracted GCC are required")
    # AF_UNIX's Windows path limit also applies to test fixtures. Keep test
    # temporary paths short without drive mappings, junctions or global changes.
    # This still resolves to the owned compiler directory, and testing.TempDir
    # removes its own per-test subdirectories. The compiler cache is retained.
    temp_root = ROOT / ".canary/tools/race-gcc"
    short_path = ctypes.windll.kernel32.GetShortPathNameW
    short_path.argtypes = (ctypes.c_wchar_p, ctypes.c_wchar_p, ctypes.c_uint)
    short_path.restype = ctypes.c_uint
    buffer = ctypes.create_unicode_buffer(32768)
    if not short_path(str(temp_root), buffer, len(buffer)):
        raise SystemExit("Could not resolve the owned short test temporary path")
    test_temp = buffer.value
    if not os.path.samefile(test_temp, temp_root):
        raise SystemExit("Short temporary path did not resolve to the owned directory")
    env = dict(os.environ)
    env.update({"CC": str(CC), "CGO_ENABLED": "1", "GOOS": "windows", "GOARCH": "amd64",
                "GOENV": "off", "GOTOOLCHAIN": "local", "GOWORK": "off", "GOFLAGS": "-mod=readonly",
                "GOCACHE": str(BASE / "cache/build"), "GOMODCACHE": str(BASE / "cache/modules"),
                "GOPATH": str(BASE / "cache/gopath"), "GOTMPDIR": test_temp,
                "TEMP": test_temp, "TMP": test_temp,
                "GORACE": "", "PATH": str(CC.parent) + os.pathsep + env.get("PATH", "")})
    for directory in (BASE / "cache/build", BASE / "cache/modules", BASE / "cache/gopath", BASE / "tmp"):
        directory.mkdir(parents=True, exist_ok=True)
    return env


def invoke(command, cwd, env, output):
    print(subprocess.list2cmdline(list(map(str, command))), flush=True)
    record = {"command": list(map(str, command)), "cwd": str(cwd), "started_at": utc()}
    with output.open("wb") as stream:
        process = subprocess.Popen(command, cwd=cwd, env=env, stdout=stream, stderr=subprocess.STDOUT,
                                   creationflags=subprocess.CREATE_NO_WINDOW)
        record["pid"] = process.pid
        write_json(output.with_suffix(".command.json"), record)
        try:
            record["exit_code"] = process.wait()
        except BaseException:
            # Only this exact owned process tree is terminated on interruption.
            subprocess.run(["taskkill.exe", "/PID", str(process.pid), "/T", "/F"],
                           stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                           creationflags=subprocess.CREATE_NO_WINDOW)
            process.wait()
            raise
    record["finished_at"] = utc()
    write_json(output.with_suffix(".command.json"), record)
    return record


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--snapshot", type=Path, help="Reuse an existing owned immutable source directory")
    parser.add_argument("--label", default="full")
    parser.add_argument("--run", help="Optional Go test name expression; default is the entire suite")
    parser.add_argument("--package", action="append", help="Go package to test; repeat for separate packages")
    parser.add_argument("--count", default="1")
    parser.add_argument("--timeout", default="10m")
    parser.add_argument("--capture-only", action="store_true")
    parser.add_argument("--no-race", action="store_true", help="Diagnostic comparison with the same fixture assertions")
    args = parser.parse_args()
    BASE.mkdir(parents=True, exist_ok=True)
    source = (args.snapshot.resolve() if args.snapshot else capture())
    if not source.is_relative_to((BASE / "snapshots").resolve()) or source.name != "source":
        raise SystemExit("Snapshot must be an owned source directory")
    print("SOURCE=" + str(source), flush=True)
    if args.capture_only:
        return
    env = environment()
    output = BASE / "runs" / (datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ") + "-" + args.label)
    output.mkdir(parents=True, exist_ok=False)
    manifest_path = source.parent / "source-manifest.json"
    manifest = json.loads(manifest_path.read_text())
    io_source = filesystem_path(source)
    before = {p.relative_to(io_source).as_posix(): hashlib.sha256(p.read_bytes()).hexdigest()
              for p in io_source.rglob("*") if p.is_file()}
    if before != manifest:
        raise SystemExit("Source manifest did not match before test")
    metadata = {"started_at": utc(), "platform": platform.platform(), "source": str(source),
                "runner_sha256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
                "manifest_sha256": hashlib.sha256(manifest_path.read_bytes()).hexdigest(),
                "environment_overrides": {key: env[key] for key in (
                    "CC", "CGO_ENABLED", "GOOS", "GOARCH", "GOENV", "GOTOOLCHAIN", "GOWORK", "GOFLAGS",
                    "GOCACHE", "GOMODCACHE", "GOPATH", "GOTMPDIR", "TEMP", "TMP", "GORACE")},
                "temporary_path_prefix": str(CC.parent)}
    for label, command in (("go-version", [GO, "version"]), ("gcc-version", [CC, "--version"]),
                           ("gcc-sync-library", [CC, "--print-file-name", "libsynchronization.a"]),
                           ("go-env", [GO, "env", "-json", "GOOS", "GOARCH", "CGO_ENABLED", "CC", "GOVERSION"])):
        invoke(command, source, env, output / (label + ".log"))
    command = [GO, "test"] + ([] if args.no_race else ["-race"])
    command += ["-json", "-count=" + args.count, "-timeout=" + args.timeout]
    if args.run:
        command += ["-run", args.run]
    command += args.package or ["./apps/node-go/..."]
    metadata["test"] = invoke(command, source, env, output / "race.jsonl")
    after = {p.relative_to(io_source).as_posix(): hashlib.sha256(p.read_bytes()).hexdigest()
             for p in io_source.rglob("*") if p.is_file()}
    metadata["snapshot_unchanged"] = before == after
    metadata["finished_at"] = utc()
    raw = (output / "race.jsonl").read_text(encoding="utf-8", errors="replace")
    metadata["data_race_warnings"] = raw.count("WARNING: DATA RACE")
    events = []
    for line in raw.splitlines():
        try:
            events.append(json.loads(line))
        except json.JSONDecodeError:
            pass
    metadata["packages"] = [e for e in events if e.get("Action") in ("pass", "fail", "skip") and "Test" not in e]
    metadata["failed_tests"] = [e for e in events if e.get("Action") == "fail" and "Test" in e]
    metadata["skipped_tests"] = [e for e in events if e.get("Action") == "skip" and "Test" in e]
    metadata["passed_test_events"] = sum(e.get("Action") == "pass" and "Test" in e for e in events)
    write_json(output / "summary.json", metadata)
    print(json.dumps(metadata, indent=2), flush=True)
    if not metadata["snapshot_unchanged"]:
        raise SystemExit("Snapshot changed during test")
    raise SystemExit(metadata["test"]["exit_code"])


if __name__ == "__main__":
    main()
