"""Release gate: incomplete or skipped proof output cannot look successful."""
import importlib.util
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("process_proofs", Path(__file__).with_name("process-proof-validation.py"))
proofs = importlib.util.module_from_spec(spec)
spec.loader.exec_module(proofs)


class ProofResultTests(unittest.TestCase):
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
