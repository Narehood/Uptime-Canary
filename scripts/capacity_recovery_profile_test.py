"""The diagnostic collector must refuse workstation runtime execution."""
import importlib.util
from pathlib import Path
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('capacity_profile',
    Path(__file__).with_name('capacity-recovery-profile.py'))
profile = importlib.util.module_from_spec(spec)
spec.loader.exec_module(profile)


class LinuxProfileRefusal(unittest.TestCase):
    def test_non_linux_refuses_before_outputs_or_subprocesses(self):
        """Neither unsupported platform may create output or invoke Go."""
        for system in ('Windows', 'Darwin'):
            with self.subTest(system=system), patch.object(profile.platform, 'system', return_value=system), \
                    patch.object(profile, 'guarded_output') as output, patch.object(profile.subprocess, 'run') as run:
                with self.assertRaisesRegex(SystemExit, 'requires native Linux'):
                    profile.main()
                output.assert_not_called()
                run.assert_not_called()


if __name__ == '__main__':
    unittest.main()
