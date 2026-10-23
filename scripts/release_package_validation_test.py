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
    def test_declared_release_label_and_runtime_identity_must_match_the_archive(self):
        # Original new-fixture assumption: require -ldflags in build metadata,
        # then reject a different release label through verify_build_info.
        # A real d3de153 build exposed that Go 1.27.3 intentionally omits
        # -ldflags under -trimpath (cmd/go/internal/load/pkg.go:2470-2480).
        # Source/target checks still apply; the native JSON check below must
        # verify the actual label, rather than trusting synthetic flag text.
        validation.verify_build_info(INFO, "windows-amd64", COMMIT, "go1.27.3")
        identity = {"name": "Uptime-Canary", "version": "0.1.0-rc.1", "sourceCommit": COMMIT, "modified": False,
                    "goVersion": "go1.27.3", "os": "windows", "arch": "amd64", "peerProtocol": 15}
        self.assertEqual(validation.verify_runtime_identity(validation.json.dumps(identity), "windows-amd64", "0.1.0-rc.1", COMMIT, "go1.27.3", 15), identity)
        for key, invalid in (("version", "0.1.0-rc.2"), ("sourceCommit", "b" * 40), ("modified", None), ("modified", 0),
                             ("modified", True), ("os", "linux"), ("arch", "arm64"), ("peerProtocol", 14), ("goVersion", "go1.26.0")):
            with self.subTest(key=key, invalid=invalid), self.assertRaises(ValueError):
                validation.verify_runtime_identity(validation.json.dumps({**identity, key: invalid}), "windows-amd64", "0.1.0-rc.1", COMMIT, "go1.27.3", 15)
    def test_native_execution_cannot_be_claimed_by_a_cross_build_or_unknown_machine(self):
        self.assertEqual(validation.native_target("Linux", "aarch64"), "linux-arm64")
        self.assertEqual(validation.native_target("Windows", "AMD64"), "windows-amd64")
        self.assertTrue(validation.execution_policy("linux-arm64", "linux-arm64", True, True, True))
        self.assertFalse(validation.execution_policy("linux-arm64", "windows-amd64", False, False, False))
        for flags in ((True, False, False), (False, True, False), (False, False, True)):
            with self.subTest(flags=flags), self.assertRaisesRegex(ValueError, "host target"):
                validation.execution_policy("linux-arm64", "windows-amd64", *flags)
        with self.assertRaisesRegex(ValueError, "host target"):
            validation.execution_policy("linux-arm64", validation.native_target("Linux", "unexpected"), True, False, False)
        with self.assertRaisesRegex(ValueError, "native Linux"):
            validation.execution_policy("windows-amd64", "windows-amd64", True, False, True)
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
