"""Host refusal and hostile-archive controls; never launch Chrome or sudo."""
import importlib.util
from pathlib import Path
import stat
import tempfile
import unittest
from unittest.mock import patch
import zipfile

spec = importlib.util.spec_from_file_location('browser_linux_launcher', Path(__file__).with_name('browser-linux-validation.py'))
launcher = importlib.util.module_from_spec(spec)
spec.loader.exec_module(launcher)


class BrowserFixtureTests(unittest.TestCase):
    def archive(self, parent, names):
        path = parent / 'private.zip'
        with zipfile.ZipFile(path, 'w') as output:
            for name, mode in names:
                member = zipfile.ZipInfo(name)
                # ZipInfo normalizes the host OS separator on construction.
                # Restore the literal hostile wire name, including on Windows.
                member.filename = name
                member.external_attr = mode << 16
                output.writestr(member, b'private fixture')
        return path

    def test_extracts_only_declared_regular_files(self):
        with tempfile.TemporaryDirectory() as name:
            parent = Path(name)
            archive = self.archive(parent, [('shell/chrome-headless-shell', stat.S_IFREG | 0o755)])
            target = launcher.extract_fixture(archive, parent / 'output', 'shell')
            self.assertEqual(target.read_bytes(), b'private fixture')

    def test_rejects_traversal_foreign_root_and_platform_paths_before_writing(self):
        for member in ('../escape', '/shell/escape', 'shell/../escape', 'other/file', 'shell\\file', 'shell/C:escape'):
            with self.subTest(member=member), tempfile.TemporaryDirectory() as name:
                parent = Path(name)
                archive = self.archive(parent, [(member, stat.S_IFREG | 0o600)])
                with self.assertRaisesRegex(ValueError, 'Unsupported'):
                    launcher.extract_fixture(archive, parent / 'output', 'shell')
                self.assertFalse((parent / 'output').exists())

    def test_rejects_symlinks_devices_and_duplicate_members(self):
        for mode in (stat.S_IFLNK | 0o777, stat.S_IFCHR | 0o600):
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as name:
                parent = Path(name)
                archive = self.archive(parent, [('shell/chrome-headless-shell', mode)])
                with self.assertRaisesRegex(ValueError, 'Unsupported'):
                    launcher.extract_fixture(archive, parent / 'output', 'shell')
        with tempfile.TemporaryDirectory() as name:
            parent = Path(name)
            with self.assertWarns(UserWarning):
                archive = self.archive(parent, [('shell/file', stat.S_IFREG)] * 2)
            with self.assertRaisesRegex(ValueError, 'Unsupported'):
                launcher.extract_fixture(archive, parent / 'output', 'shell')

    def test_enforces_unpacked_size_before_writing(self):
        with tempfile.TemporaryDirectory() as name:
            parent = Path(name)
            archive = self.archive(parent, [('shell/chrome-headless-shell', stat.S_IFREG)])
            with patch.object(launcher, 'MAX_UNPACKED', 4), self.assertRaisesRegex(ValueError, 'bounds'):
                launcher.extract_fixture(archive, parent / 'output', 'shell')
            self.assertFalse((parent / 'output').exists())

    def test_existing_output_is_never_overwritten(self):
        with tempfile.TemporaryDirectory() as name:
            parent = Path(name)
            target = parent / 'output/shell/chrome-headless-shell'
            target.parent.mkdir(parents=True)
            target.write_bytes(b'preserved')
            archive = self.archive(parent, [('shell/chrome-headless-shell', stat.S_IFREG)])
            with self.assertRaises(FileExistsError):
                launcher.extract_fixture(archive, parent / 'output', 'shell')
            self.assertEqual(target.read_bytes(), b'preserved')

    def test_non_linux_refuses_before_output_or_privileged_command(self):
        with patch.object(launcher.sys, 'platform', 'win32'), patch.object(launcher.sys, 'argv', ['fixture', '--binary', 'unused']), \
                patch.object(launcher, 'guarded_output') as output, patch.object(launcher.subprocess, 'run') as run:
            with self.assertRaisesRegex(RuntimeError, 'native Linux'):
                launcher.main()
            output.assert_not_called()
            run.assert_not_called()

    def test_runtime_admission_refuses_before_signal_or_browser_execution(self):
        with patch.object(launcher, 'admitted_runtime', return_value=False), \
                patch.object(launcher.subprocess, 'run') as run, patch.object(launcher.namespace, 'reap_namespace') as reap:
            class Args:
                offline = launcher.ROOT / '.canary/work/current/outputs/bri'
            with self.assertRaisesRegex(RuntimeError, 'admission failed'):
                launcher.offline(Args())
            run.assert_not_called()
            reap.assert_not_called()


if __name__ == '__main__':
    unittest.main()
