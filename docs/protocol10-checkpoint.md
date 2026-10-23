# Protocol 10 development checkpoint

This is development evidence, not whole-release approval or a claim of Uptime
Kuma parity. Protocol 10 adds HTTP OAuth2 client credentials and guarded JSONata
conditions for HTTP, MQTT, SNMP, and MongoDB. Upgrade peers together; protocol-9
container/process evidence does not certify these additions.

## Immutable development source

Source manifest:
`0cc233a7be89d24cb8c10bd7f22e5456aa19a9b10ad10674e7a3b3d21111f2d9`.
The snapshot includes the owned-output-pipe worker repair and the corrected
recovery fixture identity directories. It predates the subsequent MQTT review
repair, final numeric compatibility decisions, and final dashboard controls.

| Check | Executed result |
| --- | --- |
| All Go packages, ordinary native Windows tests | 1,935 passing events, zero failures; core 161.132 s |
| Windows platform/privilege skips | Two Unix `?` filename tests; one privileged origin symlink case |
| Source bytes before/after test | Unchanged |
| Symbol-level Linux Go vulnerability scan | Zero reachable findings; exit 0 |
| npm audit | Zero advisories |
| Browser fixture binary | Windows amd64 build, SHA-256 below; browser results pending |

These were **not** race-instrumented Go tests. The known module-only
`GO-2026-5932` finding remains listed for `x/crypto v0.58.0`; the scanner did not
identify a reachable imported symbol for this application. Dependencies were
not blindly upgraded to suppress a module-only report.

Windows development binary:
`2c6ca87646f27559cb572f3665abb4a85b06177f5c063c3f857288fd5b7488e7`.

Evidence resides in `.canary/alpine-validation/windows-race/runs/` under
`protocol10-development-all`, and `.canary/security/20261022T193050281818Z/`.

## Other evidence and repairs

A separate development binary passed the two-process OAuth2/JSONata proof in
19.227 s: actual loopback TLS grants and service checks, query mismatch despite
HTTP 200, one connected down alert, one recovery alert, and credential-free
history. Its SHA-256 is
`0ec39ebc561abb1e9fa63a7fd51265ea6c9adefaec92aa43c9ef1b4f21a94f32`.
The initial proof fixture used an invalid timeout greater than its interval;
the node correctly rejected it. The corrected fixture passed.

Independent review then reproduced an MQTT difference: the first JSONata
mismatch returned down even though a later queued publication matched. The
repair now waits through mismatches within the original deadline and packet
budget, preserving worker-unavailable `pending`. The unchanged reviewer
regression passed in 2.846 s; existing MQTT tests passed in 2.870 s. That repair
is outside the full-suite snapshot above and requires combined validation.

The worker repair separately passed process tests, race-instrumented parents,
2,048 observed stress children, 1,064 reference children, and Oracle fixtures.
See [worker-result-validation.md](worker-result-validation.md) for exact scope
and historical attribution limits. The immutable worker/recovery artifacts
subsequently passed on actual Alpine and restricted Docker: 41 process events
and 49 recovery events in each mode, with zero failures or skips. See the dated
entry in [container-validation.md](container-validation.md); these leaf results
do not certify the later combined application.

## Subsequent development work

The first independent finite-profile review found matcher-continuation errors
being swallowed and native sort comparators being ignored. Both repairs pass
the original regressions. The second review then established four further
false-UP mechanisms: skipped matcher calls for fractional limits/empty input,
incorrect native-partial error semantics, reduced replacement match objects,
and shifted optional capture slots. Its 105 stable cases include 56 false-UP
assertions, ten finite DOWN mismatches and 39 passing controls. Repairs are
active. See [jsonata-matcher-sort-review.md](jsonata-matcher-sort-review.md).
The passing source checkpoints below are known to contain those semantic
defects; their platform and test results are not release approval.

Public SVG badges now project only explicitly published status data. Independent
backend review found no correctness or privacy blocker. Visual/share-link work
remains assigned to Opus and is pending its provider session reset.

The new local observed-time archive preserves original validity deadlines and
closes coverage transactionally on configuration changes. Pure arithmetic passed
independent review. Focused SQLite/runtime/API tests pass, including failure
injection and separation from peer history. Storage review completed with no
remaining findings after the
malformed-query correction: 25 focused tests and vet pass. See
[availability-history.md](availability-history.md) and
[availability-storage-review.md](availability-storage-review.md). No dashboard
coverage or release claim follows from these backend checks.

`scripts/process-proof-validation.py` now captures an immutable runtime source
and records fixture, binary and source hashes around the process suite. Its
first combined development run passed all 14 proofs in 89.046 s with no skips,
failures or changed inputs. This includes public badge and local observed-time
coverage assertions in the two-process publication proof.

