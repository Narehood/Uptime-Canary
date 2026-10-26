"""Offline publication admission controls; no GitHub or registry writes."""
import copy
import importlib.util
import io
import json
from pathlib import Path
import unittest
from unittest.mock import patch
import urllib.error
import urllib.parse

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("publication_gate", Path(__file__).with_name("container-publication-gate.py"))
gate = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gate)
REPO, COMMIT = "fixture/Uptime-Canary", "c" * 40


class GitHub:
    def __init__(self):
        self.head = COMMIT
        self.runs, self.jobs = {}, {}
        self.calls = []
        self.change_after_jobs = None
        for index, (file, (name, jobs)) in enumerate(gate.REQUIRED.items(), 1):
            self.runs[file] = [{"id": index, "name": name, "path": ".github/workflows/" + file,
                "head_sha": COMMIT, "head_branch": "main", "event": "push",
                "head_repository": {"full_name": REPO}, "run_attempt": 1,
                "updated_at": "2026-10-26T01:00:00Z",
                "status": "completed", "conclusion": "success"}]
            self.jobs[index] = [{"name": name, "status": "completed", "conclusion": "success"} for name in sorted(jobs)]

    def __call__(self, path):
        self.calls.append(path)
        if path.endswith("/git/ref/heads/main"):
            return {"object": {"sha": self.head}}
        if "/workflows/" in path:
            file = path.split("/workflows/")[1].split("/runs?")[0]
            query = urllib.parse.parse_qs(urllib.parse.urlsplit(path).query)
            assert query == {"branch": ["main"], "head_sha": [COMMIT], "per_page": ["100"]}
            runs = copy.deepcopy(self.runs[file])
            return {"total_count": len(runs), "workflow_runs": runs}
        run_id = int(path.split("/runs/")[1].split("/")[0])
        if "/jobs?" in path:
            attempt = int(path.split("/attempts/")[1].split("/")[0])
            assert attempt == next(r for runs in self.runs.values() for r in runs if r["id"] == run_id)["run_attempt"]
            jobs = copy.deepcopy(self.jobs[run_id])
            if self.change_after_jobs:
                self.change_after_jobs(self)
                self.change_after_jobs = None
            return {"total_count": len(jobs), "jobs": jobs}
        return copy.deepcopy(next(r for runs in self.runs.values() for r in runs if r["id"] == run_id))


