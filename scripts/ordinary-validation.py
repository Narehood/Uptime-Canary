"""Run ordinary Go tests against an immutable source context and retain evidence."""
import argparse
from datetime import datetime, timezone
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import time

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("validation_paths", ROOT / "scripts/validation_paths.py")
paths = importlib.util.module_from_spec(spec)
spec.loader.exec_module(paths)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--context", type=Path, required=True)
    parser.add_argument("--package", action="append", help="Package below ./apps/node-go; default all node packages")
    args = parser.parse_args()
    source = args.context.resolve()
    packages = args.package or ["./apps/node-go/..."]
    if any(not p.startswith("./apps/node-go/") or ".." in p.removesuffix("/...").split("/")[1:] for p in packages):
        raise SystemExit("Only node packages are supported")
    manifest_path = source.parent / "source-manifest.json"
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))

    def unchanged():
        folder = paths.filesystem_path(source)
        return {p.relative_to(folder).as_posix(): hashlib.sha256(p.read_bytes()).hexdigest()
                for p in folder.rglob("*") if p.is_file()} == manifest

    if not unchanged():
        raise SystemExit("Source context differs from its immutable manifest")
    suffix = ".exe" if os.name == "nt" else ""
    go = ROOT / (".canary/tools/go/bin/go" + suffix)
    command = [str(go), "test", "-mod=readonly", "-json", *packages, "-count=1"]
    env = {k: v for k, v in os.environ.items() if not k.startswith("CANARY_")}
    env.update(CGO_ENABLED="0", GOTOOLCHAIN="local", GOWORK="off", GOFLAGS="-mod=readonly", GOPROXY="off", GOSUMDB="off")
    output = ROOT / ".canary/ordinary-checks" / datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%S%fZ")
    output.mkdir(parents=True, exist_ok=False)
    report = {"source": str(source), "sourceManifest": hashlib.sha256(manifest_path.read_bytes()).hexdigest(),
              "command": command, "packages": packages, "cwd": str(source), "releaseSignoff": False,
              "runnerSha256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest()}

    def save():
        (output / "summary.json").write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8")

    started = time.monotonic()
    with (output / "tests.jsonl").open("wb") as log:
        # Extended paths are needed for reading a few deep fixture names. Use
        # the ordinary source path as cwd so filepath.Rel sees the same Windows
        # volume spelling as os.TempDir inside the unchanged tests.
        child = subprocess.Popen(command, cwd=source, env=env, stdout=log, stderr=subprocess.STDOUT,
                                 creationflags=subprocess.CREATE_NO_WINDOW if os.name == "nt" else 0,
                                 start_new_session=os.name != "nt")
        report["pid"] = child.pid
        save()
        try:
            code = child.wait(timeout=1800)
        except BaseException:
            if child.poll() is None:
                if os.name == "nt":
                    subprocess.run(["taskkill.exe", "/PID", str(child.pid), "/T", "/F"], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
                else:
                    import signal
                    os.killpg(child.pid, signal.SIGTERM)
                child.wait(timeout=10)
            raise
    events = {key: 0 for key in ("pass", "fail", "skip")}
    failed, skipped = [], []
    for line in (output / "tests.jsonl").read_text(encoding="utf-8", errors="replace").splitlines():
        try:
            item = json.loads(line)
        except ValueError:
            continue
        action = item.get("Action")
        if "Test" in item and action in events:
            events[action] += 1
        if action == "fail":
            failed.append({"package": item.get("Package"), "test": item.get("Test")})
        if action == "skip" and "Test" in item:
            skipped.append({"package": item.get("Package"), "test": item["Test"]})
    report.update(exitCode=code, elapsedSeconds=round(time.monotonic()-started, 3), events=events,
                  failures=failed, skips=skipped, sourceUnchanged=unchanged())
    save()
    print(json.dumps({"evidence": str(output), **report}))
    raise SystemExit(code if report["sourceUnchanged"] else 1)


if __name__ == "__main__":
    main()
