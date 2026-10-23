"""Offline checks for embedded-code attribution and exact asset identity."""
import hashlib
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("notices", Path(__file__).with_name("third-party-notices.py"))
notices = importlib.util.module_from_spec(spec)
spec.loader.exec_module(notices)


class EmbeddedNoticeTests(unittest.TestCase):
    def test_absent_asset_needs_no_javascript_entry(self):
        with tempfile.TemporaryDirectory() as temporary:
            self.assertIsNone(notices.javascript_notice(Path(temporary)))

    def test_attribution_cannot_be_omitted_or_detached_from_bundle(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            directory = root / "apps/node-go/internal/jsonquery/jsvm"
            directory.mkdir(parents=True)
            bundle = directory / "jsonata.js"
            bundle.write_bytes(b"/* embedded code */")
            metadata = {"module": "jsonata", "version": "2.2.2", "commit": "a" * 40,
                        "bundleSha256": hashlib.sha256(bundle.read_bytes()).hexdigest()}
            with self.assertRaises(RuntimeError):
                notices.javascript_notice(root)
            (directory / "upstream.json").write_text(json.dumps(metadata), encoding="utf-8")
            with self.assertRaises(RuntimeError):
                notices.javascript_notice(root)
            (directory / "LICENSE").write_text("MIT attribution", encoding="utf-8")
            with self.assertRaises(RuntimeError):
                notices.javascript_notice(root)
            (directory / "NOTICE.txt").write_text("Upstream source and local changes", encoding="utf-8")
            result = notices.javascript_notice(root)
            self.assertEqual(result[1:4], ("2.2.2", "a" * 40, metadata["bundleSha256"]))
            self.assertEqual(len(result[4]), 2)
            bundle.write_bytes(b"changed")
            with self.assertRaises(RuntimeError):
                notices.javascript_notice(root)

    def test_malformed_identity_cannot_generate_plausible_notice(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            directory = root / "apps/node-go/internal/jsonquery/jsvm"
            directory.mkdir(parents=True)
            (directory / "jsonata.js").write_bytes(b"x")
            for key, invalid in (("module", "other"), ("version", None), ("commit", "tip"),
                                 ("bundleSha256", "wrong")):
                metadata = {"module": "jsonata", "version": "2.2.2", "commit": "b" * 40,
                            "bundleSha256": hashlib.sha256(b"x").hexdigest()}
                metadata[key] = invalid
                (directory / "upstream.json").write_text(json.dumps(metadata), encoding="utf-8")
                with self.subTest(key=key), self.assertRaises(RuntimeError):
                    notices.javascript_notice(root)


if __name__ == "__main__":
    unittest.main()
