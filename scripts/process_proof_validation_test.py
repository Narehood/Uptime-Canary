"""Release gate: incomplete or skipped proof output cannot look successful."""
import importlib.util
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("process_proofs", Path(__file__).with_name("process-proof-validation.py"))
proofs = importlib.util.module_from_spec(spec)
spec.loader.exec_module(proofs)


class ProofResultTests(unittest.TestCase):
    def test_publication_fixture_rejects_undeclared_production_or_module_drift(self):
        for name in ("go.mod", "go.sum", "apps/node-go/internal/canary/unreviewed.go",
                     "apps/node-go/internal/jsonquery/jsvm/runtime.js"):
            with self.subTest(name=name), tempfile.TemporaryDirectory() as path:
                with patch.object(proofs.subprocess, "check_output", side_effect=[name + "\n", ""]):
                    with self.assertRaisesRegex(ValueError, "Source diverged"):
                        proofs.publication_legacy_fixture(Path(path), Path(path))
                self.assertEqual(list(Path(path).iterdir()), [], "drift refusal must precede fixture writes")

    def test_publication_fixture_restores_only_declared_fragments_and_absent_file(self):
        raw = b"package canary\n// Small synthetic historical fragment, never executed.\n"
        with tempfile.TemporaryDirectory() as path:
            source, output = Path(path) / "checkout", Path(path)
            with patch.object(proofs.subprocess, "check_output", side_effect=["", "", *([raw] * 5)]):
                overlay, report = proofs.publication_legacy_fixture(source, output)
            replacements = json.loads(overlay.read_text())["Replace"]
            added = str(source / "apps/node-go/internal/canary/public_availability.go")
            self.assertEqual(replacements.pop(added), "", "added runtime file must be absent from the old build")
            self.assertEqual(len(replacements), 5)
            self.assertEqual(len(report["overlaySha256"]), 5)
            for original, backing in replacements.items():
                self.assertTrue(Path(original).is_relative_to(source))
                self.assertTrue(Path(backing).is_relative_to(output))
                self.assertEqual(Path(backing).read_bytes(), raw)
            self.assertFalse(source.exists(), "the fixture must never copy or modify a checkout")

    def test_standalone_http_fixture_has_no_repository_import_dependency(self):
        with tempfile.TemporaryDirectory() as path:
            fixture = Path(path) / "fixture.py"
            fixture.write_bytes(Path(__file__).with_name("alpine-validation.py").read_bytes())
            command = [sys.executable, "-I", "-S", "-c",
                       "import runpy,sys;runpy.run_path(sys.argv[1],run_name='fixture_import')", str(fixture)]
            result = subprocess.run(command, cwd=path, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
            self.assertEqual(result.returncode, 0, result.stdout.decode(errors="replace"))

    def test_complete_pass(self):
        values = {"tests": 18, "pass": 18, "fail": 0, "cancelled": 0, "skipped": 0, "todo": 0}
        self.assertEqual(proofs.tap_counts("\n".join(f"# {k} {v}" for k, v in values.items()))["pass"], 18)

    def test_incomplete_failed_or_skipped_output(self):
        for changed in ({"tests": 0, "pass": 0}, {"fail": 1}, {"cancelled": 1},
                        {"skipped": 1}, {"todo": 1}, {"pass": 17}, {"tests": None}):
            with self.subTest(changed=changed):
                values = {"tests": 18, "pass": 18, "fail": 0, "cancelled": 0, "skipped": 0, "todo": 0}
                values.update(changed)
                raw = "\n".join(f"# {k} {v}" for k, v in values.items() if v is not None)
                with self.assertRaises(ValueError):
                    proofs.tap_counts(raw)


if __name__ == "__main__":
    unittest.main()
