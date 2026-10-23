"""Small preparation-parser regressions; no downloads or guest processes."""
import importlib.util
import contextlib
import hashlib
import io
from pathlib import Path
import struct
import tempfile
import unittest
from unittest.mock import Mock, patch

spec = importlib.util.spec_from_file_location('alpine_runtime', Path(__file__).with_name('alpine-bounded-runtime.py'))
runtime = importlib.util.module_from_spec(spec)
spec.loader.exec_module(runtime)


class ArchiveInventory(unittest.TestCase):
    def test_windows_listing_keeps_each_member_and_ignores_empty_directory_sizes(self):
        listing = ('Path = C:\\private\\installer.exe\r\nPhysical Size = 100\r\n\r\n'
                   'Path = share\r\nSize = \r\nFolder = +\r\n\r\n'
                   'Path = qemu-system-x86_64.exe\r\nSize = 123\r\n\r\n'
                   'Path = share\\qemu\\bios-256k.bin\r\nSize = 456\r\n')
        with patch.object(runtime, 'command', return_value=listing):
            self.assertEqual(runtime.archive_entries(None, None),
                             {'qemu-system-x86_64.exe': 123, 'share/qemu/bios-256k.bin': 456})

    def test_rejects_traversal_and_absolute_members(self):
        for path in ('../outside', '/outside', 'C:/outside', 'share/../../outside'):
            with self.subTest(path=path), patch.object(runtime, 'command', return_value=f'Path = {path}\nSize = 1\n'):
                with self.assertRaises(RuntimeError):
                    runtime.archive_entries(None, None)

    def test_reads_dll_and_windows_driver_imports_without_loading_them(self):
        for name in (b'kernel32.dll', b'WINSPOOL.DRV'):
            with self.subTest(name=name), tempfile.TemporaryDirectory() as folder:
                data = bytearray(1536)
                struct.pack_into('<I', data, 0x3c, 0x80)
                data[0x80:0x84] = b'PE\0\0'
                struct.pack_into('<H', data, 0x86, 1)
                struct.pack_into('<H', data, 0x94, 240)
                struct.pack_into('<H', data, 0x98, 0x20b)
                struct.pack_into('<II', data, 0x98 + 120, 0x1000, 40)
                struct.pack_into('<IIII', data, 0x188 + 8, 1024, 0x1000, 1024, 0x200)
                struct.pack_into('<I', data, 0x20c, 0x1040)
                data[0x240:0x240 + len(name)] = name
                path = Path(folder) / 'sample.exe'
                path.write_bytes(data)
                self.assertEqual(runtime.imports(path), {name.decode().lower()})


class SerialEvidence(unittest.TestCase):
    def make_serial(self, chunks):
        serial = runtime.Serial.__new__(runtime.Serial)
        serial.process = Mock()
        serial.process.poll.return_value = None
        serial.socket = Mock()
        serial.socket.recv.side_effect = chunks
        serial.total = 0
        serial.evidence = bytearray()
        return serial

    def test_keeps_raw_bytes_across_marker_boundaries(self):
        serial = self.make_serial([b'kernel\xff\nUC_EN', b'D_123:0\n'])
        with patch.object(runtime, 'host_headroom'), contextlib.redirect_stdout(io.StringIO()):
            serial.until(b'UC_END_123:', 1)
        self.assertEqual(serial.evidence, b'kernel\xff\nUC_END_123:0\n')
        self.assertEqual(serial.total, len(serial.evidence))

    def test_log_does_not_depend_on_overwritable_latest_log(self):
        serial = self.make_serial([])
        serial.evidence.extend(b'--- PASS: finite-boundary\nALL_SELECTED_PASSED\n')
        serial.total = len(serial.evidence)
        with tempfile.TemporaryDirectory() as folder:
            path = Path(folder) / 'serial.log'
            latest = Path(folder) / 'latest.log'
            latest.write_bytes(b'unrelated next validation')
            result = {}
            with patch.object(runtime, 'SERIAL_LOG', path):
                runtime.save_serial_log(serial, result)
            self.assertEqual(path.read_bytes(), serial.evidence)
            self.assertEqual(result['serialLogSHA256'], hashlib.sha256(serial.evidence).hexdigest())
            self.assertFalse(result['serialLogTruncated'])

    def test_partial_failure_evidence_is_saved_with_truncation_flag(self):
        serial = self.make_serial([b'oversize'])
        serial.total = 1500 * 1024
        serial.evidence.extend(b'original failure\n')
        with patch.object(runtime, 'host_headroom'):
            with self.assertRaisesRegex(RuntimeError, 'evidence exceeded'):
                serial.until(b'unreached', 1)
        with tempfile.TemporaryDirectory() as folder:
            path = Path(folder) / 'serial.log'
            result = {}
            with patch.object(runtime, 'SERIAL_LOG', path):
                runtime.save_serial_log(serial, result)
            self.assertEqual(path.read_bytes(), b'original failure\n')
            self.assertTrue(result['serialLogTruncated'])


if __name__ == '__main__':
    unittest.main()
