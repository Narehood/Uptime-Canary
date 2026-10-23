"""Offline release-gate controls: pagination and invalid data cannot look clean."""
import importlib.util
import json
import hashlib
from email.message import Message
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("security_check", Path(__file__).with_name("security-check.py"))
security = importlib.util.module_from_spec(spec)
spec.loader.exec_module(security)


class Response:
    def __init__(self, data, status=200, content_type="application/json"):
        self.raw = data if isinstance(data, bytes) else json.dumps(data).encode()
        self.status = status
        self.headers = Message()
        self.headers["Content-Type"] = content_type

    def read(self, size):
        return self.raw[:size]

    def __enter__(self):
        return self

    def __exit__(self, *_):
        return False


class Opener:
    def __init__(self, responses):
        self.responses = iter(responses)
        self.requests = []

    def open(self, request, timeout):
        assert request.full_url == "https://api.osv.dev/v1/query"
        assert timeout == 30
        self.requests.append(json.loads(request.data))
        return next(self.responses)


class AdvisoryEvidenceTests(unittest.TestCase):
    def goja_fixture(self, root):
        directory = root / "apps/node-go/third_party/goja"
        directory.mkdir(parents=True)
        original = {}
        for index in range(81):
            name = f"source-{index}.go"
            raw = f"// synthetic original {index}\n".encode()
            (directory / name).write_bytes(raw)
            original[name] = hashlib.sha256(raw).hexdigest()
        helper = b"// synthetic recorded helper\n"
        (directory / "canary_own_data.go").write_bytes(helper)
        (directory / "NOTICE.txt").write_text("Synthetic test fixture")
        metadata = {"module": "github.com/dop251/goja", "version": "v0.0.0-20261022125820-07c4b9d6265b",
            "commit": "07c4b9d6265b9d472b736d9a57295b03809ce02b", "upstreamFiles": original,
            "addedFile": "canary_own_data.go", "addedFileSHA256": hashlib.sha256(helper).hexdigest()}
        (directory / "CANARY_PATCH.json").write_text(json.dumps(metadata))
        return directory, metadata

    def test_local_goja_uses_verified_upstream_version_and_commit(self):
        with tempfile.TemporaryDirectory() as path:
            root = Path(path)
            _, metadata = self.goja_fixture(root)
            opener = Opener([Response({}), Response({"vulns": [{"id": "TEST-GOJA"}]})])
            record = security.vendored_checks(root, root, opener)["localGoja"]
            self.assertEqual(record["activeAdvisories"], ["TEST-GOJA"])
            self.assertEqual(opener.requests, [
                {"package": {"name": metadata["module"], "ecosystem": "Go"}, "version": metadata["version"]},
                {"commit": metadata["commit"]}])

    def test_goja_mutations_or_unrecorded_code_fail_before_advisory_queries(self):
        for change in ("helper", "original", "unrecorded", "commit"):
            with self.subTest(change=change), tempfile.TemporaryDirectory() as path:
                root = Path(path)
                directory, metadata = self.goja_fixture(root)
                if change == "helper":
                    (directory / "canary_own_data.go").write_bytes(b"changed helper")
                elif change == "original":
                    (directory / "source-0.go").write_bytes(b"changed original")
                elif change == "unrecorded":
                    (directory / "extra.go").write_bytes(b"unrecorded source")
                else:
                    metadata["commit"] = "0" * 40
                    (directory / "CANARY_PATCH.json").write_text(json.dumps(metadata))
                opener = Opener([])
                with self.assertRaises(ValueError):
                    security.vendored_checks(root, root, opener)
                self.assertEqual(opener.requests, [])

    def test_embedded_code_changes_are_fingerprinted(self):
        with tempfile.TemporaryDirectory() as path:
            root = Path(path)
            bundle = root / "apps/node-go/internal/jsonquery/jsvm/jsonata.js"
            bundle.parent.mkdir(parents=True)
            bundle.write_text("original", encoding="utf-8")
            before = security.fingerprint(root)
            bundle.write_text("changed", encoding="utf-8")
            self.assertNotEqual(before, security.fingerprint(root))

    def test_embedded_provenance_hash_and_ecosystem_are_required(self):
        with tempfile.TemporaryDirectory() as path:
            root = Path(path)
            directory = root / "apps/node-go/internal/jsonquery/jsvm"
            directory.mkdir(parents=True)
            (directory / "jsonata.js").write_bytes(b"bundle")
            opener = Opener([Response({}), Response({"vulns": [{"id": "TEST-JS"}]})])
            with self.assertRaises(ValueError):
                security.vendored_checks(root, root, opener)
            metadata = {"module": "jsonata", "version": "2.2.2", "commit": "1" * 40, "bundleSha256": "0" * 64}
            (directory / "upstream.json").write_text(json.dumps(metadata))
            with self.assertRaises(ValueError):
                security.vendored_checks(root, root, opener)
            self.assertEqual(opener.requests, [])
            metadata["bundleSha256"] = hashlib.sha256(b"bundle").hexdigest()
            (directory / "upstream.json").write_text(json.dumps(metadata))
            result = security.vendored_checks(root, root, opener)["embeddedJSONata"]
            self.assertEqual(result["activeAdvisories"], ["TEST-JS"])
            self.assertEqual(opener.requests[0], {"package": {"name": "jsonata", "ecosystem": "npm"}, "version": "2.2.2"})
            self.assertEqual(opener.requests[1], {"commit": "1" * 40})

    def test_all_pages_and_original_query_are_retained(self):
        query = {"commit": "1" * 40}
        opener = Opener([Response({"vulns": [{"id": "TEST-A"}], "next_page_token": "next"}),
                         Response({"vulns": [{"id": "TEST-B", "withdrawn": "2026-10-22T12:00:00Z"}]})])
        with tempfile.TemporaryDirectory() as path:
            findings = security.osv_query(query, Path(path), "test", opener)
            self.assertEqual([x["id"] for x in findings], ["TEST-A", "TEST-B"])
            self.assertEqual(opener.requests, [query, {**query, "page_token": "next"}])
            self.assertEqual(len(list(Path(path).glob("*.json"))), 2)
            self.assertEqual(query, {"commit": "1" * 40})

    def test_incomplete_or_repeating_pagination_is_rejected(self):
        for responses in ([Response({"next_page_token": "same"})] * 2,
                          [Response({"next_page_token": str(i)}) for i in range(8)]):
            opener = Opener(responses)
            with tempfile.TemporaryDirectory() as path, self.assertRaises(ValueError):
                security.osv_query({"commit": "1" * 40}, Path(path), "test", opener)

    def test_invalid_or_oversized_responses_are_not_clean_evidence(self):
        for response in [Response({"error": "not a vulnerability result"}), Response({"vulns": None}),
                         Response({"vulns": [{"id": "TEST-A", "withdrawn": True}]}),
                         Response({"vulns": [{"id": "TEST-A", "withdrawn": "invalid"}]}),
                         Response({"vulns": [{"id": ""}]}), Response(b"x" * ((1 << 20) + 1)),
                         Response(b"not JSON"), Response({}, status=503), Response({}, content_type="text/html")]:
            with tempfile.TemporaryDirectory() as path, self.assertRaises(ValueError):
                security.osv_query({"commit": "1" * 40}, Path(path), "test", Opener([response]))


if __name__ == "__main__":
    unittest.main()
