"""Offline image-scan admission regressions; no Docker or scanner execution."""
import copy
import importlib.util
import json
import os
from pathlib import Path
import shlex
import sys
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('image_security', Path(__file__).with_name('image-security-check.py'))
scan = importlib.util.module_from_spec(spec)
spec.loader.exec_module(scan)


class ImageScanEvidenceTests(unittest.TestCase):
    def report(self):
        return {'SchemaVersion': 2, 'Metadata': {'ImageID': 'sha256:' + 'a' * 64,
                'OS': {'Family': 'alpine', 'Name': '3.24.3'}},
                'Results': [{'Target': 'fixture', 'Class': 'os-pkgs', 'Type': 'alpine',
                             'Packages': [{'Name': 'musl', 'Version': 'fixture'}]}]}

    def test_complete_clean_inventory_is_admitted(self):
        self.assertTrue(scan.summarize(self.report())['passed'])

    def test_empty_or_malformed_evidence_fails_closed(self):
        for data in ({}, {'SchemaVersion': 2}, {**self.report(), 'Results': []},
                     {**self.report(), 'Results': [{}]}):
            with self.assertRaises((ValueError, TypeError)):
                scan.summarize(data)

    def test_stale_wrong_or_unsupported_os_is_not_a_clean_scan(self):
        for system in ({'Family': 'alpine', 'Name': '3.24.2'}, {'Family': 'debian', 'Name': '13'},
                       {'Family': 'alpine', 'Name': '3.24.3', 'EOSL': True}):
            data = self.report(); data['Metadata']['OS'] = system
            with self.assertRaises(ValueError):
                scan.summarize(data)

    def test_high_critical_and_unfixed_findings_block_admission(self):
        for severity in ('HIGH', 'CRITICAL'):
            data = self.report()
            data['Results'][0]['Vulnerabilities'] = [{'VulnerabilityID': 'CVE-fixture', 'PkgName': 'musl',
                'InstalledVersion': 'old', 'Severity': severity}]
            self.assertFalse(scan.summarize(data)['passed'])

    def test_lower_severities_are_retained_not_called_cve_free(self):
        data = self.report()
        data['Results'][0]['Vulnerabilities'] = [{'VulnerabilityID': 'CVE-fixture', 'PkgName': 'musl',
            'InstalledVersion': 'old', 'FixedVersion': 'new', 'Severity': 'MEDIUM'}]
        result = scan.summarize(copy.deepcopy(data))
        self.assertEqual(result['vulnerabilities']['MEDIUM'], 1)
        self.assertEqual(result['findings'][0]['FixedVersion'], 'new')
        self.assertEqual(len(data['Results'][0]['Vulnerabilities']), 1)

    def test_scanner_child_uses_created_guarded_temporary_directory(self):
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory)

            def run(command, *, env, check, timeout):
                for key in ('TMPDIR', 'TMP', 'TEMP'):
                    self.assertEqual(env[key], str(output / 'tmp'))
                self.assertTrue((output / 'tmp').is_dir())
                (output / 'scan.json').write_text(json.dumps(self.report()), encoding='utf-8')

            version = json.dumps({'Version': '0.76.2', 'VulnerabilityDB': {'UpdatedAt': '2026-11-05T00:00:00Z'}}).encode()
            with patch.object(scan, 'guarded_output', return_value=output), \
                    patch.object(scan.subprocess, 'run', side_effect=run), \
                    patch.object(scan.subprocess, 'check_output', return_value=version), \
                    patch.object(scan, 'retain_summary'), patch('builtins.print'), \
                    patch.dict(os.environ, {'TMPDIR': 'outside-guard', 'TMP': 'outside-guard', 'TEMP': 'outside-guard'}), \
                    patch.object(sys, 'argv', ['image-security-check.py', '--image', 'fixture', '--source', 'docker']):
                self.assertEqual(scan.main(), 0)

    def test_query_smoke_supplies_a_valid_fixed_test_credential(self):
        workflow = (Path(__file__).resolve().parents[1] / '.github/workflows/container-images.yml').read_text(encoding='utf-8')
        smoke = workflow.split('name: Smoke test isolated query-service image', 1)[1]
        command = next(line.strip() for line in smoke.splitlines() if line.strip().startswith('docker run '))
        environment = [part for part in shlex.split(command) if part.startswith('CANARY_GAME_QUERY_TOKEN=')]
        self.assertEqual(len(environment), 1, 'the service refuses startup without its token')
        token = environment[0].split('=', 1)[1]
        self.assertTrue(token.startswith('canary-ci-'))
        self.assertTrue(24 <= len(token) <= 256 and all('!' <= c <= '~' for c in token))


if __name__ == '__main__':
    unittest.main()
