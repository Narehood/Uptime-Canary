"""One-shot, fail-closed checks before promoting a combined main build.

Only fixed GitHub API origins and metadata are read; no artifacts or PR code
are downloaded. The latest completed attempt of every required workflow and
every required job must pass on the exact current main commit.
"""
import argparse
from datetime import datetime
import json
import os
from pathlib import Path
import re
import sys
import urllib.error
import urllib.parse
import urllib.request

REQUIRED = {
    "container-images.yml": ("Container images", {
        "Build image (node)", "Build image (web)", "Build image (game-query)"}),
    "container-validation.yml": ("Container validation", {
        "alpine-runtime (node)", "alpine-runtime (web)",
        "alpine-runtime (race)", "alpine-runtime (proof)"}),
    "release-package-validation.yml": ("Release package validation", {
        "native-package (linux-amd64)", "native-package (linux-arm64)",
        "native-package (windows-amd64)"}),
}
MAX_JSON = 2 * 1024 * 1024


class GateError(RuntimeError):
    pass


def identity(repository, commit):
    if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9][A-Za-z0-9_.-]*", repository or ""):
        raise GateError("Invalid repository identity")
    if not re.fullmatch(r"[0-9a-f]{40}", commit or ""):
        raise GateError("An exact lowercase commit is required")


def candidate(event_name, event, repository, sha, ref):
    if event_name == "workflow_dispatch" and ref == "refs/heads/main":
        identity(repository, sha)
        return sha
    if event_name != "workflow_run":
        raise GateError("Only main workflow completion or an explicit main dispatch may promote images")
    run = event.get("workflow_run", {})
    expected_names = {name for name, _ in REQUIRED.values()}
    if (not isinstance(run, dict) or run.get("name") not in expected_names
            or run.get("event") not in {"push", "workflow_dispatch"}
            or run.get("head_branch") != "main"
            or not isinstance(run.get("head_repository"), dict)
            or run["head_repository"].get("full_name") != repository):
        raise GateError("The completed workflow is not a trusted main build")
    commit = run.get("head_sha")
    identity(repository, commit)
    return commit


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def github_reader(token):
    if not token:
        raise GateError("GitHub metadata authentication is missing")
    opener = urllib.request.build_opener(NoRedirect())

    def read(path):
        if not path.startswith("/repos/") or "://" in path:
            raise GateError("Invalid GitHub metadata path")
        request = urllib.request.Request("https://api.github.com" + path, headers={
            "Authorization": "Bearer " + token,
            "Accept": "application/vnd.github+json",
            "X-GitHub-Api-Version": "2022-11-28",
            "User-Agent": "uptime-canary-publication-gate",
        })
        try:
            with opener.open(request, timeout=20) as response:
                raw = response.read(MAX_JSON + 1)
            if len(raw) > MAX_JSON:
                raise GateError("GitHub metadata exceeds its size bound")
            value = json.loads(raw)
            if not isinstance(value, dict):
                raise GateError("Invalid GitHub metadata object")
            return value
        except urllib.error.HTTPError as error:
            raise GateError("GitHub metadata request failed with HTTP " + str(error.code)) from None
        except (OSError, ValueError):
            raise GateError("GitHub metadata could not be read") from None

    return read


def bounded_list(data, key):
    values = data.get(key)
    total = data.get("total_count")
    if (not isinstance(values, list) or type(total) is not int
            or total != len(values) or total > 100
            or any(not isinstance(value, dict) for value in values)):
        raise GateError("Incomplete or invalid " + key + " metadata; promotion refused")
    return values


