"""Portable notice ordering and retained-attribution checks; no installs."""
import contextlib
import hashlib
import importlib.util
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('service_notices', Path(__file__).with_name('game-query-notices.py'))
notices = importlib.util.module_from_spec(spec)
spec.loader.exec_module(notices)


class ServiceNoticeTests(unittest.TestCase):
    def fixture(self, root, fallback=False):
        service = root / 'services/game-query'
        directory = service / 'node_modules/example'
        directory.mkdir(parents=True)
        package = {'name': 'example', 'version': '1.0.0'}
        lock = {'packages': {'': {}, 'node_modules/example': {'version': '1.0.0', 'integrity': 'sha512-fixture'}}}
        (service / 'package-lock.json').write_text(json.dumps(lock), encoding='utf-8')
        (directory / 'package.json').write_text(json.dumps(package), encoding='utf-8')
        if fallback:
            (directory / 'component.json').write_text('{"license":"MIT"}\n', encoding='utf-8')
            (directory / 'README.md').write_text('MIT\nRetained original attribution.\n', encoding='utf-8')
        else:
            (directory / 'LICENSE').write_text('Original license terms.\n', encoding='utf-8')
            (directory / 'authors').write_text('Original authors.\n', encoding='utf-8')
        return service, directory

    def run_generator(self, root, check=False):
        with patch.object(notices, 'ROOT', root), patch('sys.argv', ['notices', *(['--check'] if check else [])]), contextlib.redirect_stdout(io.StringIO()):
            notices.main()

    def verify(self, root, service, directory, expected):
        manifest = json.loads((service / 'third-party-manifest.json').read_text(encoding='utf-8'))
        entries = manifest['packages'][0]['notices']
        self.assertEqual([entry['file'] for entry in entries], expected)
        body = (service / 'THIRD_PARTY_NOTICES.txt').read_text(encoding='utf-8')
        positions = []
        for entry in entries:
            raw = (directory / entry['file']).read_bytes().decode('utf-8-sig').replace('\r\n', '\n')
            self.assertEqual(entry['sha256'], hashlib.sha256(raw.encode()).hexdigest())
            self.assertIn(raw.rstrip(), body)
            positions.append(body.index('--- ' + entry['file'] + ' ---'))
        self.assertEqual(positions, sorted(positions))
        self.run_generator(root, check=True)

    def test_standard_notices_ignore_native_path_sort_order(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            service, directory = self.fixture(root)
            # A Windows path comparison puts lower-case authors before LICENSE;
            # the reader may also enumerate in either order on any filesystem.
            entries = [('authors', 'Original authors.\n'), ('LICENSE', 'Original license terms.\n')]
            for incoming in (entries, list(reversed(entries))):
                with self.subTest(incoming=incoming), patch.object(notices.notices, 'texts', return_value=incoming):
                    self.run_generator(root)
                    self.verify(root, service, directory, ['LICENSE', 'authors'])

    def test_readme_component_fallback_keeps_declaration_and_canonical_order(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            service, directory = self.fixture(root, fallback=True)
            self.run_generator(root)
            self.verify(root, service, directory, ['package.json', 'README.md', 'component.json'])

    def test_changed_retained_text_still_fails_check(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            service, _ = self.fixture(root)
            self.run_generator(root)
            (service / 'THIRD_PARTY_NOTICES.txt').write_text('omitted attribution', encoding='utf-8')
            with self.assertRaisesRegex(ValueError, 'Service notices changed'):
                self.run_generator(root, check=True)

    def test_fallback_never_invents_a_license(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            _, directory = self.fixture(root, fallback=True)
            (directory / 'component.json').write_text('{"license":"UNKNOWN"}\n', encoding='utf-8')
            with self.assertRaisesRegex(ValueError, 'no identifiable license'):
                self.run_generator(root)


if __name__ == '__main__':
    unittest.main()
