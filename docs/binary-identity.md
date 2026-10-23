# Installed binary identity

Run the executable directly:

```text
canary version
canary --version
```

Both commands print the same local JSON identity and exit. They require no
account token, environment configuration, database, HTTP endpoint or running
node. An extra argument is rejected before any environment or transport lookup.
They do not report another node's version. The existing management commands and
monitoring, delivery, database and peer-wire behavior are unchanged.

The fields are:

| Field | Meaning |
| --- | --- |
| `name` | `Uptime-Canary` |
| `version` | Explicit build-time release label, otherwise Go's module version when available, otherwise `unknown` |
| `moduleVersion` | Go's actual module version, kept separate from the release label; empty when unavailable |
| `goVersion` | Embedded Go toolchain version |
| `os`, `arch` | Executable target, rather than the queried server or current database |
| `sourceCommit` | Full embedded Git revision, omitted if the complete provenance record is unavailable or malformed |
| `modified` | Whether that source had uncommitted changes; `null` when unknown, never silently `false` |
| `peerProtocol` | This executable's peer protocol; adding the identity command does not bump it |

Git metadata is accepted only with one consistent Git/revision/modified tuple.
Duplicate fields, malformed revisions, missing fields or an unsupported VCS do
not produce an authoritative source commit or clean-state assertion. A release
label does not override dirty or unknown source provenance. This metadata is
useful for support and compatibility checks; it is not a cryptographic signature
or evidence that a binary came from a trusted distributor.

The guarded release-package validator stamps the already-declared SemVer label
through `-X uptime-canary/apps/node-go/internal/canary.releaseVersion=<version>`.
It records the exact build command and, on native targets, verifies the
extracted executable's JSON label, full clean Git revision,
target, toolchain and peer protocol against the archive's intended inputs.
Go omits linker settings from binary metadata under `-trimpath`, so that
metadata alone does not verify the release label. Cross-built artifacts report
`releaseLabelVerifiedByExecution: false`; the native CI gate requires execution
on every supported target. The archive still retains its own license files and
checksummed `release.json`.
No tag, published binary or independent-machine reproducibility is implied.

Ordinary source builds and the existing development Dockerfile need not carry
a release label or Git checkout metadata. They report the available information
and keep unknown provenance explicit. A future published-image workflow must
bind its version/source inputs and validate the final image; a blank stamp must
not be relabelled as a verified release.

This is a follow-up to [release artifact validation](release-artifacts.md).
Earlier artifact hashes and protocol-15 CI evidence cover their recorded source,
before this command existed. Native Windows evidence for the new source:

- Three focused `TestBinaryIdentity*` functions pass (package 1.460 s; guard
  108.907 s including compilation). Nil/trapping environment and HTTP adapters,
  extra-argument refusal, clean/dirty labels and unknown/malformed provenance
  are covered. The first invocation failed before Go started because Windows
  did not resolve a relative tool path; the successful run used the existing
  toolchain's absolute path. No assertion was changed.
- Canary package vet passes without diagnostics (64.563 s guarded).
- Twelve archive/provenance fixture functions pass (4.219 s guarded), including
  label mismatches and runtime identity with unknown/dirty/mismatched source,
  target, toolchain or peer protocol.

These checks used source fingerprint
`561583e1116debeaf733dce463b3910205b692097d6bb9b61f694fc4c817757d`,
unchanged during each run. The largest sampled scratch was 517,077,350 bytes,
below 512 MiB, and returned to zero. Shared modules did not grow. Actual compiled
archive execution and current-source CI remain pending; no release approval is
claimed.

The first compiled-archive attempt on `d3de153` failed the new validator before
archive execution: it incorrectly required `-ldflags` in Go build metadata.
The actual SDK intentionally excludes that field under `-trimpath`
(`cmd/go/internal/load/pkg.go:2470-2480`, referring to Go issue 52372). The
source, target and clean Git checks passed; label execution had not yet run.
The new synthetic flag-text assumption is corrected and preserved in the
fixture comment; all runtime label/source/target rejection cases remain.
That failed run took 87.672 seconds, left source unchanged, removed its
470,209,710 sampled scratch bytes and grew no shared modules. This is a
validator failure, not a demonstrated application version mismatch.