def assess(repository, commit, read):
    identity(repository, commit)
    prefix = "/repos/" + repository
    result = {"ready": False, "commit": commit, "reason": "", "workflows": []}

    def current():
        value = read(prefix + "/git/ref/heads/main").get("object")
        if not isinstance(value, dict):
            raise GateError("Invalid main branch metadata")
        return value.get("sha") == commit

    if not current():
        result["reason"] = "main advanced; retain commit images without moving latest"
        return result
    approved = True
    for file, (name, required_jobs) in REQUIRED.items():
        query = urllib.parse.urlencode({"branch": "main", "head_sha": commit, "per_page": 100})
        runs = bounded_list(read(prefix + "/actions/workflows/" + file + "/runs?" + query), "workflow_runs")
        matching = []
        for run in runs:
            if (run.get("head_sha") == commit and run.get("head_branch") == "main"
                    and isinstance(run.get("head_repository"), dict)
                    and run["head_repository"].get("full_name") == repository
                    and run.get("event") in {"push", "workflow_dispatch"}
                    and isinstance(run.get("path"), str)
                    and run["path"].split("@", 1)[0] == ".github/workflows/" + file
                    and run.get("name") == name):
                if type(run.get("id")) is not int or run["id"] < 1:
                    raise GateError("Invalid workflow run identity")
                try:
                    updated = datetime.fromisoformat(run["updated_at"])
                    if updated.tzinfo is None:
                        raise ValueError("Missing time zone")
                except (KeyError, TypeError, ValueError):
                    raise GateError("Invalid workflow execution time") from None
                run = {**run, "execution_updated": updated}
                matching.append(run)
        item = {"name": name, "approved": False, "reason": ""}
        result["workflows"].append(item)
        if not matching:
            item["reason"] = "No matching main workflow run"
            approved = False
            continue
        # Rerunning an older run keeps its ID. Its newer execution must not be
        # hidden by a previously green run with a larger creation-time ID.
        run = max(matching, key=lambda value: (value["execution_updated"], value["id"]))
        attempt = run.get("run_attempt")
        if type(attempt) is not int or attempt < 1:
            raise GateError("Invalid workflow attempt")
        item.update({"run": run["id"], "attempt": attempt})
        if run.get("status") != "completed" or run.get("conclusion") != "success":
            item["reason"] = "Latest workflow run is not a completed success"
            approved = False
            continue
        jobs = bounded_list(read(prefix + "/actions/runs/" + str(run["id"])
            + "/attempts/" + str(attempt) + "/jobs?per_page=100"), "jobs")
        names = [job.get("name") for job in jobs]
        all_required = all(names.count(name) == 1 and any(
            job.get("name") == name and job.get("status") == "completed"
            and job.get("conclusion") == "success" for job in jobs) for name in required_jobs)
        no_failed_jobs = all(job.get("status") == "completed"
            and job.get("conclusion") in {"success", "skipped"} for job in jobs)
        # Detect a rerun started while its previous attempt's jobs were read.
        latest = read(prefix + "/actions/runs/" + str(run["id"]))
        unchanged = (latest.get("head_sha") == commit and latest.get("run_attempt") == attempt
            and latest.get("status") == "completed" and latest.get("conclusion") == "success")
        item["approved"] = all_required and no_failed_jobs and unchanged
        item["reason"] = "All required jobs passed" if item["approved"] else "Required jobs missing, skipped, failed or superseded"
        approved = approved and item["approved"]
    result["ready"] = approved and current()
    result["reason"] = "All matching validation gates passed" if result["ready"] else "Validation incomplete, failed, or main advanced; latest unchanged"
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--commit", help="Recheck an already selected exact commit immediately before promotion")
    args = parser.parse_args()
    try:
        repository = os.environ.get("GITHUB_REPOSITORY", "")
        commit = args.commit
        if commit is None:
            path = Path(os.environ["GITHUB_EVENT_PATH"])
            with path.open("rb") as source:
                raw = source.read(MAX_JSON + 1)
            if len(raw) > MAX_JSON:
                raise GateError("Workflow event exceeds its size bound")
            event = json.loads(raw)
            if not isinstance(event, dict):
                raise GateError("Invalid workflow event")
            commit = candidate(os.environ.get("GITHUB_EVENT_NAME"), event, repository,
                os.environ.get("GITHUB_SHA"), os.environ.get("GITHUB_REF"))
        result = assess(repository, commit, github_reader(os.environ.get("GH_TOKEN")))
        print(json.dumps(result))
        if output := os.environ.get("GITHUB_OUTPUT"):
            with Path(output).open("a", encoding="utf-8") as destination:
                destination.write("ready=" + str(result["ready"]).lower() + "\ncommit=" + commit + "\n")
        if summary := os.environ.get("GITHUB_STEP_SUMMARY"):
            with Path(summary).open("a", encoding="utf-8") as destination:
                destination.write(result["reason"] + " for `" + commit + "`.\n")
                for item in result["workflows"]:
                    destination.write("- " + item["name"] + ": " + item["reason"] + "\n")
        return 0
    except (GateError, KeyError, OSError, ValueError) as error:
        # Never echo raw URL/HTTP exceptions, response bodies, or credentials.
        message = str(error) if isinstance(error, GateError) else "Publication metadata could not be verified"
        print("Image promotion refused: " + message, file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
