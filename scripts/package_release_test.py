"""Small synthetic files test packaging; no production binary is built or executed."""
import hashlib
import importlib.util
import json
from pathlib import Path
import struct
import tarfile
import tempfile
import unittest
from unittest.mock import patch
import zipfile

spec = importlib.util.spec_from_file_location("release_package", Path(__file__).with_name("package-release.py"))
release = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)
COMMIT = "a" * 40


def binary_bytes(target):
    raw = bytearray(128)
    if target.startswith("linux-"):
        raw[:8] = b"\x7fELF\x02\x01\x01\x00"
        struct.pack_into("<HH", raw, 16, 2, {"linux-amd64": 62, "linux-arm64": 183}[target])
    else:
        raw[:2] = b"MZ"
        struct.pack_into("<I", raw, 60, 64)
        raw[64:68] = b"PE\x00\x00"
        struct.pack_into("<H", raw, 68, 0x8664)
        struct.pack_into("<H", raw, 88, 0x20B)
    return bytes(raw) + b"synthetic fixture, never executed\x00" * 8


class ReleasePackageTests(unittest.TestCase):
    def fixture(self, folder, target="linux-amd64"):
        root = Path(folder)
        notices = root / "notices"
        notices.mkdir()
        (notices / "LICENSE").write_bytes(b"Fixture project license\n")
        (notices / "THIRD_PARTY_NOTICES.txt").write_bytes(b"Fixture full upstream license text\n")
        binary = root / "binary"
        binary.write_bytes(binary_bytes(target))
        return binary, notices

    def make(self, binary, notices, output, target="linux-amd64", version="v0.1.0-rc.1", commit=COMMIT):
        return release.package(binary, output, version, commit, target, notice_root=notices)

    def assert_contents(self, contents, binary, notices, target):
        name = "canary.exe" if target.startswith("windows-") else "canary"
        self.assertEqual(set(contents), {name, "LICENSE", "THIRD_PARTY_NOTICES.txt", "release.json"})
        self.assertEqual(contents[name], binary.read_bytes())
        metadata = json.loads(contents["release.json"])
        self.assertEqual(metadata["version"], "0.1.0-rc.1")
        self.assertEqual(metadata["sourceCommit"], COMMIT)
        self.assertEqual(metadata["target"], target)
        for entry in (name, "LICENSE", "THIRD_PARTY_NOTICES.txt"):
            self.assertEqual(metadata["files"][entry]["sha256"], hashlib.sha256(contents[entry]).hexdigest())
            self.assertEqual(metadata["files"][entry]["bytes"], len(contents[entry]))
        for entry in ("LICENSE", "THIRD_PARTY_NOTICES.txt"):
            self.assertEqual(contents[entry], (notices / entry).read_bytes())

    def assert_checksum(self, result):
        path = Path(result["archive"])
        self.assertEqual(result["sha256"], hashlib.sha256(path.read_bytes()).hexdigest())
        self.assertEqual(Path(result["checksum"]).read_text(), result["sha256"] + "  " + path.name + "\n")
        self.assertEqual(result["bytes"], path.stat().st_size)

    def test_linux_targets_contain_exact_bytes_licenses_hashes_and_executable_mode(self):
        for target in ("linux-amd64", "linux-arm64"):
            with self.subTest(target=target), tempfile.TemporaryDirectory() as folder:
                binary, notices = self.fixture(folder, target)
                result = self.make(binary, notices, Path(folder) / "output", target)
                with tarfile.open(result["archive"], "r:gz") as archive:
                    members = archive.getmembers()
                    self.assertTrue(all(m.isfile() and m.name.count("/") == 1 and m.uid == m.gid == m.mtime == 0 for m in members))
                    self.assertEqual({m.mode for m in members if m.name.endswith("/canary")}, {0o755})
                    self.assertTrue(all(m.mode == 0o644 for m in members if not m.name.endswith("/canary")))
                    contents = {m.name.split("/")[1]: archive.extractfile(m).read() for m in members}
                self.assert_contents(contents, binary, notices, target)
                self.assert_checksum(result)
                self.assertFalse(list((Path(folder) / "output").glob(".package-*")))

    def test_windows_zip_bytes_metadata_and_determinism(self):
        with tempfile.TemporaryDirectory() as folder:
            binary, notices = self.fixture(folder, "windows-amd64")
            a = self.make(binary, notices, Path(folder) / "a", "windows-amd64")
            b = self.make(binary, notices, Path(folder) / "b", "windows-amd64")
            self.assertEqual(a["sha256"], b["sha256"])
            with zipfile.ZipFile(a["archive"]) as archive:
                self.assertTrue(all(info.date_time == (1980, 1, 1, 0, 0, 0) for info in archive.infolist()))
                self.assertTrue(all(not info.filename.startswith("/") and ".." not in info.filename.split("/") for info in archive.infolist()))
                contents = {name.split("/")[1]: archive.read(name) for name in archive.namelist()}
            self.assert_contents(contents, binary, notices, "windows-amd64")
            self.assert_checksum(a)

    def test_tar_is_reproducible_across_paths_and_input_timestamps(self):
        with tempfile.TemporaryDirectory() as folder:
            binary, notices = self.fixture(folder)
            first = self.make(binary, notices, Path(folder) / "a")
            release.os.utime(binary, (1, 1))
            second = self.make(binary, notices, Path(folder) / "b")
            self.assertEqual(first["sha256"], second["sha256"])

    def test_invalid_labels_targets_and_formats_publish_nothing(self):
        with tempfile.TemporaryDirectory() as folder:
            binary, notices = self.fixture(folder)
            output = Path(folder) / "output"
            self.assertEqual(release.version_value("1.2.3+" + "a" * 58), "1.2.3+" + "a" * 58)
            for version in ("../bad", "1.2", "01.2.3", "1.2.3-01", "1.2.3-a..b", "1.2.3+" + "a" * 59):
                with self.subTest(version=version), self.assertRaises(ValueError):
                    self.make(binary, notices, output, version=version)
            for commit in ("HEAD", "A" * 40, "a" * 39):
                with self.subTest(commit=commit), self.assertRaises(ValueError):
                    self.make(binary, notices, output, commit=commit)
            for target in ("windows-arm64", "linux-arm64", "windows-amd64"):
                with self.subTest(target=target), self.assertRaises(ValueError):
                    self.make(binary, notices, output, target=target)
            binary.write_bytes(b"invalid executable")
            with self.assertRaises(ValueError):
                self.make(binary, notices, output)
            self.assertEqual(list(output.iterdir()), [])

    def test_existing_archive_or_checksum_is_never_overwritten(self):
        for suffix in (".tar.gz", ".tar.gz.sha256"):
            with self.subTest(suffix=suffix), tempfile.TemporaryDirectory() as folder:
                binary, notices = self.fixture(folder)
                output = Path(folder) / "output"
                output.mkdir()
                existing = output / ("uptime-canary-v0.1.0-rc.1-linux-amd64" + suffix)
                existing.write_bytes(b"User-owned existing output")
                with self.assertRaises(FileExistsError):
                    self.make(binary, notices, output)
                self.assertEqual(existing.read_bytes(), b"User-owned existing output")
                self.assertEqual(list(output.iterdir()), [existing])

    def test_input_change_encoding_error_and_publication_race_clean_only_owned_files(self):
        for failure in ("input", "encoding", "publication"):
            with self.subTest(failure=failure), tempfile.TemporaryDirectory() as folder:
                binary, notices = self.fixture(folder)
                output = Path(folder) / "output"
                original_writer, original_link = release.write_archive, release.os.link

                def writer(*args):
                    if failure == "encoding":
                        raise OSError("injected encoder failure")
                    if failure == "input":
                        raw = binary.read_bytes()
                        binary.write_bytes(raw[:-1] + b"x")
                    return original_writer(*args)

                def link(source, destination):
                    if str(destination).endswith(".sha256"):
                        Path(destination).write_bytes(b"Concurrent user output")
                        raise FileExistsError("injected publication race")
                    return original_link(source, destination)

                with patch.object(release, "write_archive", side_effect=writer):
                    with patch.object(release.os, "link", side_effect=link if failure == "publication" else original_link):
                        with self.assertRaises((ValueError, OSError)):
                            self.make(binary, notices, output)
                files = list(output.iterdir())
                if failure == "publication":
                    self.assertEqual(len(files), 1)
                    self.assertTrue(files[0].name.endswith(".sha256"))
                    self.assertEqual(files[0].read_bytes(), b"Concurrent user output")
                else:
                    self.assertEqual(files, [])

    def test_empty_license_or_notice_refuses_packaging(self):
        with tempfile.TemporaryDirectory() as folder:
            binary, notices = self.fixture(folder)
            (notices / "THIRD_PARTY_NOTICES.txt").write_bytes(b"")
            with self.assertRaises(ValueError):
                self.make(binary, notices, Path(folder) / "output")
            self.assertFalse((Path(folder) / "output").exists())


if __name__ == "__main__":
    unittest.main()
