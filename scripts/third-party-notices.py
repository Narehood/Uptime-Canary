"""Retain license/notice texts for linked Go modules and dashboard runtime packages.

Uses the installed, checksum-locked dependencies; does not update lockfiles or
install tools. Run after dependency changes, or use --check in release checks.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess

ROOT = Path(__file__).resolve().parents[1]
NOTICE = ROOT / "THIRD_PARTY_NOTICES.txt"
MANIFEST = ROOT / "third-party-manifest.json"
NAMES = re.compile(r"^(?:LICEN[CS]E|COPYING|COPYRIGHT|NOTICE|AUTHORS)(?:[._-].*)?$", re.I)


def command(args, env):
    result = subprocess.run(args, cwd=ROOT, env=env, capture_output=True, check=True)
    return result.stdout.decode("utf-8-sig")


def objects(text):
    decoder = json.JSONDecoder()
    while text.strip():
        item, length = decoder.raw_decode(text.lstrip())
        yield item
        text = text.lstrip()[length:]


def texts(directory):
    found = [p for p in directory.iterdir() if p.is_file() and not p.is_symlink() and NAMES.match(p.name)]
    licenses = directory / "LICENSES"
    if licenses.is_dir() and not licenses.is_symlink():
        found += [p for p in licenses.rglob("*") if p.is_file() and not p.is_symlink()]
    if not found or len(found) > 128:
        raise RuntimeError("Missing or excessive license files: " + directory.name)
    output = []
    for path in sorted(found):
        data = path.read_bytes()
        if len(data) > 2 * 1024 * 1024 or b"\0" in data:
            raise RuntimeError("License text exceeds bounds or is binary: " + path.name)
        output.append((path.relative_to(directory).as_posix(), data.decode("utf-8-sig").replace("\r\n", "\n")))
    return output


def javascript_notice(root):
    """An embedded asset needs its own attribution outside go list's graph."""
    directory = root / "apps/node-go/internal/jsonquery/jsvm"
    bundle = directory / "jsonata.js"
    if not bundle.is_file():
        return None
    try:
        upstream = json.loads((directory / "upstream.json").read_text(encoding="utf-8"))
        version, commit, digest = (upstream[key] for key in ("version", "commit", "bundleSha256"))
        valid = (upstream["module"] == "jsonata" and isinstance(version, str)
                 and re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+", version)
                 and isinstance(commit, str) and re.fullmatch(r"[0-9a-f]{40}", commit)
                 and isinstance(digest, str) and re.fullmatch(r"[0-9a-f]{64}", digest))
        if not valid or hashlib.sha256(bundle.read_bytes()).hexdigest() != digest:
            raise ValueError("Invalid embedded JSONata provenance")
        entries = texts(directory)
        if not any(name.upper().startswith("LICENSE") for name, _ in entries):
            raise ValueError("Embedded JSONata license is missing")
        if not any(name.upper() == "NOTICE.TXT" for name, _ in entries):
            raise ValueError("Embedded JSONata patch notice is missing")
    except (OSError, KeyError, TypeError, ValueError) as exc:
        raise RuntimeError("Embedded JSONata attribution is incomplete or inconsistent") from exc
    return directory, version, commit, digest, entries


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()
    go = ROOT / (".canary/tools/go/bin/go.exe" if os.name == "nt" else ".canary/tools/go/bin/go")
    executable = str(go) if go.is_file() else "go"
    env = dict(os.environ, GOTOOLCHAIN="local", GOFLAGS="-mod=readonly", CGO_ENABLED="0", GOOS="linux", GOARCH="amd64")
    linked = set(command([executable, "list", "-deps", "-f", "{{with .Module}}{{.Path}}{{end}}", "./apps/node-go/cmd/canary"], env).split())
    modules = list(objects(command([executable, "list", "-m", "-json", "all"], env)))
    sections, manifest = [], []

    def add(name, version, directory, selected=None):
        entries = selected if selected is not None else texts(directory)
        sections.append("=" * 78 + "\n" + name + " " + version + "\n" + "=" * 78 + "\n")
        files = []
        for filename, text in entries:
            sections.append("\n--- " + filename + " ---\n\n" + text.rstrip() + "\n")
            files.append({"file": filename, "sha256": hashlib.sha256(text.encode()).hexdigest()})
        manifest.append({"name": name, "version": version, "notices": files})

    goroot = Path(command([executable, "env", "GOROOT"], env).strip())
    add("Go standard library", command([executable, "env", "GOVERSION"], env).strip(), goroot,
        [(name, (goroot / name).read_text(encoding="utf-8").replace("\r\n", "\n")) for name in ("LICENSE", "PATENTS") if (goroot / name).is_file()])
    for module in sorted(modules, key=lambda m: m["Path"]):
        if module["Path"] in linked and not module.get("Main"):
            source = module.get("Replace", module)
            # A locally maintained source replacement keeps its pinned upstream
            # version in the parent requirement, not in Replace.Version.
            version = source.get("Version", module.get("Version", "local"))
            add(module["Path"], version, Path(source["Dir"]))
    # A locally patched evaluator is part of the main module, so go list's
    # external-module inventory cannot retain its upstream attribution for us.
    evaluator = ROOT / "apps/node-go/internal/jsonquery/evaluator"
    if evaluator.is_dir():
        upstream = json.loads((evaluator / "upstream.json").read_text(encoding="utf-8"))
        commit = upstream["commit"]
        if not isinstance(commit, str) or not re.fullmatch(r"[0-9a-f]{40}", commit):
            raise RuntimeError("Invalid vendored evaluator provenance")
        notices = texts(evaluator)
        source_notice = evaluator.parent / "NOTICE.txt"
        notices.append(("source-NOTICE.txt", source_notice.read_text(encoding="utf-8-sig").replace("\r\n", "\n")))
        add("vendored:github.com/jsonata-go/jsonata/v206", commit + " + Canary finite-profile patch", evaluator, notices)
    javascript = javascript_notice(ROOT)
    if javascript is not None:
        directory, version, commit, digest, entries = javascript
        add("vendored:npm:jsonata", version + " (" + commit + ") + Canary finite-profile patch", directory, entries)
        manifest[-1]["embeddedSource"] = {
            "file": "apps/node-go/internal/jsonquery/jsvm/jsonata.js",
            "sha256": digest,
            "upstreamCommit": commit,
        }
    pending = list(json.loads((ROOT / "apps/web/package.json").read_text())["dependencies"])
    visited = set()
    while pending:
        name = pending.pop(0)
        if name in visited or name.startswith("@uptime-canary/"):
            continue
        visited.add(name)
        directory = ROOT / "node_modules" / name
        package = json.loads((directory / "package.json").read_text(encoding="utf-8"))
        add("npm:" + name, package["version"], directory)
        pending.extend(sorted(package.get("dependencies", {})))
    header = ("Uptime-Canary third-party notices\n\n"
              "The project license is in LICENSE. The following texts accompany linked\n"
              "Linux Go dependencies, embedded assets and dashboard runtime packages\n"
              "at the recorded versions.\n"
              "Their original terms and attribution remain applicable. This generated inventory\n"
              "retains distributed notice files; it is not a substitute for reviewing changes\n"
              "to dependencies, container base packages or separately distributed tools.\n\n")
    notice = header + "\n".join(sections)
    metadata = json.dumps({"target": "linux/amd64, CGO=0; dashboard runtime", "packages": manifest}, indent=2) + "\n"
    if args.check:
        if not NOTICE.exists() or not MANIFEST.exists() or NOTICE.read_text(encoding="utf-8") != notice or MANIFEST.read_text(encoding="utf-8") != metadata:
            raise SystemExit("Third-party notices changed; regenerate with scripts/third-party-notices.py")
    else:
        NOTICE.write_text(notice, encoding="utf-8", newline="\n")
        MANIFEST.write_text(metadata, encoding="utf-8", newline="\n")
    print(f"Verified {len(manifest)} package notice sets ({len(notice.encode())} bytes)")


if __name__ == "__main__":
    main()
