"""Provenance refusals use fixed metadata and a tiny private Git fixture."""
import importlib.util
from pathlib import Path
import subprocess
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("release_validation", Path(__file__).with_name("release-package-validation.py"))
validation = importlib.util.module_from_spec(spec)
spec.loader.exec_module(validation)
COMMIT = "a" * 40
INFO = ("fixture: go1.27.3\n\tpath\tuptime-canary/apps/node-go/cmd/canary\n\tmod\tuptime-canary\t(devel)\n"
        "\tbuild\t-buildmode=exe\n\tbuild\t-trimpath=true\n\tbuild\tCGO_ENABLED=0\n"
        "\tbuild\tGOARCH=amd64\n\tbuild\tGOOS=windows\n\tbuild\tvcs=git\n"
        "\tbuild\tvcs.revision=" + COMMIT + "\n\tbuild\tvcs.modified=false\n")


class ReleaseValidationTests(unittest.TestCase):
    def test_metadata_rejects_wrong_or_missing_source_target_policy_and_duplicate_fields(self):
        checked = validation.verify_build_info(INFO, "windows-amd64", COMMIT, "go1.27.3")
        self.assertEqual(checked["vcs.revision"], COMMIT)
        for version in ("v0.0.0-20261023211050-" + COMMIT[:12], "v1.2.3"):
            raw = INFO.replace("(devel)", version)
            self.assertEqual(validation.verify_build_info(raw, "windows-amd64", COMMIT, "go1.27.3")["mainModuleVersion"], version)
        cases = [INFO.replace("go1.27.3", "go1.26.0"), INFO.replace(COMMIT, "b" * 40),
                 INFO.replace("vcs.modified=false", "vcs.modified=true"), INFO.replace("GOOS=windows", "GOOS=linux"),
                 INFO.replace("GOARCH=amd64", "GOARCH=arm64"), INFO.replace("CGO_ENABLED=0", "CGO_ENABLED=1"),
                 INFO.replace("-trimpath=true", "-trimpath=false"), INFO.replace("canary/apps", "other/apps"),
                 INFO.replace("\tbuild\tvcs.revision=" + COMMIT + "\n", ""),
                 INFO + "\tbuild\tvcs.modified=false\n", INFO + "\tpath\tother\n", INFO + "\tbuild\tbroken\n",
                 INFO.replace("(devel)", "not-a-version")]
        for raw in cases:
            with self.subTest(raw=raw), self.assertRaises(ValueError):
                validation.verify_build_info(raw, "windows-amd64", COMMIT, "go1.27.3")

    def test_platform_notices_cannot_silently_reuse_an_incomplete_or_wrong_version_inventory(self):
        manifest = {"packages": [{"name": "example.org/module", "version": "v1.2.3"},
                                 {"name": "Go standard library", "version": "go1.27.3"}]}
        self.assertEqual(validation.verify_notice_coverage("example.org/module v1.2.3\n", manifest, "go1.27.3"), 1)
        for raw, toolchain in (("example.org/other v1.2.3", "go1.27.3"), ("example.org/module v2.0.0", "go1.27.3"),
                               ("example.org/module v1.2.3", "go1.27.4"), ("malformed", "go1.27.3")):
            with self.subTest(raw=raw, toolchain=toolchain), self.assertRaises(ValueError):
                validation.verify_notice_coverage(raw, manifest, toolchain)

    def test_clean_commit_binding_rejects_dirty_tracked_untracked_and_wrong_head(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            def git(*args):
                return subprocess.check_output(["git", *args], cwd=root, stderr=subprocess.STDOUT, text=True).strip()
            git("init", "--quiet")
            source = root / "fixture.txt"
            source.write_text("original fixture\n")
            git("add", "fixture.txt")
            git("-c", "user.name=Release fixture", "-c", "user.email=release@example.invalid",
                "-c", "commit.gpgsign=false", "commit", "--quiet", "-m", "Private tiny fixture")
            commit = git("rev-parse", "HEAD")
            validation.clean_checkout(commit, root)
            with self.assertRaisesRegex(ValueError, "does not match"):
                validation.clean_checkout(COMMIT, root)
            source.write_text("changed\n")
            with self.assertRaisesRegex(ValueError, "clean checkout"):
                validation.clean_checkout(commit, root)
            source.write_text("original fixture\n")
            (root / "untracked.go").write_text("package fixture\n")
            with self.assertRaisesRegex(ValueError, "clean checkout"):
                validation.clean_checkout(commit, root)


if __name__ == "__main__":
    unittest.main()
