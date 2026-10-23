# Node release packaging and validation

`scripts/package-release.py` packages an **already built** node executable. It
does not build, run, sign or publish it, create a Git tag, or certify a release.
This is follow-up release tooling; the monitored public-history PR remains
unchanged on its own branch.

Initial packaging targets:

| Target | Required input header | Output |
| --- | --- | --- |
| `linux-amd64` | Little-endian 64-bit ELF, machine x86-64 | `.tar.gz` |
| `linux-arm64` | Little-endian 64-bit ELF, machine AArch64 | `.tar.gz` |
| `windows-amd64` | PE32+ x86-64 | `.zip` |

Header checks catch a common wrong-target input; they are not a loader, Go
provenance check or native-platform runtime test. A binary must be a regular
non-symlink file. The output filesystem must support exclusive hard links
(the tested Windows filesystem is NTFS; Linux CI uses its own filesystem).

## Contents and identity

Each archive has one directory named
`uptime-canary-v<VERSION>-<TARGET>`, containing exactly:

- `canary` or `canary.exe`, with executable archive permissions;
- the repository's `LICENSE` and `THIRD_PARTY_NOTICES.txt`, byte for byte;
- `release.json`, recording version, supplied source commit, target, and SHA-256
  plus byte count of the executable and both notice files.

A separate `<archive-name>.sha256` contains the archive's SHA-256 in standard
checksum-file format. There is no binary or notice content in console output.
No environment file, database, backup, identity directory, dashboard profile,
token, build cache or other directory is read or copied into the archive.

The version accepts SemVer with an optional leading `v`, with at most 64
characters after that prefix. Invalid identifiers, path separators, invalid
numeric prerelease identifiers and unsupported target combinations are refused.
The commit must be an explicit lowercase 40-character Git object ID. It is
caller-supplied metadata; the packaging function does not prove that the binary
was compiled from that commit. The trusted build/release workflow must bind and
verify those inputs before publication.

Archive ordering, times, owner names/IDs and permissions are fixed. Identical
input bytes, version, commit, target and compression implementation produce
identical archives, independent of input modification time or output path.
Binary hashing/copying streams bounded chunks. Source mutation detected while
packaging refuses publication. Compression output is staged before exclusive
publication, and existing archive/checksum names are never overwritten.
An error cleans only the helper's known temporary files and published links
still pointing to its own staged files. A concurrent foreign output is retained.

## Invocation

All paths and labels are explicit; no default running node binary is selected:

```text
python scripts/package-release.py --binary <built-node> --output <release-output-directory> --version <semver> --commit <exact-reviewed-commit> --target linux-amd64
```

Run agent builds/tests through `scripts/bounded_validation.py`. For example:

```text
python scripts/bounded_validation.py --timeout 60 -- python -B scripts/package_release_test.py
```

Those tests use only tiny synthetic executable headers and fixture license
texts under the guard's private temporary directory. They never execute those
files or package a user's binary, and retain no archives. Normal agent
validation outputs are deleted by the guard. Intended published release assets
need an explicit release workflow rather than keeping validation copies.

## Evidence and remaining work

On native Windows, all **seven** fixture test functions pass, including both
Linux header variants and Windows ZIP metadata. The guarded run took **4.266
seconds**, source unchanged at fingerprint
`e920181f166cb59124ed7205f54ecf0940d6f632d48fc7fd7a72957a427060e8`.
Sampled scratch peaked at 459 bytes (sampling does not capture every instant)
and returned to zero. No Go build, module-cache growth, actual executable,
release image, source snapshot or retained binary collection was produced.
CI now runs the same fixture checks; that Linux result is pending.

## Actual build and archive validation

`scripts/release-package-validation.py` is a separate guarded acceptance runner.
It requires a clean Git checkout at the exact supplied commit, including no
nonignored untracked files. It does not publish or retain a release artifact.

```text
python scripts/bounded_validation.py --timeout 480 -- python -B scripts/release-package-validation.py --commit <exact-HEAD> --version 0.0.0-dev --target windows-amd64
```

The runner uses the existing pinned toolchain and shared cache, with readonly
modules, CGO disabled, no Go workspace and module-network access disabled. It
checks embedded-runtime provenance and module integrity. The selected target's
linked external module versions and the standard-library version must all have
entries in the notice inventory, so a Linux-only inventory cannot silently omit
a Windows-only module. This is a coverage check, not legal advice or a complete
license audit.

The real build must embed the requested Git revision, `vcs.modified=false`,
the expected main module/package, toolchain, OS, architecture and static build
settings. A second build must have identical bytes. The runner verifies archive
checksums, metadata and license contents, extracts only its known executable to
a private path, and checks that executable's build identity and checksum again.
When the target matches the host, it runs CLI help and the existing two-node
public-history process proof against that extracted executable. Cross-target
checks explicitly omit execution. One native proof is not the full process
suite or application acceptance gate.

All binaries, private databases and archives remain in the guard's outputs and
are deleted afterward. A single overwritten `.canary/release-package-current.json`
retains compact source, commands, settings, checksums and proof results. It is
development evidence, not a retained distributable or release approval.

Three additional provenance fixture tests pass on Windows (4.172 seconds
guarded), covering wrong/missing/duplicate build identity, target notice gaps and
dirty or mismatched checkouts. They use tiny private Git fixtures and fixed
metadata. Actual executable validation is still pending; the first attempted
run is recorded below.

The first actual Windows attempt on `187f486` stopped before compilation:
target dependency checks found `github.com/mattn/go-isatty` and
`github.com/ncruces/go-strftime` absent from the Linux-only notice inventory.
The guarded failure took 23.344 seconds, left zero scratch and changed no source
or shared modules. The notice generator now discovers the union of Linux amd64,
Linux arm64 and Windows amd64 dependencies. This failure is retained as evidence
that platform notice omissions block packaging; it is not an executable test.
Regeneration produced 48 notice sets. The generator exited zero, while the guard
correctly reported changed source because the two inventory files were updated;
that authoring command is not claimed as a passing immutable validation. The
subsequent readonly `--check` passed in 4.485 seconds, with source unchanged,
zero scratch and zero shared-module growth.

The next actual attempt on `874fac4` verified all 42 Windows linked module
notice sets and built successfully, then failed the new validator's assumption
that the main module must say `(devel)`. Its real Go 1.27.3 metadata correctly
contained `v0.0.0-20261023211050-874fac4fa436`, the full matching Git revision and
`vcs.modified=false`. The validator now accepts Go-derived versions while still
requiring the exact full revision, clean flag and target settings; new positive
fixtures cover pseudoversions and tags. This was a validator defect, not an
application test failure. That guarded run took 22.594 seconds and removed all
172,952,074 sampled scratch bytes; module growth was zero.

Before calling a release ready, the producer still needs to build and validate
real Windows/Linux artifacts, verify target-specific dependency notices and
source provenance, execute the supported native/container gates including
arm64, aggregate published checksums and publish reviewed binaries/images.
The existing notice inventory primarily describes the verified Linux graph;
packaging its bytes is not an audit of a different platform's dependency set.
Installation/upgrade guidance and reproducible tagged-release verification
remain part of the broader [shipping work](shipping-progress.md). No tagged
release, publication or full near-Kuma completion is claimed by this helper.
