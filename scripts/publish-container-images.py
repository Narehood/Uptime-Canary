"""Promote a GHCR image pair, restoring previous tags after a failed write."""
import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import sys


class PublicationError(RuntimeError):
    pass


def promote(prefix, commit, run=subprocess.run):
    if not re.fullmatch(r"ghcr\.io/[a-z0-9-]+/uptime-canary", prefix) or not re.fullmatch(r"[0-9a-f]{40}", commit):
        raise PublicationError("Invalid container publication identity")

    def command(*arguments):
        try:
            result = run(["docker", "buildx", "imagetools", *arguments],
                         capture_output=True, text=True, timeout=45, check=False)
        except (OSError, subprocess.TimeoutExpired) as error:
            raise PublicationError(str(error)) from error
        return result

    def inspect(reference, missing_ok=False):
        result = command("inspect", "--format", "{{json .Manifest}}", reference)
        if result.returncode:
            error = result.stderr or result.stdout or "Registry inspection failed"
            missing = ("manifest unknown" in error.lower() or "no such manifest" in error.lower()
                       or re.search(re.escape(reference) + r": not found(?:\s|$)", error))
            if missing_ok and missing:
                return None
            raise PublicationError(error.strip())
        try:
            digest = json.loads(result.stdout)["digest"]
            if not isinstance(digest, str) or not re.fullmatch(r"sha256:[0-9a-f]{64}", digest):
                raise ValueError("Invalid digest")
            return digest
        except (KeyError, TypeError, ValueError) as error:
            raise PublicationError("Registry returned an invalid manifest descriptor") from error

    def write(component, digest):
        image = prefix + "-" + component
        result = command("create", "--prefer-index=false", "--tag", image + ":latest", image + "@" + digest)
        if result.returncode:
            raise PublicationError((result.stderr or result.stdout or "Registry promotion failed").strip())

    components = ("node", "web")
    # Capture both candidates and old tags before any write. Promote captured
    # digests, so a mutable commit tag cannot change halfway through the pair.
    candidates = {key: inspect(prefix + "-" + key + ":sha-" + commit) for key in components}
    previous = {key: inspect(prefix + "-" + key + ":latest", missing_ok=True) for key in components}
    attempted = []
    try:
        for key in components:
            # A timeout can follow a committed registry write: include the
            # failing attempt in rollback, not just calls that returned zero.
            attempted.append(key)
            write(key, candidates[key])
    except PublicationError as error:
        recovery = []
        for key in reversed(attempted):
            if previous[key] is None:
                recovery.append(key + ": no previous latest tag; initial publication remains incomplete")
                continue
            try:
                write(key, previous[key])
            except PublicationError as rollback:
                recovery.append(key + ": rollback failed: " + str(rollback))
        detail = "; ".join(recovery) if recovery else "previous latest tags restored"
        raise PublicationError(str(error) + "; " + detail) from error
    return {key: {"digest": candidates[key], "previousDigest": previous[key]} for key in components}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--prefix", required=True)
    parser.add_argument("--commit", required=True)
    args = parser.parse_args()
    try:
        result = promote(args.prefix, args.commit)
    except PublicationError as error:
        print("Container promotion failed: " + str(error), file=sys.stderr)
        return 1
    print(json.dumps(result))
    if summary := os.environ.get("GITHUB_STEP_SUMMARY"):
        with Path(summary).open("a", encoding="utf-8") as output:
            for component, value in result.items():
                output.write(f"Published `{args.prefix}-{component}:latest` at `{value['digest']}` from `{args.commit}`.\n")
    if path := os.environ.get("GITHUB_OUTPUT"):
        with Path(path).open("a", encoding="utf-8") as output:
            output.write("published=true\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
