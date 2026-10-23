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
metadata. After the version-parser correction below, the same three test
functions passed again in 4.078 seconds, with source unchanged and zero scratch.

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

### Completed local artifact checks

All three targets pass on the exact clean checkout
`93e5dc23a90c66c2098941da05047d4a387f2ef7`, source fingerprint
`a1475ab7ad2efabcb9dceb697ee448db367d37678c095090d31717f8deb1971b`,
using Go 1.27.3 and the development label `0.0.0-dev`:

| Target | Guarded seconds | Archive bytes | Linked external notice sets | Execution |
| --- | ---: | ---: | ---: | --- |
| Windows amd64 | 73.969 | 17,253,356 | 42 | Extracted CLI help and two-node history/restart/withdrawal proof, 1/1, zero skips |
| Linux amd64 | 40.484 | 17,145,073 | 40 | Cross-built on Windows; not executed |
| Linux arm64 | 36.297 | 15,773,018 | 40 | Cross-built on Windows; not executed |

Each target's two builds had identical executable bytes. All archive and
extracted-file hashes, license bytes, clean Git revisions and target settings
passed. These are repeated builds on one machine/toolchain, not independent
multi-machine reproducibility certification or native Linux acceptance.

| Target | Executable SHA-256 | Archive SHA-256 |
| --- | --- | --- |
| Windows amd64 | `a51ffb2d614ef3001221087500310e698be77f0d3cceb58aa68ca92903b117f4` | `726bc54035f1e6c090a749abbb86ec0ac4d202433585e7a9a40f659f4e938e69` |
| Linux amd64 | `23b7e618729d55e1b54a18f3f7cde386ba3e15a3a7c3ec27e3ab31827b8d8efa` | `54cae901e163c9d142c650f590c33d36f8b2df11c2fd4d6c1e97b82edb52b3b6` |
| Linux arm64 | `d4d774355210102fe888420cc86e6c03832c5029e1847a4e1bdf163c24bb7ee9` | `7e99a6b9bb604e63fe313800f33a31fc1177bccf494afd18d05dc6b5cdce03ba` |

No binaries or archives were retained. Sampled scratch peaks were 169,484,591,
434,174,587 and 397,777,798 bytes respectively, below the 512 MiB ceiling; every
run returned scratch to zero. The largest sampled repository footprint was
2,170,576,341 bytes, below 3 GiB. Final idle usage was 1,259,504,104 bytes with
the shared owned cache below 256 MiB and zero module-cache growth. No VM,
source snapshot, private cache, external notification or remote measurement
was created. The failures above remain part of the record.

### Native CI gate

