#!/usr/bin/env python3
"""Package an already-built node binary; this does not certify or publish a release."""
import argparse
import gzip
import hashlib
import io
import json
import os
from pathlib import Path
import re
import shutil
import stat
import struct
import tarfile
import tempfile
import zipfile

ROOT = Path(__file__).resolve().parents[1]
TARGETS = {"linux-amd64": 62, "linux-arm64": 183, "windows-amd64": 0x8664}
VERSION = re.compile(r"(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?")


def version_value(value):
    value = value.removeprefix("v")
    match = VERSION.fullmatch(value) if len(value) <= 64 else None
    if not match or (match[1] and any(x.isdigit() and len(x) > 1 and x[0] == "0" for x in match[1].split("."))):
        raise ValueError("A valid SemVer version of at most 64 characters is required")
    return value


def validate_header(prefix, target):
    if target.startswith("linux-"):
        valid = (len(prefix) >= 64 and prefix[:7] == b"\x7fELF\x02\x01\x01"
                 and prefix[7] in (0, 3) and struct.unpack_from("<H", prefix, 16)[0] in (2, 3)
                 and struct.unpack_from("<H", prefix, 18)[0] == TARGETS[target])
    else:
        offset = struct.unpack_from("<I", prefix, 60)[0] if len(prefix) >= 64 else -1
        valid = (prefix[:2] == b"MZ" and 64 <= offset <= len(prefix) - 26
                 and prefix[offset:offset + 4] == b"PE\x00\x00"
                 and struct.unpack_from("<H", prefix, offset + 4)[0] == TARGETS[target]
                 and struct.unpack_from("<H", prefix, offset + 24)[0] == 0x20B)
    if not valid:
        raise ValueError("Binary format or machine does not match the requested target")


class HashedReader:
    def __init__(self, source):
        self.source, self.digest, self.count = source, hashlib.sha256(), 0

    def read(self, size=-1):
        data = self.source.read(size)
        self.digest.update(data)
        self.count += len(data)
        return data


def digest_file(path):
    with path.open("rb") as source:
        reader = HashedReader(source)
        while reader.read(1048576):
            pass
        return reader.digest.hexdigest(), reader.count


def write_archive(file, target, folder, entries):
    """Fixed metadata and streaming input make output independent of paths/time."""
    if target.startswith("windows-"):
        with zipfile.ZipFile(file, "w", compression=zipfile.ZIP_DEFLATED, compresslevel=6) as archive:
            for name, mode, size, source in entries:
                info = zipfile.ZipInfo(folder + "/" + name, (1980, 1, 1, 0, 0, 0))
                info.create_system = 3
                info.external_attr = (stat.S_IFREG | mode) << 16
                info.compress_type, info.file_size = zipfile.ZIP_DEFLATED, size
                with archive.open(info, "w") as destination:
                    shutil.copyfileobj(source, destination, 1048576)
    else:
        with gzip.GzipFile(filename="", mode="wb", fileobj=file, mtime=0, compresslevel=6) as compressed:
            with tarfile.open(fileobj=compressed, mode="w", format=tarfile.USTAR_FORMAT) as archive:
                for name, mode, size, source in entries:
                    info = tarfile.TarInfo(folder + "/" + name)
                    info.mode, info.size, info.mtime = mode, size, 0
                    info.uid = info.gid = 0
                    info.uname = info.gname = ""
                    archive.addfile(info, source)


