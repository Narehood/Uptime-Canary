"""Check npm advisories and Go reachable vulnerabilities, retaining exact evidence.

Requires npm and govulncheck v1.9.0. Uses portable workspace tools when present.
No dependencies, environment settings, credentials or application data change.
"""
import argparse
from datetime import datetime, timezone
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import urllib.request


ROOT = Path(__file__).resolve().parents[1]
_paths_spec = importlib.util.spec_from_file_location("canary_validation_paths", Path(__file__).with_name("validation_paths.py"))
_paths_module = importlib.util.module_from_spec(_paths_spec)
_paths_spec.loader.exec_module(_paths_module)
filesystem_path = _paths_module.filesystem_path
_runtime_spec = importlib.util.spec_from_file_location("jsonquery_runtime_provenance", Path(__file__).with_name("verify-jsonquery-runtime.py"))
_runtime_module = importlib.util.module_from_spec(_runtime_spec)
_runtime_spec.loader.exec_module(_runtime_module)
from validation_workspace import guarded_output, retain_summary


def fingerprint(source):
    source = filesystem_path(source)
    files = [source / name for name in ("go.mod", "go.sum", "package.json", "package-lock.json")]
    files += sorted((source / "apps/node-go").rglob("*.go"))
    files += sorted((source / "apps/node-go").rglob("*.js"))
    files += sorted((source / "apps/node-go").rglob("upstream.json"))
    files += sorted((source / "apps/node-go").rglob("CANARY_PATCH.json"))
    files += sorted((source / "apps/node-go").rglob("*.mod"))
    files += sorted((source / "apps/node-go").rglob("*.sum"))
    files += sorted((source / "apps/web").glob("package*.json"))
    return {str(p.relative_to(source)).replace("\\", "/"): hashlib.sha256(p.read_bytes()).hexdigest()
            for p in files if p.is_file()}


def json_stream(data):
    text = data.decode("utf-8-sig")
    decoder = json.JSONDecoder()
    values, position = [], 0
    while position < len(text):
        while position < len(text) and text[position].isspace():
            position += 1
        if position == len(text):
            break
        value, consumed = decoder.raw_decode(text[position:])
        values.append(value)
        position += consumed
    return values


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def osv_query(query, output, label, opener):
    """Retain complete bounded OSV pages; never call a partial result clean."""
    findings, tokens = [], set()
    payload = dict(query)
    for page in range(8):
        request = urllib.request.Request("https://api.osv.dev/v1/query",
            data=json.dumps(payload).encode(), method="POST",
            headers={"Content-Type": "application/json", "Accept": "application/json", "Accept-Encoding": "identity"})
        with opener.open(request, timeout=30) as response:
            if response.status != 200 or response.headers.get_content_type() != "application/json":
                raise ValueError("Invalid OSV response")
            raw = response.read((1 << 20) + 1)
        (output / (label + "-page-" + str(page) + ".json")).write_bytes(raw)
        if len(raw) > 1 << 20:
            raise ValueError("OSV response exceeds evidence budget")
        data = json.loads(raw)
        if not isinstance(data, dict) or not isinstance(data.get("vulns", []), list):
            raise ValueError("Invalid OSV response")
        if data and not {"vulns", "next_page_token"}.intersection(data):
            raise ValueError("Unrecognized OSV response")
        for finding in data.get("vulns", []):
            if not isinstance(finding, dict) or not isinstance(finding.get("id"), str) or not 1 <= len(finding["id"]) <= 256:
                raise ValueError("Invalid OSV finding")
            if finding.get("withdrawn") is not None:
                withdrawn = finding["withdrawn"]
                if not isinstance(withdrawn, str) or datetime.fromisoformat(withdrawn.replace("Z", "+00:00")).tzinfo is None:
                    raise ValueError("Invalid OSV withdrawal")
            findings.append({key: finding[key] for key in ("id", "aliases", "modified", "withdrawn") if key in finding})
        token = data.get("next_page_token")
        if token is None or token == "":
            return findings
        if not isinstance(token, str) or len(token) > 8192 or token in tokens:
            raise ValueError("Invalid OSV pagination")
        tokens.add(token)
        payload["page_token"] = token
    raise ValueError("OSV pagination exceeds evidence budget")


