"""Exercise the actual workflow shell with private files and a stubbed runner."""
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
WORKFLOW = (ROOT / '.github/workflows/automation-integrations.yml').read_text()
STEP = WORKFLOW.split('      - name: Fleet, maintenance and registry equivalence and measurements\n', 1)[1]
STEP = STEP.split('\n      - ', 1)[0]
SCRIPT = '\n'.join(line[10:] for line in STEP.split('        run: |\n', 1)[1].splitlines())
UPLOAD = WORKFLOW.split('      - name: Retain additional calculation measurements\n', 1)[1].split('\n      - ', 1)[0]
BASH = shutil.which('bash') or ('C:/Program Files/Git/bin/bash.exe' if os.name == 'nt' else None)

# No Go, Node, real bounded runner, network, or application executable is started.
# Each failure happens before the stub can write replacement evidence.
STUB = '''python3() {
  printf 'started\\n' >> invoked
  if [ "$FIXTURE_MODE" = init-failure ]; then
    printf 'fixture runner initialization failed\\n' >&2
    return 7
  fi
  printf 'current additional proof\\n' > .canary/work/current/latest.log
  printf '{"proof":"additional","exitCode":%s}\\n' "$FIXTURE_STATUS" > .canary/work/current/latest.json
  return "$FIXTURE_STATUS"
}
'''


class EvidenceIsolation(unittest.TestCase):
    def run_step(self, mode='success', status=0, cleanup_failure=False):
        self.assertTrue(BASH, 'Bash is required to exercise the Linux workflow shell')
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            work = root / '.canary/work/current'
            work.mkdir(parents=True)
            if cleanup_failure:
                (work / 'latest.log').mkdir()
                (work / 'latest.log/keep').write_text('not a disposable result file')
            else:
                (work / 'latest.log').write_text('previous dashboard proof')
            (work / 'latest.json').write_text('{"proof":"dashboard"}')
            (work / 'neighbor.json').write_text('keep')
            env = dict(os.environ, FIXTURE_MODE=mode, FIXTURE_STATUS=str(status),
                       GITHUB_OUTPUT='step-output')
            process = subprocess.run([BASH, '--noprofile', '--norc', '-e', '-c', STUB + SCRIPT],
                                     cwd=root, env=env, capture_output=True, text=True, timeout=10)
            files = {path.name: path.read_text() for path in work.iterdir() if path.is_file()}
            output = root / 'step-output'
            return process, files, (root / 'invoked').exists(), output.read_text() if output.exists() else ''

    def test_initialization_failure_cannot_publish_previous_proof(self):
        process, files, invoked, output = self.run_step(mode='init-failure')
        self.assertEqual(process.returncode, 7)
        self.assertTrue(invoked)
        self.assertNotIn('latest.log', files)
        self.assertNotIn('latest.json', files)
        self.assertNotIn('previous dashboard', process.stdout)
        self.assertEqual(files['neighbor.json'], 'keep')
        self.assertIn('evidence_cleared=true', output)

    def test_success_retains_only_current_results(self):
        process, files, invoked, output = self.run_step()
        self.assertEqual(process.returncode, 0, process.stderr)
        self.assertTrue(invoked)
        self.assertEqual(json.loads(files['latest.json']), {'proof': 'additional', 'exitCode': 0})
        self.assertEqual(process.stdout.strip(), 'current additional proof')
        self.assertIn('evidence_cleared=true', output)

    def test_command_failure_retains_current_diagnostics_and_status(self):
        process, files, _, _ = self.run_step(status=9)
        self.assertEqual(process.returncode, 9)
        self.assertEqual(json.loads(files['latest.json']), {'proof': 'additional', 'exitCode': 9})
        self.assertEqual(process.stdout.strip(), 'current additional proof')

    def test_cleanup_failure_stops_before_runner_and_does_not_enable_upload(self):
        process, _, invoked, output = self.run_step(cleanup_failure=True)
        self.assertNotEqual(process.returncode, 0)
        self.assertFalse(invoked)
        self.assertEqual(output, '')

    def test_upload_requires_successful_cleanup_even_after_failure_or_skip(self):
        step_id = re.search(r'^        id: (\w+)$', STEP, re.MULTILINE)
        self.assertIsNotNone(step_id)
        self.assertIn("if: always() && steps." + step_id[1] + ".outputs.evidence_cleared == 'true'", UPLOAD)


if __name__ == '__main__':
    unittest.main()
