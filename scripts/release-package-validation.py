"""Validate an actual clean-checkout build and archive inside the storage guard.

No release assets are retained or published. Cross-target checks never claim
native execution; native checks run the extracted executable and one HA proof.
"""
import argparse
from datetime import datetime, timezone
import importlib.util
import json
import os
from pathlib import Path
import platform
import re
import shutil
import subprocess
import sys
import tarfile
import zipfile

from bounded_validation import source_digest
from validation_workspace import ROOT, guarded_output, retain_summary, tap_counts

spec = importlib.util.spec_from_file_location("release_package", Path(__file__).with_name("package-release.py"))
packager = importlib.util.module_from_spec(spec)
spec.loader.exec_module(packager)
MAIN = "uptime-canary/apps/node-go/cmd/canary"
VERSION_SYMBOL = "uptime-canary/apps/node-go/internal/canary.releaseVersion"


def native_target(system, machine):
    architecture = {"amd64": "amd64", "x86_64": "amd64", "arm64": "arm64", "aarch64": "arm64"}.get(machine.lower())
    return system.lower() + "-" + (architecture or "unknown")


def execution_policy(target, host, require_native, full_proofs, alpine_help):
    native = target == host
    if (require_native or full_proofs or alpine_help) and not native:
        raise ValueError("Native execution was required but the host target does not match")
    if alpine_help and not host.startswith("linux-"):
        raise ValueError("Alpine help validation requires a native Linux host")
    return native


def clean_checkout(commit, root=ROOT):
    if not re.fullmatch(r"[0-9a-f]{40}", commit):
        raise ValueError("An exact lowercase source commit is required")
    def git(*args):
        return subprocess.check_output(["git", *args], cwd=root, text=True).strip()
    if Path(git("rev-parse", "--show-toplevel")).resolve() != root.resolve():
        raise ValueError("The source directory must be the repository root")
    if git("rev-parse", "HEAD") != commit:
        raise ValueError("The checkout does not match the requested source commit")
    if git("status", "--porcelain=v1", "--untracked-files=all"):
        raise ValueError("A clean checkout, including nonignored untracked files, is required")


def verify_build_info(raw, target, commit, toolchain):
    lines = raw.splitlines()
    if not lines or not lines[0].endswith(": " + toolchain):
        raise ValueError("Unexpected executable toolchain")
    fields, settings = {}, {}
    for line in lines[1:]:
        parts = line.strip().split("\t")
        if parts[0] in ("path", "mod"):
            if parts[0] in fields:
                raise ValueError("Duplicate executable identity")
            fields[parts[0]] = parts[1:]
        elif parts[0] == "build":
            key, separator, value = parts[1].partition("=") if len(parts) == 2 else ("", "", "")
            if not separator or key in settings:
                raise ValueError("Invalid or duplicate build setting")
            settings[key] = value
    goos, goarch = target.split("-")
    expected = {"GOOS": goos, "GOARCH": goarch, "CGO_ENABLED": "0", "-trimpath": "true",
                "-buildmode": "exe", "vcs": "git", "vcs.revision": commit, "vcs.modified": "false"}
    module = fields.get("mod", [])
    module_version = module[1] if len(module) >= 2 else ""
    # Go 1.27 derives a module version from VCS when available. The full VCS
    # revision and clean flag below bind source; '(devel)' is not mandatory.
    valid_version = module_version == "(devel)" or re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.+-]+)?", module_version)
    if (fields.get("path") != [MAIN] or not module or module[0] != "uptime-canary" or not valid_version
            or any(settings.get(k) != value for k, value in expected.items())):
        raise ValueError("Executable source identity, target or build policy mismatch")
    settings["mainModuleVersion"] = module_version
    return settings


def verify_runtime_identity(raw, target, version, commit, toolchain, protocol):
    identity = json.loads(raw)
    goos, goarch = target.split("-")
    expected = {"name": "Uptime-Canary", "version": packager.version_value(version), "sourceCommit": commit,
                "goVersion": toolchain, "os": goos, "arch": goarch, "peerProtocol": protocol}
    if (not isinstance(identity, dict) or any(identity.get(k) != value for k, value in expected.items())
            or identity.get("modified") is not False):
        raise ValueError("The extracted executable reported unexpected identity")
    return identity