class AdmissionTests(unittest.TestCase):
    def test_all_ten_required_jobs_on_one_exact_main_commit(self):
        api = GitHub()
        result = gate.assess(REPO, COMMIT, api)
        self.assertTrue(result["ready"])
        self.assertEqual(len(result["workflows"]), 3)
        self.assertEqual(sum(len(jobs) for _, jobs in gate.REQUIRED.values()), 10)
        self.assertEqual(len(api.calls), 11)  # Fixed one-shot reads, no polling.

    def test_original_image_only_success_cannot_promote(self):
        api = GitHub()
        api.runs["container-validation.yml"] = []
        api.runs["release-package-validation.yml"] = []
        self.assertFalse(gate.assess(REPO, COMMIT, api)["ready"])

    def test_each_failed_running_cancelled_or_missing_workflow_blocks(self):
        for file in gate.REQUIRED:
            for status, conclusion in [("completed", "failure"), ("completed", "cancelled"),
                    ("completed", "skipped"), ("in_progress", None), ("queued", None)]:
                with self.subTest(file=file, status=status, conclusion=conclusion):
                    api = GitHub()
                    api.runs[file][0].update(status=status, conclusion=conclusion)
                    self.assertFalse(gate.assess(REPO, COMMIT, api)["ready"])

    def test_every_required_job_must_pass_even_if_workflow_claims_success(self):
        for run_id in range(1, 4):
            for index in range(len(GitHub().jobs[run_id])):
                for conclusion in ("failure", "skipped", "cancelled", None):
                    api = GitHub()
                    api.jobs[run_id][index]["conclusion"] = conclusion
                    self.assertFalse(gate.assess(REPO, COMMIT, api)["ready"])
            for mutation in ("missing", "duplicate"):
                api = GitHub()
                if mutation == "missing":
                    api.jobs[run_id].pop()
                else:
                    api.jobs[run_id].append(dict(api.jobs[run_id][0]))
                self.assertFalse(gate.assess(REPO, COMMIT, api)["ready"])

    def test_old_green_run_cannot_hide_newer_failed_or_pending_run(self):
        for status, conclusion in [("completed", "failure"), ("in_progress", None)]:
            api = GitHub()
            newer = dict(api.runs["container-validation.yml"][0], id=99, status=status, conclusion=conclusion)
            api.runs["container-validation.yml"].append(newer)
            self.assertFalse(gate.assess(REPO, COMMIT, api)["ready"])

    def test_rerun_of_older_id_is_not_hidden_by_larger_previously_green_id(self):
        for status, conclusion in (("in_progress", None), ("completed", "failure")):
            api = GitHub()
            file = "container-validation.yml"
            green = dict(api.runs[file][0], id=99)
            api.runs[file][0].update(run_attempt=2, status=status, conclusion=conclusion,
                updated_at="2026-10-26T02:00:00Z")
            api.runs[file].append(green)
            result = gate.assess(REPO, COMMIT, api)
            self.assertFalse(result["ready"])
            chosen = next(item for item in result["workflows"] if item["name"] == "Container validation")
            self.assertEqual((chosen["run"], chosen["attempt"]), (2, 2))

    def test_other_commit_branch_event_repo_or_workflow_never_qualifies(self):
        for change in ({"head_sha": "d" * 40}, {"head_branch": "feature"},
                {"event": "pull_request"}, {"head_repository": {"full_name": "foreign/repo"}},
                {"path": ".github/workflows/not-validation.yml"}, {"name": "Impersonated workflow"}):
            api = GitHub()
            api.runs["container-validation.yml"][0].update(change)
            self.assertFalse(gate.assess(REPO, COMMIT, api)["ready"])

    def test_main_advance_and_rerun_during_inspection_block(self):
        api = GitHub()
        api.head = "d" * 40
        self.assertFalse(gate.assess(REPO, COMMIT, api)["ready"])
        self.assertEqual(len(api.calls), 1)
        for mutate in (lambda api: setattr(api, "head", "d" * 40),
                lambda api: api.runs["container-images.yml"][0].update(run_attempt=2, status="queued", conclusion=None)):
            api = GitHub()
            api.change_after_jobs = mutate
            self.assertFalse(gate.assess(REPO, COMMIT, api)["ready"])

    def test_incomplete_or_malformed_api_data_and_api_outage_fail_closed(self):
        for value in ({}, {"total_count": 101, "jobs": []},
                {"total_count": 1, "jobs": [None]}, {"total_count": True, "jobs": [{}]}):
            with self.assertRaises(gate.GateError):
                gate.bounded_list(value, "jobs")
        api = GitHub()
        original = api.__call__
        def unavailable(path):
            if "/workflows/" in path:
                raise gate.GateError("GitHub metadata unavailable")
            return original(path)
        with self.assertRaises(gate.GateError):
            gate.assess(REPO, COMMIT, unavailable)

    def test_trigger_must_be_own_main_work_not_pr_or_fork(self):
        api = GitHub()
        event = {"workflow_run": dict(api.runs["container-images.yml"][0])}
        self.assertEqual(gate.candidate("workflow_run", event, REPO, "d" * 40, "refs/heads/main"), COMMIT)
        self.assertEqual(gate.candidate("workflow_dispatch", {}, REPO, COMMIT, "refs/heads/main"), COMMIT)
        for change in ({"event": "pull_request"}, {"head_branch": "feature"},
                {"head_repository": {"full_name": "foreign/repo"}}, {"name": "Unknown"}):
            bad = {"workflow_run": {**event["workflow_run"], **change}}
            with self.assertRaises(gate.GateError):
                gate.candidate("workflow_run", bad, REPO, COMMIT, "refs/heads/main")
        for name, ref in (("pull_request", "refs/heads/main"), ("workflow_dispatch", "refs/heads/feature")):
            with self.assertRaises(gate.GateError):
                gate.candidate(name, {}, REPO, COMMIT, ref)

    def test_invalid_identity_precedes_any_api_read(self):
        for repo, commit in ((REPO, "latest"), ("bad/repo/extra", COMMIT), (REPO, "C" * 40), ("../..", COMMIT)):
            api = GitHub()
            with self.assertRaises(gate.GateError):
                gate.assess(repo, commit, api)
            self.assertEqual(api.calls, [])

    def test_null_repository_or_path_cannot_qualify_as_main(self):
        for change in ({"head_repository": None}, {"path": None}):
            api = GitHub()
            api.runs["container-validation.yml"][0].update(change)
            self.assertFalse(gate.assess(REPO, COMMIT, api)["ready"])
        with self.assertRaises(gate.GateError):
            gate.candidate("workflow_run", {"workflow_run": {"head_repository": None}}, REPO, COMMIT, "refs/heads/main")
        with self.assertRaises(gate.GateError):
            gate.assess(REPO, COMMIT, lambda path: {"object": None})

    def test_api_reader_has_fixed_origin_bounds_and_no_redirects_or_secret_errors(self):
        from types import SimpleNamespace
        calls = []
        token = "made-up-test-credential"
        def open_good(request, timeout):
            calls.append((request, timeout))
            return io.BytesIO(b'{"ok":true}')
        with patch.object(gate.urllib.request, "build_opener", return_value=SimpleNamespace(open=open_good)):
            self.assertEqual(gate.github_reader(token)("/repos/fixture/repo/actions/runs"), {"ok": True})
        request, timeout = calls[0]
        self.assertEqual(request.full_url, "https://api.github.com/repos/fixture/repo/actions/runs")
        self.assertEqual(request.get_header("Authorization"), "Bearer " + token)
        self.assertEqual(timeout, 20)
        for payload in (b"[]", b"not json", b"x" * (gate.MAX_JSON + 1)):
            with patch.object(gate.urllib.request, "build_opener", return_value=SimpleNamespace(open=lambda *args, **kwargs: io.BytesIO(payload))):
                with self.assertRaises(gate.GateError):
                    gate.github_reader(token)("/repos/fixture/repo/actions/runs")
        for exception in (urllib.error.HTTPError("https://api.github.com", 302, token, {}, None), OSError(token)):
            def open_bad(*args, **kwargs):
                raise exception
            with patch.object(gate.urllib.request, "build_opener", return_value=SimpleNamespace(open=open_bad)):
                with self.assertRaises(gate.GateError) as error:
                    gate.github_reader(token)("/repos/fixture/repo/actions/runs")
                self.assertNotIn(token, str(error.exception))
        self.assertIsNone(gate.NoRedirect().redirect_request(None, None, 302, "", {}, "https://foreign.invalid"))

    def test_workflow_wiring_has_no_build_only_promotion_path(self):
        images = (ROOT / ".github/workflows/container-images.yml").read_text()
        promotion = (ROOT / ".github/workflows/container-publication.yml").read_text()
        self.assertNotIn("\n  publish:", images)
        self.assertNotIn("publish-container-images.py --prefix", images)
        self.assertIn("container_publication_gate_test.py", images)
        for name, _ in gate.REQUIRED.values():
            self.assertIn(name, promotion)
        self.assertIn("types: [completed]", promotion)
        self.assertIn("needs.validation.outputs.ready == 'true'", promotion)
        self.assertIn("steps.recheck.outputs.ready == 'true'", promotion)
        self.assertIn('container-publication-gate.py --commit "$SOURCE_COMMIT"', promotion)
        self.assertIn("github.event.workflow_run.head_repository.full_name == github.repository", promotion)
        self.assertIn("actions: read", promotion)
        self.assertIn("persist-credentials: false", promotion)
        self.assertIn("--with-game-query", promotion)
        self.assertNotIn("pull_request_target:", promotion)
        self.assertNotIn("workflow_run.artifacts", promotion)


if __name__ == "__main__":
    unittest.main()
