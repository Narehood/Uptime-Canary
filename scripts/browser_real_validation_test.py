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
    def test_userns_policy_attaches_only_to_each_exact_private_binary(self):
        root = '/home/runner/work/canary/canary'
        name = 'canary-browser-' + 'a' * 32
        for arch in ('linux64', 'linux-arm64'):
            binary = root + '/.canary/work/current/outputs/bri/chrome-headless-shell-' + arch + '/chrome-headless-shell'
            source = launcher.apparmor_source(binary, name, root).decode('ascii')
            self.assertIn('profile ' + name + ' "' + binary + '" flags=(unconfined)', source)
            self.assertEqual(source.count('userns,'), 1)
            for forbidden in ('*', '?', '@{', 'include', 'capability', 'sysctl', '--no-sandbox'):
                self.assertNotIn(forbidden, source)

    def test_userns_policy_refuses_foreign_paths_and_rule_injection(self):
        root = '/home/runner/work/canary/canary'
        name = 'canary-browser-' + 'b' * 32
        binary = root + '/.canary/work/current/outputs/bri/chrome-headless-shell-linux64/chrome-headless-shell'
        for other in ('/tmp/chrome', binary + '*', binary + '/child', binary.replace('/bri/', '/bri-other/'), binary + '\nuserns,'):
            with self.subTest(binary=other), self.assertRaises(ValueError):
                launcher.apparmor_source(other, name, root)
        for other in (root + '/..', root + '/.', '//home/runner', root + '"', root + ' ', root + '*', root + '/@{HOME}'):
            with self.subTest(root=other), self.assertRaises(ValueError):
                launcher.apparmor_source(binary, name, other)
        for other in ('chrome', name + '\n', 'canary-browser-' + 'A' * 32):
            with self.subTest(name=other), self.assertRaises(ValueError):
                launcher.apparmor_source(binary, other, root)

    def test_temporary_userns_profile_is_removed_after_runtime_failure(self):
        info, source = {}, b'private fixture policy'
        with patch.object(launcher.subprocess, 'run') as run:
            with self.assertRaisesRegex(RuntimeError, 'runtime failure'):
                with launcher.loaded_apparmor_profile(source, info):
                    raise RuntimeError('runtime failure')
            self.assertEqual([call.args[0] for call in run.call_args_list], [
                ['/usr/bin/sudo', '-n', '/usr/sbin/apparmor_parser', '-K', '-a'],
                ['/usr/bin/sudo', '-n', '/usr/sbin/apparmor_parser', '-K', '-R']])
            for call in run.call_args_list:
                self.assertEqual(call.kwargs['input'], source)
                self.assertTrue(call.kwargs['check'])
                self.assertEqual(call.kwargs['timeout'], 15)
        self.assertEqual(info, {'loaded': True, 'removed': True})

    def test_rejected_profile_add_never_executes_runtime_or_removes_another_profile(self):
        info = {}
        with patch.object(launcher.subprocess, 'run', side_effect=launcher.subprocess.CalledProcessError(1, ['parser'])) as run:
            with self.assertRaises(launcher.subprocess.CalledProcessError):
                with launcher.loaded_apparmor_profile(b'policy', info):
                    self.fail('runtime must not start')
            self.assertEqual(run.call_count, 1)
        self.assertEqual(info, {'loaded': False, 'removed': False})

    def test_failed_profile_removal_cannot_report_cleanup_success(self):
        info = {}
        with patch.object(launcher.subprocess, 'run', side_effect=[None, launcher.subprocess.CalledProcessError(1, ['parser'])]):
            with self.assertRaises(launcher.subprocess.CalledProcessError):
                with launcher.loaded_apparmor_profile(b'policy', info):
                    pass
        self.assertEqual(info, {'loaded': True, 'removed': False})

    def test_ambiguous_profile_load_timeout_is_cleaned_up_and_stays_failed(self):
        info = {}
        with patch.object(launcher.subprocess, 'run', side_effect=[launcher.subprocess.TimeoutExpired(['parser'], 15), None]) as run:
            with self.assertRaises(launcher.subprocess.TimeoutExpired):
                with launcher.loaded_apparmor_profile(b'policy', info):
                    self.fail('runtime must not start')
            self.assertEqual(run.call_args_list[-1].args[0][-1], '-R')
        self.assertEqual(info, {'loaded': False, 'removed': True})

    def test_userns_profile_host_admission_precedes_privileged_command(self):
        with patch.object(launcher.sys, 'platform', 'win32'), patch.object(launcher.subprocess, 'run') as run:
            with self.assertRaisesRegex(RuntimeError, 'AppArmor admission'):
                with launcher.browser_apparmor(Path('unused'), {}):
                    self.fail('non-Linux runtime must not start')
            run.assert_not_called()

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
