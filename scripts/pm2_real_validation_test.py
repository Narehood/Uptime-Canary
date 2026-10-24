"""Fail-closed controls for the privileged Linux fixture launcher.

These never create namespaces, invoke sudo, or signal a real process.
"""
import importlib.util
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('pm2_real_validation', Path(__file__).with_name('pm2-real-validation.py'))
launcher = importlib.util.module_from_spec(spec)
spec.loader.exec_module(launcher)


class PM2NamespaceRefusalTests(unittest.TestCase):
    def test_regular_fixture_dependencies_are_admitted(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'node_modules').mkdir()
            (root / 'node_modules/package.json').write_text('{}')
            launcher.require_link_free_fixture(root)

    @unittest.skipUnless(sys.platform == 'linux', 'Linux npm executable links')
    def test_fixture_symlinks_are_refused_without_following_them(self):
        with tempfile.TemporaryDirectory() as directory:
            parent = Path(directory)
            root = parent / 'package'
            (root / 'node_modules/.bin').mkdir(parents=True)
            outside = parent / 'preserved'
            outside.write_text('unchanged')
            link = root / 'node_modules/.bin/tool'
            link.symlink_to(outside)
            with self.assertRaisesRegex(RuntimeError, 'unsupported link'):
                launcher.require_link_free_fixture(root)
            self.assertEqual(outside.read_text(), 'unchanged')
            self.assertTrue(link.is_symlink())

    def test_cleanup_refuses_an_ordinary_process_before_signalling(self):
        with patch.object(launcher, 'namespace_supervisor', return_value=False), patch.object(launcher.os, 'kill') as kill:
            with self.assertRaisesRegex(RuntimeError, 'private PID namespace'):
                launcher.reap_namespace()
            kill.assert_not_called()

    def test_runtime_refuses_ordinary_process_before_import_or_cleanup(self):
        with patch.object(launcher, 'namespace_supervisor', return_value=False), patch.object(launcher.subprocess, 'run') as run:
            with self.assertRaisesRegex(RuntimeError, 'admission failed'):
                launcher.offline(None, 1001, 1001, 'untrusted', '/unused/node', '/unused/canary')
            run.assert_not_called()

    def test_non_linux_entry_refuses_before_any_output_or_privilege_wrapper(self):
        with patch.object(launcher.sys, 'platform', 'win32'), patch.object(launcher.sys, 'argv', ['fixture']), \
                patch.object(launcher, 'guarded_output') as output, patch.object(launcher.subprocess, 'run') as run:
            with self.assertRaisesRegex(RuntimeError, 'native Linux'):
                launcher.main()
            output.assert_not_called()
            run.assert_not_called()

    @unittest.skipUnless(sys.platform == 'linux', 'Linux signal/waitpid constants')
    def test_private_init_reaps_all_adopted_children_before_returning(self):
        with patch.object(launcher, 'namespace_supervisor', return_value=True), patch.object(launcher.os, 'kill') as kill, \
                patch.object(launcher.os, 'waitpid', side_effect=[(40, 0), (41, 0), ChildProcessError]):
            self.assertEqual(launcher.reap_namespace(), 2)
            kill.assert_called_once_with(-1, launcher.signal.SIGKILL)


if __name__ == '__main__':
    unittest.main()
