import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time
import unittest
from unittest.mock import patch

import bounded_validation as guard


class StorageGuards(unittest.TestCase):
    def test_digest_detects_frontend_and_validation_source_changes(self):
        with tempfile.TemporaryDirectory() as folder:
            base = Path(folder)
            for name in ('form.ts', 'view.tsx', 'styles.css', 'index.html', 'validator.py',
                         'Dockerfile.node', 'compose.yaml', 'web.conf', '.gitattributes'):
                path = base / name
                path.write_text('before', encoding='utf-8')
                original = guard.source_digest(base)
                path.write_text('after', encoding='utf-8')
                self.assertNotEqual(guard.source_digest(base), original, name)

    def test_digest_excludes_private_environment_and_generated_outputs(self):
        with tempfile.TemporaryDirectory() as folder:
            base = Path(folder)
            (base / 'source.ts').write_text('keep', encoding='utf-8')
            original = guard.source_digest(base)
            for name in ('node_modules', 'dist', '.canary', '__pycache__'):
                directory = base / name
                directory.mkdir()
                (directory / 'generated.ts').write_text('generated', encoding='utf-8')
            # Synthetic fixture data only; no user environment file is read.
            (base / '.env').write_text('fixture-only', encoding='utf-8')
            self.assertEqual(guard.source_digest(base), original)

    def test_cleanup_refuses_parent_and_sibling(self):
        with tempfile.TemporaryDirectory() as folder:
            base = Path(folder)
            with self.assertRaises(guard.StorageError):
                guard.owned_path(base, base)
            with self.assertRaises(guard.StorageError):
                guard.owned_path(base / '..' / 'other', base)

    def test_scratch_cleanup_preserves_neighbor(self):
        with tempfile.TemporaryDirectory() as folder:
            work = Path(folder) / 'current'
            scratch = work / 'tmp'
            scratch.mkdir(parents=True)
            (scratch / 'artifact').write_bytes(b'disposable')
            survivor = work / 'source'
            survivor.write_bytes(b'keep')
            with patch.object(guard, 'WORK', work):
                guard.remove_scratch(scratch)
            self.assertFalse(scratch.exists())
            self.assertEqual(survivor.read_bytes(), b'keep')

    def test_cache_trim_is_bounded_and_keeps_root_metadata(self):
        with tempfile.TemporaryDirectory() as folder:
            cache = Path(folder)
            (cache / '00').mkdir()
            (cache / 'README').write_bytes(b'metadata')
            old, recent = cache / '00' / 'old', cache / '00' / 'recent'
            old.write_bytes(b'a' * 128)
            recent.write_bytes(b'b' * 64)
            os.utime(old, (1, 1))
            with patch.object(guard, 'CACHE', cache), patch.object(guard, 'CACHE_TARGET', 80):
                guard.trim_cache()
            self.assertFalse(old.exists())
            self.assertEqual(recent.read_bytes(), b'b' * 64)
            self.assertEqual((cache / 'README').read_bytes(), b'metadata')

    def test_budget_detects_each_growth_source(self):
        baseline = {'moduleBytes': 100}
        normal = {'projectBytes': 100, 'scratchBytes': 0, 'moduleBytes': 100,
                  'externalBuildCacheBytes': 0}
        self.assertIsNone(guard.breach(normal, baseline))
        for field, excessive in [('projectBytes', guard.PROJECT_LIMIT + 1),
                                 ('scratchBytes', guard.SCRATCH_LIMIT + 1),
                                 ('moduleBytes', 101 + guard.MODULE_GROWTH_LIMIT),
                                 ('externalBuildCacheBytes', 17 * guard.MIB)]:
            with self.subTest(field=field):
                self.assertIsNotNone(guard.breach({**normal, field: excessive}, baseline))

    def test_disposable_outputs_share_the_scratch_budget(self):
        with tempfile.TemporaryDirectory() as folder:
            base = Path(folder)
            for name, count in (('tmp', 7), ('outputs', 11)):
                path = base / name
                path.mkdir()
                (path / 'data').write_bytes(b'x' * count)
            with patch.object(guard, 'WORK', base):
                measured = guard.usage(base / 'missing-modules', base / 'missing-cache')
            self.assertEqual(measured['scratchBytes'], 18)

    def test_an_already_selected_owned_cache_is_not_counted_as_external_growth(self):
        with tempfile.TemporaryDirectory() as folder:
            cache = Path(folder) / 'owned-cache'
            cache.mkdir()
            with patch.object(guard, 'CACHE', cache), patch.object(guard, 'size', return_value=17 * guard.MIB):
                measured = guard.usage(Path(folder) / 'modules', cache)
                self.assertEqual(measured['cacheBytes'], 17 * guard.MIB)
                self.assertEqual(measured['externalBuildCacheBytes'], 0)
                foreign = guard.usage(Path(folder) / 'modules', Path(folder) / 'foreign-cache')
                self.assertEqual(foreign['externalBuildCacheBytes'], 17 * guard.MIB)

    def test_stop_only_the_started_process(self):
        options = {'creationflags': subprocess.CREATE_NEW_PROCESS_GROUP | subprocess.CREATE_NO_WINDOW} if os.name == 'nt' else {'start_new_session': True}
        child = subprocess.Popen([sys.executable, '-c', 'import time; time.sleep(60)'], **options)
        try:
            guard.stop_tree(child)
            self.assertIsNotNone(child.poll())
        finally:
            if child.poll() is None:
                child.kill()
                child.wait()

    def test_stop_reaps_descendants_after_the_command_parent_exits(self):
        with tempfile.TemporaryDirectory() as folder:
            marker = Path(folder) / 'late-child.txt'
            child_code = ('import time; from pathlib import Path; time.sleep(1); '
                          'Path(' + repr(str(marker)) + ').write_text("fixture only")')
            parent_code = ('import subprocess,sys; subprocess.Popen('
                           '[sys.executable,"-B","-c",' + repr(child_code) + '],'
                           'stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)')
            options = {'creationflags': subprocess.CREATE_NEW_PROCESS_GROUP | subprocess.CREATE_NO_WINDOW} if os.name == 'nt' else {'start_new_session': True}
            launch = getattr(guard, 'start_command', None)
            parent = (launch([sys.executable, '-B', '-c', parent_code]) if launch else
                      subprocess.Popen([sys.executable, '-B', '-c', parent_code], **options))
            try:
                self.assertEqual(parent.wait(timeout=5), 0)
                guard.stop_tree(parent)
                time.sleep(1.5)
                self.assertFalse(marker.exists(), 'a descendant survived command cleanup')
            finally:
                guard.stop_tree(parent)

    def test_command_preserves_piped_input_output_and_exit_status(self):
        command = [sys.executable, '-B', '-c',
                   'import sys; print(sys.stdin.read()); print("diagnostic", file=sys.stderr); sys.exit(7)']
        process = guard.start_command(command, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                      stderr=subprocess.PIPE, text=True)
        try:
            output, errors = process.communicate('private fixture', timeout=5)
            self.assertEqual(process.returncode, 7)
            self.assertEqual(output.strip(), 'private fixture')
            self.assertEqual(errors.strip(), 'diagnostic')
        finally:
            guard.stop_tree(process)

    def test_command_accepts_an_executable_without_arguments(self):
        process = guard.start_command([sys.executable], stdin=subprocess.PIPE,
                                      stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        try:
            output, errors = process.communicate('print("single executable")\n', timeout=5)
            self.assertEqual(process.returncode, 0, errors)
            self.assertEqual(output.strip(), 'single executable')
        finally:
            guard.stop_tree(process)


if __name__ == '__main__':
    unittest.main()