That development source manifest is
`8b5c1bcaefddd44cb20ef8d28cb748001596054aaf1a8c27de1bb28fb4497ad2`.
The compiled Windows binary is
`fa832f2fe5e39555cb6cb4a7b99d343083c7009aa6e53baa5ec118434f735bb3`.
The same source passed all ordinary Go packages: 2,444 passing test/subtest
events, zero failures, three expected Windows platform skips, and 144.404 s
for the core package. These were not race tests.

The archive's focused race selection passed 50 events with zero skips/failures
or race warnings. The expanded worker race-parent selection passed 1,085 events
with zero skips/failures or race warnings. Worker children were separately
checksummed, uninstrumented production artifacts with the original resource
limits; these tests do not instrument the children.

The first full core `go test -race` attempt failed its worker-dependent query
checks: it self-executed race-instrumented children inside the fixed production
memory limit. It recorded 770 passing events, six failure events across three
functions, two expected Windows skips, and zero race warnings. The original
failure remains under the `protocol10-core-race` run directory. Repeating all
six OAuth/query functions with race-instrumented parents and the existing
separately built production-worker bridge passed 19 events with zero failures,
skips or race warnings. The runner now supports `--core` for a complete repeat
with that same boundary; no test assertion or worker limit was changed.

That complete core repeat subsequently passed **776 test/subtest events** in
**960.785 seconds**, with zero failures or race warnings and two expected
Windows-only skips for literal-question-mark path fixtures. Source, production
worker and process-fixture hashes matched before and after. The evidence is
`.canary/worker-race/20261022T204819871892Z/`; its summary SHA-256 is
`fc29077f27a6772f45b9c052383d82905e922b7dad73fb2b73a9d580e3c6a0f7`.
This validates race-instrumented parent execution at the unchanged child
resource boundary. It does not instrument the children or certify later
evaluator repairs, API changes, container configuration or the current tree.

Exact evidence:

- `.canary/process-proofs/20261022T202229595007Z/`: original proof log and
  summary, with `reporter-counts.json` parsing the original Node spec reporter.
  The first runner did not select TAP, so its original `counts` object is empty;
  the supplemental count file preserves that failure of report extraction.
- `.canary/process-proofs/20261022T203238600179Z/`: the corrected runner
  explicitly selects TAP and passed the focused publication proof.
- `.canary/alpine-validation/windows-race/runs/20261022T202523Z-protocol10-observed-history-all/`:
  complete ordinary-test JSON events and source verification.
- `.canary/worker-race/20261022T203044639796Z/`: worker-parent results and
  checksums for both child artifacts.

After that capture, malformed availability query strings were explicitly
rejected instead of silently defaulting the requested range; its focused
regressions pass. Notices and validation scripts also changed. Independent
reviews and final current-source/platform validation remain separate gates.

Actual Alpine and restricted Docker passed the archive/recovery selection
(96 events in each mode) and pure availability selection (94 events in each).
The same runs exposed a reproducible evaluator failure at its supported 1 MiB
document boundary: `$length($)` stopped the worker and returned pending. Each
runtime passed the other 495 query events. Memory observations were consistent
with pressure near the fixed Linux data limit, but did not establish the exact
exit cause. This is a failed success requirement, not a waived test or a clean
platform gate. An allocation repair is assigned without increasing limits or
reducing the supported document size. See [container-validation.md](container-validation.md).

## Remaining validation

Finish the fresh callback review (archive storage/integration review passed);
exercise the final forms and
publication UI in the browser; capture the combined
source; then run whole Go/vet, compiled-process proofs, parent/core race checks,
actual Alpine node/web/worker checks, and dependency/license checks against that
capture. The corrected Unix recovery fixtures passed the actual Alpine leaf
run above; later source still needs combined platform validation.

Feature and option gaps remain tracked in [parity-audit.md](parity-audit.md).
This checkpoint does not replace those requirements with its passing test scope.

The subsequent container configuration places `/identity` on its own retained
volume instead of keeping sequence reservations under `/data`. The stopped-node
migration is documented in [backups](backups.md). Its exact derived source has
separate deployment validation; neither the 8b5 application tests nor the prior
container images establish that later change. The runtime feasibility study
in [jsonata-runtime-options.md](jsonata-runtime-options.md) recommends finishing
the current bounded repairs; no evaluator replacement has been adopted.

## Subsequent development results

Frozen R6 snapshot `f429a3cb2571c0ca2a3087c65e9511c161562277512c84e3a39c07c701b97d88`
passed the unchanged maximum-document test on actual Alpine and restricted Docker
under the original 128 MiB worker limit. Each mode passed 700 query events and
72 pure-rollup events. Its production node also observed UP for an exact
1,048,576-byte JSON response. The earlier resource failures above remain part of
the evidence. These results do not clear the seven further compatibility
mechanisms subsequently reproduced by the third evaluator review.

An isolated official-JavaScript candidate is being evaluated against the same
reference cases and resource profile. It has not replaced the production
evaluator. Working protocol 11 separately adds group parent links and read-only
current health; its integration review, UI and process evidence are separate
from every protocol-10 result recorded here.