def vendored_checks(source, output, opener):
    records = {}
    specifications = [
        ("evaluator", "Go", "vendoredEvaluator", "vendored"),
        ("jsvm", "npm", "embeddedJSONata", "embedded-jsonata"),
    ]
    for folder, ecosystem, key, label in specifications:
        directory = source / "apps/node-go/internal/jsonquery" / folder
        vendor = directory / "upstream.json"
        bundle = directory / "jsonata.js" if folder == "jsvm" else None
        if not vendor.is_file():
            if bundle is not None and bundle.is_file():
                raise ValueError("Embedded evaluator provenance is required")
            continue
        upstream = json.loads(vendor.read_text(encoding="utf-8"))
        package, version, commit = upstream["module"], upstream["version"], upstream["commit"]
        if any(not isinstance(x, str) or not 1 <= len(x) <= 256 for x in (package, version, commit)):
            raise ValueError("Invalid vendored provenance")
        if bundle is not None:
            digest = upstream["bundleSha256"]
            if package != "jsonata" or not isinstance(digest, str) or len(digest) != 64 or not bundle.is_file() or hashlib.sha256(bundle.read_bytes()).hexdigest() != digest:
                raise ValueError("Embedded evaluator does not match its provenance")
        module_findings = osv_query({"package": {"name": package, "ecosystem": ecosystem}, "version": version}, output, label + "-module", opener)
        commit_findings = osv_query({"commit": commit}, output, label + "-commit", opener)
        active = sorted({v["id"] for v in module_findings + commit_findings if not v.get("withdrawn")})
        records[key] = {"module": package, "ecosystem": ecosystem, "version": version, "commit": commit,
            "endpoint": "https://api.osv.dev/v1/query", "moduleFindings": module_findings,
            "commitFindings": commit_findings, "activeAdvisories": active,
            "scope": "Upstream advisory matching, not reachability or a claim that local patches fix advisories"}
        if bundle is not None:
            records[key]["bundleSha256"] = digest
    goja = source / "apps/node-go/third_party/goja"
    if goja.is_dir():
        # Local replacements need their upstream identity checked explicitly;
        # an empty replacement version is not an advisory exemption.
        metadata = _runtime_module.verify_goja(source)
        findings = osv_query({"package": {"name": metadata["module"], "ecosystem": "Go"},
                              "version": metadata["version"]}, output, "local-goja-module", opener)
        commits = osv_query({"commit": metadata["commit"]}, output, "local-goja-commit", opener)
        records["localGoja"] = {"module": metadata["module"], "version": metadata["version"],
            "commit": metadata["commit"], "moduleFindings": findings, "commitFindings": commits,
            "activeAdvisories": sorted({v["id"] for v in findings + commits if not v.get("withdrawn")}),
            "scope": "Verified original source plus one recorded helper; upstream advisory matching, not patch clearance"}
    return records


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--govulncheck", help="Path to an existing govulncheck executable")
    parser.add_argument("--context", type=Path, default=ROOT, help="Source to check; evidence stays in the workspace")
    parser.add_argument("--goos", default="linux", choices=("linux", "windows", "darwin"))
    parser.add_argument("--goarch", default="amd64", choices=("amd64", "arm64"))
    args = parser.parse_args()
    source = args.context.resolve()
    if not (source / "go.mod").is_file() or not (source / "package-lock.json").is_file():
        raise SystemExit("A Canary source context is required")
    suffix = ".exe" if os.name == "nt" else ""
    portable_go = ROOT / (".canary/tools/go/bin/go" + suffix)
    go = str(portable_go) if portable_go.is_file() else shutil.which("go")
    portable_scan = ROOT / (".canary/tools/security/govulncheck" + suffix)
    scan = args.govulncheck or (str(portable_scan) if portable_scan.is_file() else shutil.which("govulncheck"))
    npm = shutil.which("npm.cmd" if os.name == "nt" else "npm")
    if not go or not scan or not npm:
        raise SystemExit("Go, npm and govulncheck are required; install govulncheck v1.9.0 into an isolated tool directory first")
    stamp = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%S%fZ")
    output = guarded_output("security")
    _runtime_module.verify(source)
    before = fingerprint(source)
    env = dict(os.environ)
    env.update({"CGO_ENABLED": "0", "GOOS": args.goos, "GOARCH": args.goarch,
                "GOTOOLCHAIN": "local", "npm_config_cache": str(output / "npm-cache"),
                "npm_config_update_notifier": "false"})
    env["PATH"] = str(Path(go).parent) + os.pathsep + env.get("PATH", "")
    commands = {"npm": [npm, "audit", "--json"], "go": [scan, "-json", "./apps/node-go/..."]}
    report = {"startedAt": stamp, "target": {"goos": args.goos, "goarch": args.goarch},
              "source": str(source), "sourceBefore": hashlib.sha256(json.dumps(before, sort_keys=True).encode()).hexdigest(), "checks": {}}
    failed = False
    for name, command in commands.items():
        # npm.cmd requires the Windows command processor; arguments are fixed.
        invocation = subprocess.list2cmdline(command) if os.name == "nt" and name == "npm" else command
        try:
            result = subprocess.run(invocation, cwd=source, env=env, stdout=subprocess.PIPE,
                                    stderr=subprocess.PIPE, timeout=300,
                                    shell=os.name == "nt" and name == "npm")
            (output / (name + ".json")).write_bytes(result.stdout)
            (output / (name + ".stderr.txt")).write_bytes(result.stderr)
            item = {"command": command, "exitCode": result.returncode}
            if name == "npm":
                data = json.loads(result.stdout.decode("utf-8-sig"))
                item["vulnerabilities"] = data.get("metadata", {}).get("vulnerabilities", {})
                if "error" in data:
                    item["error"] = data["error"]
                    failed = True
            else:
                events = json_stream(result.stdout)
                item["config"] = [event["config"] for event in events if "config" in event]
                item["findings"] = [event["finding"] for event in events if "finding" in event]
                # Source-symbol mode returns nonzero for reachable findings. A
                # module-only advisory is retained in full, not called a clean DB.
                if not item["config"]:
                    failed = True
            failed = failed or result.returncode != 0
            report["checks"][name] = item
        except (OSError, subprocess.TimeoutExpired, ValueError) as error:
            report["checks"][name] = {"error": type(error).__name__}
            failed = True
    # Embedded code is outside module/lockfile scanners. Verify its exact asset
    # before checking upstream package and commit identities independently.
    try:
        records = vendored_checks(source, output, urllib.request.build_opener(NoRedirect()))
        report["checks"].update(records)
        failed = failed or any(record["activeAdvisories"] for record in records.values())
    except (OSError, ValueError, KeyError, TypeError, RecursionError) as error:
        report["checks"]["vendoredProvenance"] = {"error": type(error).__name__}
        failed = True
    after = fingerprint(source)
    report["sourceAfter"] = hashlib.sha256(json.dumps(after, sort_keys=True).encode()).hexdigest()
    report["sourceUnchanged"] = before == after
    report["finishedAt"] = datetime.now(timezone.utc).isoformat()
    report["passed"] = not failed and report["sourceUnchanged"]
    (output / "summary.json").write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8")
    retain_summary("security", report)
    print(json.dumps({"evidence": str(output), "passed": report["passed"],
                      "sourceUnchanged": report["sourceUnchanged"], "checks": report["checks"]}, indent=2))
    return 0 if report["passed"] else 1


if __name__ == "__main__":
    sys.exit(main())
