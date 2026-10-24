"""Failure controls for registry writes; no Docker daemon or registry used."""
import importlib.util
import json
from pathlib import Path
import subprocess
import unittest

spec = importlib.util.spec_from_file_location("publisher", Path(__file__).with_name("publish-container-images.py"))
publisher = importlib.util.module_from_spec(spec)
spec.loader.exec_module(publisher)

PREFIX = "ghcr.io/fixture/uptime-canary"
COMMIT = "c" * 40
OLD = {"node": "sha256:" + "1" * 64, "web": "sha256:" + "2" * 64}
NEW = {"node": "sha256:" + "a" * 64, "web": "sha256:" + "b" * 64}


class Registry:
    def __init__(self, initial=True):
        self.tags = {f"{PREFIX}-{key}:sha-{COMMIT}": value for key, value in NEW.items()}
        if initial:
            self.tags.update({f"{PREFIX}-{key}:latest": value for key, value in OLD.items()})
        self.calls = []
        self.fail = None
        self.ambiguous = False
        self.rollback_failure = False
        self.inspection_failure = None

    def __call__(self, arguments, **options):
        self.calls.append(arguments)
        reference = arguments[-1]
        if arguments[3] == "inspect":
            if self.inspection_failure and reference.endswith(":latest"):
                return subprocess.CompletedProcess(arguments, 1, "", self.inspection_failure)
            if reference not in self.tags:
                return subprocess.CompletedProcess(arguments, 1, "", reference + ": not found")
            return subprocess.CompletedProcess(arguments, 0, json.dumps({"digest": self.tags[reference]}), "")
        assert arguments[3] == "create" and "--prefer-index=false" in arguments
        target = arguments[arguments.index("--tag") + 1]
        digest = reference.split("@", 1)[1]
        key = "node" if "-node:" in target else "web"
        if self.fail == key and digest == NEW[key]:
            if self.ambiguous:
                self.tags[target] = digest
                raise subprocess.TimeoutExpired(arguments, 45)
            return subprocess.CompletedProcess(arguments, 1, "", "Injected promotion failure")
        if self.rollback_failure and digest == OLD[key]:
            return subprocess.CompletedProcess(arguments, 1, "", "Injected rollback outage")
        self.tags[target] = digest
        return subprocess.CompletedProcess(arguments, 0, "", "")

    def latest(self):
        return {key: self.tags.get(f"{PREFIX}-{key}:latest") for key in OLD}

    def writes(self):
        return [call for call in self.calls if call[3] == "create"]


class PublicationTests(unittest.TestCase):
    def test_success_captures_both_previous_digests_before_writing(self):
        registry = Registry()
        result = publisher.promote(PREFIX, COMMIT, registry)
        self.assertEqual(registry.latest(), NEW)
        self.assertEqual({key: value["previousDigest"] for key, value in result.items()}, OLD)
        self.assertTrue(all(call[3] == "inspect" for call in registry.calls[:4]))
        self.assertTrue(all("@sha256:" in call[-1] for call in registry.writes()))

    def test_web_failure_restores_node_and_preserves_commit_tags(self):
        registry = Registry()
        registry.fail = "web"
        with self.assertRaisesRegex(publisher.PublicationError, "previous latest tags restored"):
            publisher.promote(PREFIX, COMMIT, registry)
        self.assertEqual(registry.latest(), OLD)
        self.assertEqual({key: registry.tags[f"{PREFIX}-{key}:sha-{COMMIT}"] for key in NEW}, NEW)

    def test_ambiguous_failures_restore_even_the_failed_write(self):
        for key in ("node", "web"):
            with self.subTest(key=key):
                registry = Registry()
                registry.fail, registry.ambiguous = key, True
                with self.assertRaises(publisher.PublicationError):
                    publisher.promote(PREFIX, COMMIT, registry)
                self.assertEqual(registry.latest(), OLD)

    def test_inspection_denial_or_outage_never_changes_latest(self):
        for error in ("unauthorized", "host not found", "registry returned 503"):
            with self.subTest(error=error):
                registry = Registry()
                registry.inspection_failure = error
                with self.assertRaises(publisher.PublicationError):
                    publisher.promote(PREFIX, COMMIT, registry)
                self.assertEqual(registry.writes(), [])
                self.assertEqual(registry.latest(), OLD)

    def test_missing_candidate_never_changes_latest(self):
        registry = Registry()
        del registry.tags[f"{PREFIX}-web:sha-{COMMIT}"]
        with self.assertRaises(publisher.PublicationError):
            publisher.promote(PREFIX, COMMIT, registry)
        self.assertEqual(registry.writes(), [])

    def test_first_publication_succeeds_without_previous_latest(self):
        registry = Registry(initial=False)
        result = publisher.promote(PREFIX, COMMIT, registry)
        self.assertEqual(registry.latest(), NEW)
        self.assertTrue(all(value["previousDigest"] is None for value in result.values()))

    def test_first_publication_failure_reports_incomplete_without_deletion(self):
        registry = Registry(initial=False)
        registry.fail = "web"
        with self.assertRaisesRegex(publisher.PublicationError, "initial publication remains incomplete"):
            publisher.promote(PREFIX, COMMIT, registry)
        self.assertNotIn(f"{PREFIX}-web:latest", registry.tags)
        self.assertEqual(len(registry.tags), 3)

    def test_rollback_outage_is_reported_as_failure(self):
        registry = Registry()
        registry.fail, registry.rollback_failure = "web", True
        with self.assertRaisesRegex(publisher.PublicationError, "rollback failed"):
            publisher.promote(PREFIX, COMMIT, registry)

    def test_invalid_publication_identity_never_invokes_docker(self):
        registry = Registry()
        for prefix, commit in (("other.example/image", COMMIT), (PREFIX, "latest")):
            with self.assertRaises(publisher.PublicationError):
                publisher.promote(prefix, commit, registry)
        self.assertEqual(registry.calls, [])


if __name__ == "__main__":
    unittest.main()