def package(binary, output, version, commit, target, *, notice_root=ROOT):
    version = version_value(version)
    if target not in TARGETS or not re.fullmatch(r"[0-9a-f]{40}", commit):
        raise ValueError("A supported target and exact lowercase Git commit are required")
    binary, output, notice_root = Path(binary), Path(output), Path(notice_root)
    if binary.is_symlink() or not stat.S_ISREG(binary.lstat().st_mode):
        raise ValueError("Binary input must be a regular non-symlink file")
    if output.is_symlink():
        raise ValueError("Output directory must not be a symlink")
    notices = {name: (notice_root / name).read_bytes() for name in ("LICENSE", "THIRD_PARTY_NOTICES.txt")}
    if not all(notices.values()):
        raise ValueError("License and third-party notices must be nonempty")
    output.mkdir(parents=True, exist_ok=True, mode=0o700)
    output = output.resolve()
    folder = "uptime-canary-v" + version + "-" + target
    archive_path = output / (folder + (".zip" if target.startswith("windows-") else ".tar.gz"))
    checksum_path = output / (archive_path.name + ".sha256")
    if any(os.path.lexists(p) for p in (archive_path, checksum_path)):
        raise FileExistsError("Release output already exists; nothing will be overwritten")
    temporary, published = [], []

    def temp_file():
        fd, name = tempfile.mkstemp(prefix=".package-", dir=output)
        path = Path(name)
        assert path.parent.resolve() == output
        temporary.append(path)
        return path, os.fdopen(fd, "w+b")

    try:
        with binary.open("rb") as source:
            initial = os.fstat(source.fileno())
            prefix = source.read(65536)
            validate_header(prefix, target)
            first = HashedReader(source)
            first.digest.update(prefix)
            first.count = len(prefix)
            while first.read(1048576):
                pass
            size, digest = first.count, first.digest.hexdigest()
            source.seek(0)
            reader = HashedReader(source)
            executable = "canary.exe" if target.startswith("windows-") else "canary"
            metadata = {"schemaVersion": 1, "name": "Uptime-Canary", "version": version,
                "sourceCommit": commit, "target": target,
                "files": {executable: {"sha256": digest, "bytes": size}, **{
                    name: {"sha256": hashlib.sha256(raw).hexdigest(), "bytes": len(raw)} for name, raw in notices.items()}}}
            raw_manifest = (json.dumps(metadata, indent=2, sort_keys=True) + "\n").encode()
            entries = [(executable, 0o755, size, reader)] + [
                (name, 0o644, len(raw), io.BytesIO(raw)) for name, raw in notices.items()]
            entries.append(("release.json", 0o644, len(raw_manifest), io.BytesIO(raw_manifest)))
            staged_archive, handle = temp_file()
            with handle:
                write_archive(handle, target, folder, entries)
                handle.flush()
                os.fsync(handle.fileno())
            final = os.fstat(source.fileno())
            if (reader.count != size or reader.digest.hexdigest() != digest or final.st_size != size
                    or final.st_mtime_ns != initial.st_mtime_ns or final.st_ctime_ns != initial.st_ctime_ns):
                raise ValueError("Binary changed while it was being packaged")
        archive_digest, archive_size = digest_file(staged_archive)
        staged_checksum, handle = temp_file()
        with handle:
            handle.write((archive_digest + "  " + archive_path.name + "\n").encode("ascii"))
            handle.flush()
            os.fsync(handle.fileno())
        # Exclusive hard links cannot replace an existing output, even in a race.
        # The caller's output filesystem must support them (NTFS/ext4 in CI).
        for staged, destination in ((staged_archive, archive_path), (staged_checksum, checksum_path)):
            os.link(staged, destination)
            published.append((destination, staged))
        return {"archive": str(archive_path), "checksum": str(checksum_path),
            "sha256": archive_digest, "bytes": archive_size, "binarySha256": digest}
    except Exception:
        for destination, staged in reversed(published):
            if destination.exists() and os.path.samefile(destination, staged):
                destination.unlink()
        raise
    finally:
        for path in temporary:
            path.unlink(missing_ok=True)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--commit", required=True)
    parser.add_argument("--target", choices=TARGETS, required=True)
    args = parser.parse_args()
    try:
        print(json.dumps(package(args.binary, args.output, args.version, args.commit, args.target), indent=2))
    except (OSError, ValueError, tarfile.TarError, zipfile.BadZipFile) as error:
        parser.exit(1, "Release packaging failed: " + str(error) + "\n")
