# Protocol 9 integration checkpoint

This records identifiable test inputs, not a release approval or a near-parity
claim. The comparison still includes the missing monitor families and options in
[parity-audit.md](parity-audit.md). SMS remains deferred by the user.

## Immutable inputs

- Earlier capture: `14cf56c70c3252618ef3d1c7b522856a50b215371afeafdadfd3c843b5de3627`.
- Repaired capture: `cde59ef5e10cedecbed9b022d55535305a0b831c4b9b95208299810429e7db35`.

Each source lives under `.canary/alpine-validation/windows-race/snapshots/<hash>/source`
with a sibling JSON manifest and archive. The repaired capture differs in exactly
two files: `canary/push.go` and the independent `push_final_review_test.go`.
The production change preserves admission across temporarily disabled, unready,
or conflicting monitor configurations. The reviewer corrected an index fixture
that had expected a unique binding while an unproven child and its retained
parent correctly formed an ambiguous binding. No concrete index defect remained.

## Earlier capture results

| Check | Result and scope |
| --- | --- |
| Real-process proofs | 13/13 passed in 88.840 s; zero failures/skips; binary and proof sources unchanged. Sender death, partitions, provider retries, groups, HTTPS/ICMP, published metadata and passive heartbeats. |
| Full ordinary Go suite | Failed: four passive-admission subcases and the obsolete index assertion. Other packages passed; this is not relabeled a full pass. |
| Native leaf race suites | Seven packages passed, 607 passing events and zero race warnings. One Windows symlink case skipped for unavailable privilege. Actual Linux permission/alias controls have separate earlier evidence. |
| Worker-controller race harness | 397 passing events; zero failures, skips or race warnings. Parents instrumented; separately hashed children are uninstrumented and retain production memory limits. This does not certify child/driver internals as race-free. |
| Linux amd64 symbol security scan | No reachable Go vulnerability; npm reported zero advisories. The module-only OpenPGP advisory GO-2026-5932 remains in the report and is not called an advisory-free dependency graph. |
| Full native core race | Completed in 811.498 s with zero race warnings and 655 passing events, but failed the known passive-admission cases and two fixture assertions. This remains a failed suite, not a race pass. |

The proof executable SHA-256 is
`a6f381a4dac7a86b5b1f918ef68331819f9cf2aa9f3ab2ef11aa2f6509238b84`.
Evidence is retained in `.canary/proof-checkpoints/protocol9-14cf`,
`.canary/worker-race/20261022T181603757312Z`, and
`.canary/security/20261022T181713943185Z`; native Go logs are in the corresponding
`windows-race/runs/*protocol9-repaired-*` directories.

## Repaired capture

Current-source review validation passes all 32 push functions, the heartbeat
suite and vet. The frozen repaired source builds with CGO disabled, readonly
modules and trimpath on Windows amd64, Linux amd64 and Linux arm64:

| Target | SHA-256 |
| --- | --- |
| Windows amd64 | `d5f1840268269a64be95f257551d32f1c6410dc5fb68ba7b39846fa2aa7889ba` |
| Linux amd64 | `de4962e782107badf7399e99272c5f0d4cbdb04897c2441b59ac214054f71cae` |
| Linux arm64 | `6d507db70faf0c867843f3ad730fb45c60865dbeebda933489682180b3e4487a` |

The repaired capture passed the full ordinary Go suite: 1,670 passing test
events, no failures, with the three documented Windows platform/privilege skips.
The core package completed in 195.480 s. All 13 current-artifact process proofs
passed in 90.309 s with zero skips/failures and unchanged executable/proof inputs.

Focused push race execution had zero race warnings and 104 passing events, but
failed its strict allocation equality assertion: the unknown request improved
from 43 to 42 allocations after more warmup. This is recorded as a failed test
run, not a race-suite pass. A test-only derivative
`1cbcf07ac1d1a441a60760f796cd3da93d40f3290340b79f5dda9d79e23604d6`
keeps production and modules byte-identical, rejects allocation growth beyond
one allocation of pooling noise, and keeps exact HTTP404/no-body-read assertions.
The first tolerance change still failed one of three race runs when pooled HTTP
allocations rose from 42 to 44; the other two passed. That unsuccessful test-only
attempt is retained. The stronger test-only derivative
`912dbe1a1e10bfd7b26b0378eb9001772eda4c45bd2398401f247f2c2d8fc92c`
measures the ingress lookup directly, requiring exactly zero allocations at
both head counts and account counts, while separately exercising full HTTP404
and zero body reads. It avoids claiming that net/http pools themselves allocate
nothing. Three race executions of that replacement passed in 118.745 s, with zero
race warnings/failures/skips. Production and
modules remain byte-identical to the repaired capture. The full core race run
on the earlier `1cbc` measurement derivative finished in 863.891 s: 665 passing
events, zero race warnings, and one failed allocation-measurement function.
This remains a failed suite, despite the replacement lookup test passing.
Actual Alpine node/web execution subsequently completed as described below.
Earlier protocol-6/7/8 container or race results
are not claims about this artifact. The current Alpine harness also checks
nginx HTML/asset security headers, cache policy and the new web Docker healthcheck.

See [push-final-review.md](push-final-review.md),
[partition-causal-repair.md](partition-causal-repair.md) and
[container-validation.md](container-validation.md) for findings, repairs,
retained-history costs and exact platform boundaries. Independent dashboard
contract review found two valid empty-value configurations blocked by forms
(Kafka messages and RabbitMQ passwords), plus future-option preservation. Opus
repaired all three findings; the unchanged reviewer harness then passed 114/114
against a copy of the frozen node artifact. Opus's private 28-check browser proof and successful
workspace typecheck/production build are frontend evidence, not backend release
approval. JSONata expansion is subsequent work and is absent from these captures.

Actual Alpine execution on `cde59` passed 10/10 node checks and all 32 push
functions (105 passing events, zero skips). Recovery passed 14/15 functions;
the remaining fixture failed before recovery because two fixture databases used
the same external origin identity. Root has separated those fixture identity
directories; a Linux rerun remains required. The web harness initially failed
by reading only one Cache-Control field. A separate consumer correction passed
headers, immutable assets, SPA fallback, and Docker health. Both original failure
and corrected evidence remain in the container report. These container results
predate the latest dashboard fixes and working protocol-10 features.