def verify_notice_coverage(raw, manifest, toolchain):
    recorded = {(p["name"], p["version"]) for p in manifest["packages"]}
    linked = {tuple(line.split()) for line in raw.splitlines() if line.strip()}
    if any(len(pair) != 2 for pair in linked):
        raise ValueError("Unrecognized linked dependency inventory")
    missing = linked.union({("Go standard library", toolchain)}) - recorded
    if missing:
        raise ValueError("Target dependency notices missing: " + ", ".join(sorted(name for name, _ in missing)))
    return len(linked)


def unpack_binary(result, destination, target, version, commit):
    archive = Path(result["archive"])
    if packager.digest_file(archive)[0] != result["sha256"]:
        raise ValueError("Archive checksum mismatch")
    if Path(result["checksum"]).read_bytes() != (result["sha256"] + "  " + archive.name + "\n").encode("ascii"):
        raise ValueError("Archive checksum sidecar mismatch")
    folder = "uptime-canary-v" + packager.version_value(version) + "-" + target
    name = "canary.exe" if target.startswith("windows-") else "canary"
    expected = {folder + "/" + item for item in (name, "LICENSE", "THIRD_PARTY_NOTICES.txt", "release.json")}
    opener = zipfile.ZipFile if target.startswith("windows-") else tarfile.open
    with opener(archive) as package:
        names = package.namelist() if isinstance(package, zipfile.ZipFile) else package.getnames()
        if len(names) != 4 or set(names) != expected:
            raise ValueError("Unexpected or duplicate archive member")
        def read(member):
            return package.open(member) if isinstance(package, zipfile.ZipFile) else package.extractfile(member)
        with read(folder + "/release.json") as source:
            metadata = json.loads(source.read(65537))
        if (metadata.get("version") != packager.version_value(version) or metadata.get("sourceCommit") != commit
                or metadata.get("target") != target or metadata["files"][name]["sha256"] != result["binarySha256"]):
            raise ValueError("Archive source metadata mismatch")
        for license_name in ("LICENSE", "THIRD_PARTY_NOTICES.txt"):
            with read(folder + "/" + license_name) as source:
                if source.read(1048577) != (ROOT / license_name).read_bytes():
                    raise ValueError("Archive license content mismatch")
        with read(folder + "/" + name) as source, destination.open("xb") as output:
            shutil.copyfileobj(source, output, 1048576)
    destination.chmod(0o700)
    digest, size = packager.digest_file(destination)
    if digest != result["binarySha256"] or size != metadata["files"][name]["bytes"]:
        raise ValueError("Extracted binary checksum or size mismatch")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--commit", required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--target", choices=packager.TARGETS, required=True)
    parser.add_argument("--require-native", action="store_true", help="Refuse cross-build-only acceptance")
    parser.add_argument("--full-proofs", action="store_true", help="Run every ordinary process proof on the extracted native executable")
    parser.add_argument("--alpine-help", action="store_true", help="Also execute extracted CLI help in a pre-pulled restricted Alpine container")
    parser.add_argument("--pm2-interop", action="store_true", help="Require offline actual PM2 interoperability on native hosted Linux")
    parser.add_argument("--browser-interop", action="store_true", help="Require sandboxed actual Chromium acceptance on native hosted Linux")
    parser.add_argument("--coordinated-upgrade", action="store_true", help="Require upgrade from the pinned public protocol18 Linux amd64 executable")
    args = parser.parse_args()
    native = execution_policy(args.target, native_target(platform.system(), platform.machine()),
                              args.require_native, args.full_proofs, args.alpine_help)
    if args.pm2_interop and (not native or not args.target.startswith("linux-")):
        raise ValueError("PM2 interoperability requires native Linux")
    if args.browser_interop and (not native or not args.target.startswith("linux-")):
        raise ValueError("Browser interoperability requires native Linux")
    if args.coordinated_upgrade and (not native or args.target != "linux-amd64"):
        raise ValueError("Published-baseline upgrade requires native Linux amd64")
    clean_checkout(args.commit)
    args.version = packager.version_value(args.version)
    output = guarded_output("release-package")
    go = ROOT / (".canary/tools/go/bin/go.exe" if os.name == "nt" else ".canary/tools/go/bin/go")
    if not go.is_file():
        raise SystemExit("The existing pinned Go toolchain is required")
    env = {k: v for k, v in os.environ.items() if not k.startswith(("CANARY_", "GO")) and k != "NODE_OPTIONS"}
    env.update({k: v for k, v in os.environ.items() if k in ("GOCACHE", "GOTMPDIR", "GOMODCACHE", "GOPATH")})
    goos, goarch = args.target.split("-")
    env.update(GOTOOLCHAIN="local", GOENV="off", GOWORK="off", GOPROXY="off", GOSUMDB="off",
               GOOS=goos, GOARCH=goarch, CGO_ENABLED="0", TSX_DISABLE_CACHE="1", GOFLAGS="-p=1 -mod=readonly")
    before = source_digest(ROOT)
    report = {"sourceCommit": args.commit, "sourceBefore": before, "target": args.target,
              "version": args.version, "passed": False, "releaseSignoff": False,
              "startedAt": datetime.now(timezone.utc).isoformat(), "commands": []}

    def run(label, command, timeout=180):
        print(label, flush=True)
        result = subprocess.run(command, cwd=ROOT, env=env, stdout=subprocess.PIPE,
                                stderr=subprocess.STDOUT, timeout=timeout,
                                creationflags=subprocess.CREATE_NO_WINDOW if os.name == "nt" else 0)
        report["commands"].append({"label": label, "command": command, "exitCode": result.returncode})
        text = result.stdout.decode("utf-8", errors="replace")
        print(text[-32000:], flush=True)
        if result.returncode:
            raise ValueError(label + " failed")
        return text

    try:
        expected_go = "go" + re.search(r"(?m)^go ([0-9.]+)\s*$", (ROOT / "go.mod").read_text())[1]
        if run("toolchain", [str(go), "env", "GOVERSION"]).strip() != expected_go:
            raise ValueError("The toolchain does not match go.mod")
        run("runtime-provenance", [sys.executable, "-B", "scripts/verify-jsonquery-runtime.py"])
        run("module-verification", [str(go), "mod", "verify"])
        linked = run("target-dependencies", [str(go), "list", "-mod=readonly", "-deps", "-f",
                     "{{if .Module}}{{if not .Module.Main}}{{.Module.Path}} {{.Module.Version}}{{end}}{{end}}", "./apps/node-go/cmd/canary"])
        report["linkedNoticeSets"] = verify_notice_coverage(linked, json.loads((ROOT / "third-party-manifest.json").read_text()), expected_go)
        binary = output / ("build.exe" if goos == "windows" else "build")
        flags = "-s -w -X " + VERSION_SYMBOL + "=" + args.version
        build = [str(go), "build", "-mod=readonly", "-trimpath", "-buildvcs=true", "-buildmode=exe",
                 "-ldflags=" + flags, "-o", str(binary), "./apps/node-go/cmd/canary"]
        run("build", build, 360)
        report["buildSettings"] = verify_build_info(run("build-info", [str(go), "version", "-m", str(binary)]), args.target, args.commit, expected_go)
        result = packager.package(binary, output / "archives", args.version, args.commit, args.target)
        report["artifact"] = {k: v for k, v in result.items() if k not in ("archive", "checksum")}
        binary.unlink()
        run("repeat-build", build)
        if packager.digest_file(binary)[0] != result["binarySha256"]:
            raise ValueError("Repeated build bytes differ")
        binary.unlink()
        extracted = output / ("canary.exe" if goos == "windows" else "canary")
        unpack_binary(result, extracted, args.target, args.version, args.commit)
        verify_build_info(run("extracted-build-info", [str(go), "version", "-m", str(extracted)]), args.target, args.commit, expected_go)
        report["nativeExecution"] = native
        if report["nativeExecution"]:
            protocol = int(re.search(r"(?m)^const peerProtocol = ([1-9][0-9]*)\s*$", (ROOT / "apps/node-go/internal/canary/wire_pages.go").read_text())[1])
            report["runtimeIdentity"] = verify_runtime_identity(run("extracted-identity", [str(extracted), "version"], 15),
                                                               args.target, args.version, args.commit, expected_go, protocol)
            report["releaseLabelVerifiedByExecution"] = True
            if not run("extracted-help", [str(extracted), "--help"], 15).startswith("Commands: serve,"):
                raise ValueError("Extracted executable did not return expected CLI help")
            node = shutil.which("node")
            if not node or not (ROOT / "node_modules/tsx/package.json").is_file():
                raise ValueError("Existing Node and tsx are required for the native HA proof")
            env["CANARY_BIN"] = str(extracted)
            tests = sorted((ROOT / "apps/proof/test").glob("*.test.ts")) if args.full_proofs else [ROOT / "apps/proof/test/public-availability.test.ts"]
            if not tests or any(not path.is_file() for path in tests):
                raise ValueError("Process proof selection is empty or missing")
            report["proofs"] = [path.name for path in tests]
            proof = run("extracted-ha-proof", [node, "--import", "tsx", "--test", "--test-concurrency=1",
                        "--test-reporter=tap", *map(str, tests)], 600 if args.full_proofs else 150)
            report["proofCounts"] = tap_counts(proof)
        else:
            report["executionLimit"] = "Cross-built artifact only; not executed on its target platform"
            report["releaseLabelVerifiedByExecution"] = False
        if args.alpine_help:
            docker = shutil.which("docker")
            if not docker or os.getuid() == 0:
                raise ValueError("Docker and a nonroot native Linux host user are required")
            image = "alpine:3.24"
            report["alpineImage"] = json.loads(run("alpine-image", [docker, "image", "inspect", "--format", "{{json .RepoDigests}}", image], 30))
            container = [docker, "run", "--rm", "--pull=never", "--network=none", "--read-only",
                         "--cap-drop=ALL", "--security-opt=no-new-privileges", "--pids-limit=64", "--memory=128m",
                         "--user=" + str(os.getuid()) + ":" + str(os.getgid())]
            observed_arch = run("alpine-architecture", [*container, "--entrypoint=/bin/uname", image, "-m"], 30).strip()
            if native_target("Linux", observed_arch) != args.target:
                raise ValueError("Alpine container architecture does not match the native target")
            help_text = run("alpine-extracted-help", [*container, "--mount", "type=bind,source=" + str(extracted) + ",target=/usr/local/bin/canary,readonly",
                            "--entrypoint=/usr/local/bin/canary", image, "--help"], 30)
            if not help_text.startswith("Commands: serve,"):
                raise ValueError("The extracted executable did not return Alpine CLI help")
            report["alpineHelpPassed"] = True
        if args.pm2_interop:
            run("actual-pm2-interop", [sys.executable, "-B", "scripts/pm2-real-validation.py", "--binary", str(extracted)], 360)
            report["pm2Interop"] = json.loads((ROOT / ".canary/pm2-real-current.json").read_text())
            if not report["pm2Interop"]["passed"] or report["pm2Interop"]["binarySha256"] != result["binarySha256"]:
                raise ValueError("Actual PM2 acceptance did not cover the extracted executable")
            # Reclaim only the completed, declared PM2 fixture before the next
            # fixture's download. Keep its compact retained evidence above.
            fixture = ROOT / '.canary/work/current/outputs/pmi'
            if fixture.resolve() != fixture or not fixture.is_relative_to(output.parent):
                raise ValueError('Refusing an unowned PM2 fixture cleanup target')
            shutil.rmtree(fixture)
        if args.browser_interop:
            run("actual-browser-interop", [sys.executable, "-B", "scripts/browser-linux-validation.py", "--binary", str(extracted)], 360)
            report["browserInterop"] = json.loads((ROOT / ".canary/browser-linux-current.json").read_text())
            if not report["browserInterop"]["passed"] or report["browserInterop"]["binarySha256"] != result["binarySha256"]:
                raise ValueError("Actual browser acceptance did not cover the extracted executable")
            fixture = ROOT / '.canary/work/current/outputs/bri'
            if fixture.resolve() != fixture or not fixture.is_relative_to(output.parent):
                raise ValueError('Refusing an unowned browser fixture cleanup target')
            shutil.rmtree(fixture)
        if args.coordinated_upgrade:
            run("published-baseline-upgrade", [sys.executable, "-B", "scripts/coordinated-upgrade-validation.py", "--binary", str(extracted)], 360)
            report["coordinatedUpgrade"] = json.loads((ROOT / ".canary/coordinated-upgrade-current.json").read_text())
            if not report["coordinatedUpgrade"]["passed"] or report["coordinatedUpgrade"]["binarySha256"] != result["binarySha256"]:
                raise ValueError("Published-baseline upgrade did not cover the extracted executable")
        if packager.digest_file(extracted)[0] != result["binarySha256"]:
            raise ValueError("Extracted executable changed during validation")
        clean_checkout(args.commit)
        report["passed"] = True
    except (OSError, ValueError, subprocess.SubprocessError, tarfile.TarError, zipfile.BadZipFile) as error:
        report["error"] = str(error)
    finally:
        report["sourceUnchanged"] = source_digest(ROOT) == before
        report["passed"] = report["passed"] and report["sourceUnchanged"]
        report["finishedAt"] = datetime.now(timezone.utc).isoformat()
        retain_summary("release-package", report)
        print(json.dumps(report, indent=2), flush=True)
    return 0 if report["passed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