The new `.github/workflows/release-package-validation.yml` runs serial native
jobs on Windows amd64, Ubuntu amd64 and Ubuntu arm64. The runner labels are
listed in [GitHub's hosted-runner documentation](https://docs.github.com/en/actions/reference/runners/github-hosted-runners).
The workflow checks out the actual PR head commit rather than a synthetic merge
commit, with read-only permissions and no persisted Git credential. Go 1.27.3,
Node 24.13.0 and all actions are pinned; action dependency caches are disabled.
It cannot tag, sign or publish a release.

Each fresh disposable hosted runner installs the locked Go modules and the
existing Node proof dependencies before the guard establishes its baseline.
That initial remote dependency installation is not claimed as zero module-cache
growth. No dependency or SDK is installed locally by authoring this workflow.
The installed Go SDK is linked into the expected tools path instead of copied;
one owned build cache is reused, with no cache archives or snapshots retained.
Every subsequent build/test runs through the existing guard, preserving its
3 GiB repository, 512 MiB scratch and post-baseline module-growth checks. The
validator explicitly preserves serial Go compilation. The guard also correctly
recognizes its already-selected owned cache, rather than classifying that same
directory as unexpected external growth.

The guard fixtures and archive/provenance fixtures run first. Native arm64 also
runs every node package, filling a previously missing execution gate. Each
target must then pass `--require-native --full-proofs`: two matching builds,
verified/extracted archive identity, CLI help and **every** ordinary process
proof. A mismatched or unknown host architecture is rejected before building;
a successful cross-build can never satisfy this native gate.

On both Linux architectures the extracted binary additionally runs CLI help in
a pre-pulled native Alpine container with no network, read-only root, dropped
capabilities, no-new-privileges and the nonroot host UID. The container image
digest and actual architecture are recorded. This is a CLI portability check,
not an assertion that the entire process suite ran inside Alpine. The existing
broader Alpine application/race workflow remains a separate gate.

Only the overwritten compact report and guard log/result are uploaded, with
seven-day retention; binaries, archives, fixture databases and outputs are
removed. Hosted runner dependencies disappear with the job, and no local VM or
build collection is introduced. Current local source checks pass: nine guard
test functions (4.188 s guarded) and eleven archive/provenance test functions
(4.234 s guarded), including native/cross-target classification. Scratch returns
to zero and shared modules do not grow. These checks do **not** prove the new
workflow or native arm64 runtime passes; those results are pending execution.

### First native CI result and repairs

[Workflow 40027197572](https://github.com/Narehood/Uptime-Canary/actions/runs/40027197572)
on `bcbd65b` completed with one passing target and two failures. Its small
artifacts were inspected in memory; no archive or source copy was retained:

- **Native Linux amd64 passes:** two matching builds, extracted executable,
  all 18/18 process proofs, zero skips, and offline/nonroot/read-only native
  Alpine CLI help. Source is unchanged. The guarded run took 526.743 seconds,
  with 434,077,520 sampled scratch bytes returned to zero. Archive SHA-256 is
  `c928cd6605f5c2c034ab06994340c0eb5887957d0b49d7fc247a1c62c0890578`,
  executable SHA-256 is
  `23b7dad8f546a707fedd2ab2b6774eb82beb9418b34b714677bf554188ae74c2`.
  Native Alpine image digest was
  `alpine@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a`.
- **Windows fails before compilation:** the exact runtime-source verifier
  rejected `jsonata.js`, whose checkout bytes had been converted to CRLF. The
  run took 2.688 seconds; no executable or application assertion ran. A tiny
  checkout fixture independently reproduces LF becoming CRLF under
  `core.autocrlf=true` (4.313 seconds guarded). New `.gitattributes` preserves
  every tracked Git blob's bytes, including existing CRLF fixtures; it does not
  normalize source files or weaken hash verification. The unchanged byte
  assertions now pass, with all twelve archive/provenance fixture functions
  passing in 4.219 seconds guarded.
- **Arm64 is stopped during compilation:** the guard detected 537,148,698 bytes
  of scratch, above 512 MiB, and killed the command after 134.205 seconds. The
  availability package passed, but canary tests and subsequent packages did not
  run. Scratch returned to zero and idle usage to 361,891,727 bytes. This is not
  a numerical/runtime assertion failure or an arm64 pass. The full package test
  command now strips test-executable debug symbols (`-ldflags='-s -w'`) to reduce
  linker/output storage while retaining every test, assertion, timeout and
  worker limit. The 512 MiB cap is unchanged; actual arm64 completion remains
  pending a new run.

The v3 guard fingerprint also covers `.gitattributes`; its new detection case
fails before the change and passes afterward. All nine guard test functions
pass in 4.313 seconds guarded, with zero scratch and no module growth. Earlier
v2 fingerprints retain their original scope. The Alpine source manifest now
includes the checkout policy itself. None of these local setup controls clears
the failed Windows/arm64 gate without executing the corrected workflow.

A local cross-compilation of the stripped arm64 canary test binary subsequently
passes in 118.125 seconds guarded. Its 54,591,648-byte output is deleted;
504,709,110 sampled scratch bytes remain below 512 MiB and return to zero,
with no module growth. Source is unchanged at v3 fingerprint
`e75e3bd50bf9e2acf88f8b71f79489d34bc5cdf9050ff214445ff452072c2a68`.
This verifies that core test compilation fits on this Windows host; it does not
execute an arm64 test or prove the complete native package suite fits.

Before calling a release ready, the producer still needs to build and validate
real Windows/Linux artifacts, verify target-specific dependency notices and
source provenance, execute the supported native/container gates including
arm64, aggregate published checksums and publish reviewed binaries/images.
The notice inventory now covers the three declared CGO-disabled target graphs;
its module coverage checks do not certify a different platform or build mode.
Installation/upgrade guidance and reproducible tagged-release verification
remain part of the broader [shipping work](shipping-progress.md). No tagged
release, publication or full near-Kuma completion is claimed by this helper.
