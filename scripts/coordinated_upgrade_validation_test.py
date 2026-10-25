"""Metadata and early refusal controls; no image pull or executable is run."""
import copy
import importlib.util
from pathlib import Path
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('coordinated_upgrade', Path(__file__).with_name('coordinated-upgrade-validation.py'))
validation = importlib.util.module_from_spec(spec)
spec.loader.exec_module(validation)


class UpgradeValidationTests(unittest.TestCase):
    def test_published_baseline_binds_digest_source_and_native_platform(self):
        image = {'Id': 'sha256:' + 'a' * 64, 'Os': 'linux', 'Architecture': 'amd64',
                 'RepoDigests': [validation.BASE_IMAGE], 'Config': {'Labels': {
                     'org.opencontainers.image.revision': validation.BASE_COMMIT,
                     'org.opencontainers.image.source': 'https://github.com/Narehood/Uptime-Canary'}}}
        self.assertEqual(validation.admit_baseline([image]), image['Id'])
        for path, value in [(('Os',), 'windows'), (('Architecture',), 'arm64'), (('RepoDigests',), []),
                            (('Config', 'Labels', 'org.opencontainers.image.revision'), 'b' * 40),
                            (('Config', 'Labels', 'org.opencontainers.image.source'), 'https://example.invalid/source')]:
            changed = copy.deepcopy(image)
            target = changed
            for part in path[:-1]:
                target = target[part]
            target[path[-1]] = value
            with self.subTest(path=path), self.assertRaises(ValueError):
                validation.admit_baseline([changed])
        for malformed in (None, {}, [], [image, image]):
            with self.subTest(malformed=malformed), self.assertRaises(ValueError):
                validation.admit_baseline(malformed)

    def test_unsupported_host_refuses_before_guard_or_docker(self):
        for system, machine in [('Windows', 'AMD64'), ('Linux', 'aarch64'), ('Linux', 'unknown'), ('Darwin', 'x86_64')]:
            with self.subTest(system=system, machine=machine), patch.object(validation.platform, 'system', return_value=system), \
                    patch.object(validation.platform, 'machine', return_value=machine), \
                    patch.object(validation, 'guarded_output') as guard, patch.object(validation.subprocess, 'run') as run, \
                    patch('sys.argv', ['fixture', '--binary', 'unused']):
                with self.assertRaisesRegex(ValueError, 'native Linux amd64'):
                    validation.main()
                guard.assert_not_called()
                run.assert_not_called()


if __name__ == '__main__':
    unittest.main()
