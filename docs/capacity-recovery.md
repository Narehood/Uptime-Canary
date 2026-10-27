# Fixed-workload capacity and recovery proof

The release objective requires evidence about operation under load, beyond
individual monitor and notification fixtures. `apps/proof/test/capacity.test.ts`
adds a fixed workload against two independently running Go/SQLite nodes. It is
not a maximum-capacity benchmark or a promise about arbitrary retained history.

## Workload and assertions

- 64 HTTP monitors, assigned to both nodes: 128 observing slots, one-second
  intervals and 500 ms probe timeouts. All targets are private loopback fixtures.
- Two independent webhook destinations per monitor. One accepts immediately;
  the other delays each response by 75 ms and returns HTTP 429 with a four-second
  Retry-After until explicitly released by the fixture.
- All observing slots must reach up, then down. Every fast destination must
  accept while the other destination remains rate limited.
- Twenty-four authenticated overview/metrics reads run during the backlog.
  Each has a ten-second abort deadline. The proof records P95 and maximum
  latency; these observations are not an SLA or an adaptive pass threshold.
- Both node processes are killed and restarted against their own private
  databases. Persisted failed attempt identities, failure counts and retry
  deadlines must survive. No migration or graceful receipt handoff is used.
- The second destination is released. Every retained down obligation must name
  a concrete satisfying receipt in its owning node's current delivery view,
  either its exact key or a sibling satisfying its reconciled episode. That
  receipt must be accepted by the fixture provider and present on both peers,
  within the existing maximum 60-second
  backoff plus a 45-second processing window. Independent sources cannot cover
  one another merely because they share a monitor. Recoveries follow after
  probes return up.
- Each node is configured to retain at most eight raw samples per monitor.
  Histories must stay local and respect that cap while probes keep running.
- Each monitor/destination must accept down and recovery. Requests for the same
  original source/destination/event must retain one delivery key. Once both
  peers hold the exact accepted receipts and in-flight sends settle, further
  election/sync windows must produce no new accepted sends. Initial proof lag
  and ambiguous sends may produce duplicate provider acceptances, as in the
  existing HA contract; they are reported rather than counted as exactly once.
  Attempts observed before restart, on later polls or in provider requests stay
  in a retained ledger. Missing attempts, unexplained cancellations and retries
  remain unsettled. All retained down/recovery obligations are checked at the
  final receipt barrier and again after the quiet window; a quiet provider alone
  cannot hide a deferred retry. A full 1,000-entry delivery page fails the proof
  because this public endpoint has no pagination cursor.

The read-only delivery response exposes `sourceIncidentId` before public alias
canonicalization and `satisfiedByReceiptId` from the same locked projection used
to report acceptance. Exact receipts take precedence. The proof joins this
concrete witness to receipt IDs and provider requests; a canonical display ID
or an accepted state alone cannot discharge an obligation. These additive view
fields change no persistence, scheduling or peer protocol behavior.

The diagnostic reports workload size, API latencies, outage-to-fast-acceptance
time, backlog drain time, accepted notifications and provider concurrency. The
history cap concerns raw measurements; it does not retire causal incident,
configuration, receipt or proof history.

## Execution and storage

Run one command through the shared bounded runner:

```text
python scripts/bounded_validation.py --timeout 480 -- python -B scripts/process-proof-validation.py --test capacity.test.ts
```

The existing 240-second whole-test timeout is an additional aggregate acceptance
gate. It includes setup, API reads, restart, all phase waits and quiet windows.
Individual phase deadlines are ceilings within that total, not additive time
allowances: a run can satisfy each phase deadline and still fail the total
budget. The outer 480-second guard also covers the production build and runner
cleanup. Neither budget was increased by the retained-obligation review repair.

The existing proof runner builds one disposable production binary and creates
fresh private SQLite databases under the guard's temporary directory. The
guard enforces the repository, scratch and shared-cache budgets and removes
its own outputs after the command. No user database, `.env`, external provider
or paid measurement is involved. No persistent VM, copied source tree or
private build cache is created.

## Evidence and limits

The first Windows run failed before the backlog phase. Source fingerprint
`e0c190ab53133b91020f2d119a169ac587fea2c16fe05d5142ef00f529465ed3`
passed workspace typecheck, then the process proof failed after 48.621 seconds:

```text
Timed out after 45000ms waiting for all 128 observing-node slots are down
Last condition error: The operation was aborted due to timeout
```

The fixture recorded 1,899 target requests and recent peer proxy responses
including HTTP 502. Both node processes were still alive. These observations
show a failed liveness gate; they do not identify the cause or prove that all
node observations were down. Backlog, restart and later recovery assertions
were not reached and are not counted as passes. The guard completed in 95.843
seconds with unchanged source, scratch peak 187,180,873 bytes cleaned to zero,
and no shared module-cache growth. The original workload and deadlines remain
unchanged while an isolated runtime profile investigates the cost.
The initial run uses the merged protocol-16 baseline on a separate validation
branch while the Steam protocol-17 PR is in hosted review. Any later result
must identify its exact source and platform; it cannot be silently attributed
to another branch or tagged release.

This workload does not establish sustained multi-day behavior, memory bounds,
disk-space reclamation, arbitrary peer counts, long retained-proof antichains,
maximum monitor count, maximum safe polling rate, queue fairness under every
ordering, or throughput for expensive SQL/JSONata/remote probes. Those remain
distinct capacity and operating-limit questions. Wire-page limits and capped
probe workers alone do not bound total node history or reconciliation work.


### Investigation in progress

The isolated evaluation fixture completes 12 full evaluations of 64 outages
without destinations in about 104 ms; 32 validated peer sweeps take about
552 ms. Adding the two configured channels increases those observations to
about 260 ms and 1.148 s. The latter CPU profile attributes substantial work to
JSON encoding/decoding and notification-target construction. These are scoped
profiles, not end-to-end acceptance or a confirmed complete root cause.

A candidate change evaluates only the changed monitor after a sample commit,
while the existing scheduler tick still evaluates every monitor. The unchanged
process proof on source
`7157aa8a52bb2042637e5a12375d16e39465a1c579f29c95ed76188e2860ece5`
**still fails** at the same 45-second down-observation gate. It records 3,505
target requests, but the later assertions remain unexecuted. This candidate is
not called repaired or ready. Guard time was 125.609 seconds, source unchanged,
scratch peak 471,400,738 bytes cleaned to zero and no module growth.

`BenchmarkCapacityLiveRuntime`, run explicitly with `-benchtime=1x`, adds a
23-second profile of live local probes, peer HTTP and destination scheduling.
It is diagnostic only; the process proof remains the liveness authority.


The scoped-reconciliation candidate on source
`93b0a28dce3df59555f8283eb9691cdfc9eb5cccc18fd9472e12922bc91d8ad6`
clears the initial down-observation gate, both destination assertions, the 24
API reads and persisted-attempt checks after restarting both processes. It then
fails the original 45-second backlog-drain assertion (118.312 seconds in the
proof; 195.937 seconds guarded). Later history/recovery assertions were not
reached. Scratch peaked at 508,090,082 bytes and returned to zero; source and
modules were unchanged.

The original new fixture's drain budget conflicts with the accepted existing
retry policy: `notifications.go` schedules every fifth failed attempt 60 seconds
later, independently of a shorter provider Retry-After. A provider becoming
healthy does not cancel that persisted deadline. The fixture will be corrected
to allow that existing backoff plus the original 45-second processing window,
without changing production retry policy, workload or earlier phase deadlines.
The failed fixture is retained at Git commit `978078a` before that correction; this failure is
not relabelled a product pass. The final report must distinguish this fixture
correction from the production evaluation/reconciliation repairs.


The live profile attributes 31.96% of sampled CPU to `reconcileEpisodes` and
15.03% to positive-path search (nested costs, not additive). It also shows
repeated `evaluateInterrupted` / `sourceProjection` work while peer proof arrives.
The production candidate partitions reconciliation by exact monitor and check
generation before folding. All records, embedded proofs and within-scope
barriers remain; unrelated scopes cannot supply continuity or predecessor
closure. The sample-completion path also evaluates its changed monitor instead
of evaluating the entire configuration. Full ticks still handle dependencies,
clock changes and the complete configuration. Both changes require the broad
causal/delivery suite as well as the corrected end-to-end proof before approval.


The backoff-corrected run on source
`dd83c856015ab245c7d48cc2d23b1ba6bf821772ed087da06a6715ead716e3e0`
reached history inspection, then hit the unchanged 240-second total test limit.
It recorded 86 fast and 100 backlog down acceptances for 64 monitors, no
recovery acceptances and zero fixture errors. Thus duplicates and the later
recovery/retention assertions are not cleared. The guard reported 279.938
seconds, scratch peak 228,353,162 bytes cleaned to zero, unchanged source and
zero module growth. This result contradicts complete capacity acceptance.

Further inspection identified a redundant-source path: an unchanged original
can be hidden by a merged alias projection. `saveEpisode` compares it with the
projection rather than its retained original, then unnecessarily reprojects all
sources. A new fault-injection regression closes storage only after settling
that original. An actual no-op must avoid a transaction; missing local delivery
provenance, a new run or a new uncertainty must still attempt persistence. This
regression and the original end-to-end workload are the next repair gates.


The no-op regression first failed only its unchanged-original case on source
`717fabf306f9eaff18fb79f6e482ad367ae0d4a441bbc429ccff391f0b09c908`;
all three changed-evidence/local-marker controls passed. After the source-based
comparison repair, all four subcases pass unchanged. The process proof on
`a7eca2ce1938791ab6d32b5c70179df0e0aae6b6f3b3ab78e461a44256903360`
now completes the observation, API, restart, backlog, history and recovery
phases in 62.130 seconds, but fails its final acceptance-total assertion:
394 accepted requests versus its assumed 256. The final distribution is
114 fast down, 97 backlog down, 92 fast recovery and 91 backlog recovery;
fixture errors are zero. This is still a failed proof, not overall acceptance.
Guard time was 168.468 seconds, scratch peak 437,304,634 bytes cleaned to zero,
unchanged source and no module growth.

Before treating those counts as a product deduplication defect, the next
measurement distinguishes the same persisted delivery key sent twice from
different original sources that dispatch before complete paged proof converges.
The accepted HA contract permits independent locally witnessed delivery while
historical proof is incomplete; it does not promise globally exactly-once
provider acceptance. See notifications.md, architecture.md and
partition-causal-repair.md. The existing final assertion remains unchanged
while that distinction is measured.


### Correcting the new fixture's exactly-once assumption

A diagnostic-only rerun on source
`3be46127035bfe666a42fee1117be71dc7aa2d943c663ca6dfc938d45344ca59`
completes all workload phases in 17.992 seconds but retains the failing final
assertion: 259 accepted requests versus 256. There are 257 accepted identities
and two repeated accepted identities. This distinguishes an extra original
source from repeat acceptance of an existing key; it does not establish which
actor acknowledged each request. Guard time was 60.140 seconds, unchanged source,
scratch peak 264,153,242 bytes cleaned to zero and no module growth.

The original globally exactly-once expectation was stronger than the accepted
HA contract. Connected transport does not mean all paged proof and receipts
have already arrived. `scheduleTargetNotification` uses staggered senders and
locally witnessed obligations; it does not wait for quorum agreement. The
existing notification contract explicitly permits partition/ambiguous-send
duplicates and promises stable retry identities and quietness after successful
receipt exchange. Neither source-based down obligations nor backoff will be
weakened to make that original expectation pass.

The corrected fixture keeps the original workload and earlier liveness gates.
It lets the existing bounded send deadline settle, requires both nodes to hold
every exact accepted receipt ID, and then asserts no further accepted sends
through several sync/election windows. It checks coverage of every destination
and stable keys per original source/event/destination, rather than assuming a
single global source existed before paged catch-up. The failed expectations and
results above and their Git versions remain historical failures, not passes.


## Historical local result before review integration

On Windows amd64, source
`7b00d956d776ff7355f80aa400dd4dd8652376f6608c61e322519d1cec7ee744`
passes workspace typecheck, the selected capacity core tests (39.467 seconds)
and the corrected process proof (1/1, zero skips, 62.622 seconds). The diagnostic
records 24 API reads, P95 137 ms / maximum 146 ms, 4.756 seconds from outage to
all fast destinations, 6.728 seconds to drain the released backlog, history
limit eight and provider concurrency peak 12 (within the two eight-worker pools).
There are 402 provider acceptances across 306 identities, including 96 repeated
acceptances before the converged quietness assertions. Those counts remain
visible; this is not an exactly-once claim. Every destination/event is covered,
keys remain stable per original source, both nodes learn the exact receipts,
and subsequent sync/election windows stay quiet. Binary SHA-256:
`9a5084f6419be620b44e6a6e18f280a5cd613da4d13e4a5870a872b0e43d91bd`.

The same source subsequently passes the complete canary package: 896 passing
test events, zero failures and two expected Windows question-mark filename
skips, 217.640 seconds including compilation. All-node vet passes in 101.485
seconds. This is CGO-disabled native evidence, not race execution. The scope
isolation test compares the partitioned implementation with the previous
unpartitioned fold over 64 interleaved monitor/generation scopes, including
foreign predecessor references and private uncertainties, in four fixed orders.
The unchanged-original regression and its three persistence-required controls
pass without changing their expectations.

The two guards complete in 256.219 and 334.547 seconds with unchanged source;
scratch peaks are 416,736,742 and 449,832,221 bytes respectively, each returning
to zero, with no shared module growth. The idle repository remains under its
1.25 GiB target. Full process-suite, hosted Linux/Alpine/race/arm64 validation
and review of this candidate remain pending. None of these local results
certifies another branch, a tagged artifact or the complete shipping goal.


## Historical protocol-17 local result before review integration

The capacity branch now includes the Steam encoded-options repair from
`5d9637a`. On clean merge commit `f94e23752925af5f59a99e5569a83f42bf971884`,
source fingerprint
`f94663a0234a838097eb3afb4a375eed3fe0de3e3d0b1db2dcba3108e2005dde`
passes all node packages: 3,794 passing test events across 20 packages with
tests, zero failures, plus the command package without tests. The four actual
Windows skips are both question-mark recovery cases, the origin symlink
subcase and the Docker Unix-socket test. Test-command time is 406.047 seconds;
all-node vet passes in 40.984 seconds. The guard completes in 452.688 seconds,
with sampled scratch 520,315,047 bytes below the unchanged 512 MiB ceiling,
cleaned to zero and no module growth.

The same source passes the entire separate-process suite: **21/21**, zero
failures, cancellations or skips, against one production binary. Build time is
76.828 seconds, process-suite time 521.297 seconds and total guard time 616.343
seconds. Source, fixtures and binary remain unchanged; scratch peaks at
456,233,957 bytes and returns to zero with no module growth. Binary SHA-256:
`6551c9b490d69ee18ad8b91618c013271b8173bfa46761e336728e8d39b92152`.
The suite covers peers, failover, restart, notification providers, monitor edits,
public history, passive push and the Steam/system-service integrations. It does
not run the separately selected historical upgrade fixtures.

The capacity case in that full suite passes in 87.869 seconds. Its 24 API reads
have P95 272 ms / maximum 428 ms; outage-to-fast acceptance is 6.414 seconds and
backlog drain 8.110 seconds. Provider concurrency peaks at 14. It records 348
acceptances across 314 identities, including 34 repeated acceptances, with no
fixture errors and successful receipt-converged quietness. These are measured
local workload results, not a performance SLA or globally exactly-once promise.

Hosted Linux/Alpine/race/native arm64 validation and review of the capacity
changes remain pending. The Steam PR's checks cover its own head, not these
additional runtime changes. No user data, external provider or paid measurement
was used. This remains development evidence, not a tagged release or completion
of the near-parity shipping goal.


## Reviewed integration: recovery failure retained

The integration at `fc7090d` combines the saved capacity work with the reviewed
PR #6 repairs. On Windows amd64, source fingerprint
`ce12fe00f2eaa41a35d6613085085b5a2deb2ff88b3c4ceabed27d0e5c31f85f`
passes the complete changed canary package: 953 passing events, zero failures
and the two expected question-mark filename skips. Package vet and proof
TypeScript checking pass. These are changed-package results; the other native
packages retain their separate PR #6 source scope.

The first combined package run was stopped at 543,318,165 bytes of scratch.
It did not complete and is not a pass. Compiling one test executable before
running it separately clears the same package in 229.765 guarded seconds,
with a 199,568,377-byte scratch peak. Both guards remove their outputs,
restore the idle budget and show no shared-module growth. No storage limit
was increased and no cache or source tree was copied.

The unchanged two-node capacity process proof then **fails** after 75.227
seconds at the existing 45-second all-observers-recover gate. Backlog, restart,
drain and local history phases were reached. The last overview request aborted;
both node processes stayed alive and the fixture served 7,593 target requests.
The recovery-phase diagnostic records 955 notification requests, 746 rejected,
209 accepted identities and no repeated accepted requests or fixture errors.
Accepted counts are 124 fast down, 6 fast recovery, 75 backlog down and 4 backlog
recovery. Later recovery-delivery and converged-quietness assertions are not
cleared. This is a failed liveness gate, not an overall acceptance or a flaky
pass. Its workload and deadlines remain unchanged.

That failed run uses binary
`93596d438563f8d2236b0f9a141997f33c80c9e555dcf1a1674e25083604c145`.
The guard completes in 163.375 seconds, with source, fixtures and binary
unchanged, scratch peak 437,670,908 bytes cleaned to zero, and no module growth.
The original failure remains before subsequent profiling and repairs in Git.

A separate fixed recovery benchmark adds actual restart, backlog release and
return-to-up phases to the earlier down-only diagnostic. Its CPU profile finds
repeated notification-target construction at 20.23% cumulative sampled CPU,
including channel decoding at 15.39%. A bounded current-payload target cache
removes that decoding from the leading samples while retaining current-head,
deletion, proof and payload checks. It has at most 128 entries and 1 MiB of
raw payload strings. Independent review found no material cache safety defect;
functional controls cover credential/route/provider changes, conflicts,
withheld proof, deletion, redaction and cache churn.

The asynchronous profiles perform different amounts of work: 75.10 CPU seconds
in 36.89 seconds before caching, versus 77.03 CPU seconds in 39.77 seconds in
the next diagnostic. Those totals do not demonstrate an overall speedup. The
latter profile still attributes 30.34% cumulative CPU to source projection and
18.49% to uncertainty resolution; these nested costs are not additive. The
unchanged process workload remains the acceptance gate. No tagged release or
complete near-parity sign-off follows from the diagnostic.


## Integrated reviewed-source Windows acceptance

The bounded channel-target cache at `1510938` passes its 59 focused target,
suppression and integration events, vet and proof typecheck. Its unchanged
capacity process case passes in 34.016 seconds, before the later parent review
follow-ups. That result has source fingerprint
`59d94f2d8d6dc6e30812600e24869c9b7c71dc07eb6f919113e277c6018ec157`.
The earlier `fc7090d` recovery failure above remains a failure.

Merge `b0d181c39bf0be2388af9c75e8cda51f1d50ebde` includes the parent PR #6
frontier/privacy follow-ups and the bounded target cache. On Windows amd64,
source fingerprint
`69b2a3b5d3ec86edc3544999245a449328d7ece9eebfe76a513178ca7c198e02`
passes the complete changed canary package: **971 passing test events**, zero
failures, with the two expected question-mark filename skips. Vet and proof
typecheck pass. Compilation and test execution are separate so their temporary
storage does not overlap; the guard completes in 308.312 seconds, with sampled
scratch peak 522,863,628 bytes below 512 MiB, cleaned to zero, unchanged source
and no module growth. Other Go packages and frontend/Python checks retain the
separate exact-source records in PR #6; no new all-package total is inferred.

The same integrated source passes the unchanged production-binary capacity
proof: **1/1**, zero failures, cancellations or skips, in 35.531 seconds.
Build time is 68.719 seconds and total guard time is 118.719 seconds. Source,
fixtures and binary remain unchanged; scratch peaks at 508,546,430 bytes and
returns to zero, with no module growth. The idle repository is 1,234,921,511
bytes, below 1.25 GiB. Binary SHA-256:
`e1edccec7f7492957dc1f7c69e26229dc3c7a78667bec06afeaf6a42b33ff0b4`.

All original phases clear: both nodes' observations, destination isolation,
API reads, abrupt restart, retained retry attempts, backlog drain, local raw
history limits, recovery delivery, stable source/destination keys and quietness
after exact receipt exchange. The 24 API reads record P95 73 ms and maximum
90 ms. Outage-to-fast acceptance is 3,468 ms; backlog drain is 2,232 ms. Raw
history stays at the configured eight samples per monitor. The two provider
pools together peak at 12 concurrent requests. There are 330 acceptances across
263 identities and 67 repeated requests before converged quietness, with zero
fixture errors. These are measured fixed-workload results, not globally
exactly-once delivery, an SLA, maximum capacity or sustained multi-day evidence.

The complete 21-case process suite was not rerun locally on this combined
source; it is a required hosted gate. Linux/Alpine/race/native arm64 and review
acceptance for each final Git head are tracked in
[PR #7](https://github.com/Narehood/Uptime-Canary/pull/7). Markdown-only evidence
updates leave the v3 source fingerprint unchanged, while the new Git identity
still requires its own hosted artifacts. No tagged release or full near-parity
sign-off is claimed.

## Native Linux failure and scoped uncertainty follow-up

The Windows checkpoint above does not establish Linux acceptance. On exact
head `82a0573` (the same `69b2a3b5…` source fingerprint), hosted native Linux
amd64 [run 40069898317](https://github.com/Narehood/Uptime-Canary/actions/runs/40069898317)
passes 20/21 process proofs. Capacity fails after 72.297 seconds at the unchanged
45-second gate requiring all observing-node slots to recover. Later recovery
and quietness assertions do not run. The clean extracted Linux binary has
SHA-256 `db8c011ef573633bb1016939981ef552be00b22e4868c431bf18679d6479c9a5`.
Its recovery-phase diagnostics record 7,085 target requests, 1,098 notification
requests, 755 rejections, 343 accepted identities, no repeated accepted requests
and no fixture errors. The guard completes in 606.24 seconds; sampled scratch
peaks at 436,935,245 bytes and returns to zero, with no module growth. This is
a failed liveness gate, with no storage violation. The other six hosted jobs
and CodeRabbit pass on that head; they do not waive the Linux failure.

Commits `a4f5a23`, `db7141a` and `71c94c9` address a recurring cost in embedded
uncertainty resolution. The previous global fixed-point implementation remains
available as a comparison oracle. The production wrapper groups originals by
exact monitor and generation, preserving original map keys and the existing
fixed-point algorithm. Boundary identity checks still consult all originals;
all facts and links remain available. A foreign-scope header in an original or
any fact boundary selects the original global behavior, including nested raw
local headers. No proof is discarded or promoted, and persistence, wire format
and notification policies do not change. Focused independent source reviews
find no material defect. The earlier profiles identify this cost but do not
establish that it alone caused the hosted timeout.

At `71c94c9`, Windows amd64 source fingerprint
`236a1b663e1721228bd7a2c69035e45a2f8ea260bbcf57a35b89bd908c37fc1c`
passes 25 focused differential controls and the complete changed canary package:
996 passing test events, zero failures and the two expected question-mark
filename skips. Vet and the proof typecheck pass. Controls compare public and
nested private proof state, input immutability, reordered/delayed evidence,
aliases, collisions and foreign-header fallback. The guard completes in
263.031 seconds, with sampled scratch peak 523,294,773 bytes below 512 MiB,
cleaned to zero, unchanged source and no module growth.

The same source passes the unchanged capacity proof, 1/1 with no failures,
skips or cancellations, on normal production binary
`2adf6c1c0d4f69db56d7bfd12fd043515abb068c483daf44a9686252deea73f1`.
Total test time is 45.489 seconds; this includes all phases and is distinct
from the 45-second recovery-phase gate. The proof command takes 45.750 seconds,
the build 64.797 seconds and the guard 118.968 seconds. Source, fixtures and
binary stay unchanged. Scratch peaks at 437,702,568 bytes and returns to zero;
the idle repository is 1,235,120,799 bytes, with no module growth.

All original phases clear on Windows. The 24 API reads record P95 89 ms and
maximum 103 ms; outage-to-fast acceptance is 3,649 ms and backlog drain is
3,114 ms. History remains at eight samples per monitor, with provider peak
concurrency 14. There are 343 acceptances across 271 identities and 72 repeated
requests before receipt-converged quietness, with zero fixture errors. These
measurements do not establish an overall CPU improvement or Linux recovery.
The Linux failure remains recorded, and the repair requires fresh exact-head
native Linux, Windows, arm64, Alpine, race and full 21-process acceptance in
[PR #7](https://github.com/Narehood/Uptime-Canary/pull/7). No workload or deadline
was relaxed, and no release approval is implied.

## Retained-obligation review follow-up

The native Linux amd64 package at `3b3da51`, source `236a1b66…`, subsequently
passes 21/21 process proofs in
[run 40072971455](https://github.com/Narehood/Uptime-Canary/actions/runs/40072971455/job/120810202011).
The extracted binary reports that exact clean commit, protocol 17, Linux amd64
and Go 1.27.3; binary SHA-256 is
`71aeade357119d3c1d93c74d689ff0469d4b6aaccafaa77c6091ed424019b1b5`.
This clears that source's Linux gate, without erasing the `82a0573` failure.
It precedes the stronger obligation assertions below.

Review [4366366057](https://github.com/Narehood/Uptime-Canary/pull/7#discussion_r4366366057)
identifies a proof gap: counting accepted monitors could declare drain while
another original source still awaited retry. Commit `4cbed02` adds the retained
ledger and concrete receipt witnesses described above, keeping the existing
monitor coverage, restart and quietness assertions. Independent source review
approves the focused repair. Twelve pure control groups include the decisive
negative case (one accepted source and another independent 60-second retry),
exact and sibling positives, alias splits, missing provenance/rows, receipt
scope, exchange and enumeration failures. Nine Go test events exercise the
receipt view's exact preference, source preservation, split invalidation and
event/destination scope.

On Windows amd64, exact source fingerprint
`90209d5b89479c6ce721c5c9373f1f9def0fab6c34d769ed9058e789419e5f12`
passes the complete changed canary package: 1,005 passing events, zero failures,
with the two expected question-mark filename skips. Vet and all three workspace
typechecks pass. The native guard completes in 313.453 seconds, with sampled
scratch peak 523,429,078 bytes below 512 MiB, cleaned to zero. The pure controls
pass in a separate 4.109-second bounded run at the same source fingerprint.

The strengthened capacity case passes 1/1 in 32.020 seconds (32.265 seconds for
the proof command, 67.125 seconds build, 113.250 seconds guarded), using binary
`14bbcdff8c053cf0a35e2f12eca9c794dcfcada9ab68608f91b073624092dfb0`.
It finishes with 320 exact and 159 sibling-satisfied **per-node obligations**,
and zero unsettled obligations; these counts are distinct from provider delivery
identities. The provider records 308 acceptances across 259 identities and 49
repeated requests before quietness, with zero fixture errors. API P95 is 97 ms,
maximum 149 ms; outage-to-fast acceptance is 3,413 ms and backlog drain is
2,376 ms. History remains at eight samples per monitor and provider concurrency
peaks at 13. Source, fixtures and binary remain unchanged. Scratch peaks at
437,701,764 bytes and returns to zero; the idle repository is 1,235,467,743 bytes,
with no module growth.

Review [4366366054](https://github.com/Narehood/Uptime-Canary/pull/7#discussion_r4366366054)
is addressed by documenting the existing 240-second aggregate acceptance gate;
no phase or outer-runner deadline increases. The optional cache-eviction
optimization remains deferred: bounded clear-all eviction is unchanged, with no
demonstrated production regression or large-working-set throughput claim.
The complete 21-process suite was not rerun locally for `4cbed02`; fresh
exact-head hosted platform gates remain required before merge. This is not a
tagged release, sustained capacity result or complete near-parity sign-off.

## PR #9: expired peer requests and recovery freshness

At `b41a266`, the [Alpine proof job](https://github.com/Narehood/Uptime-Canary/actions/runs/40106276690/job/120908810571)
passes 21 of 22 process proofs; capacity fails the unchanged 45-second
all-observer recovery gate. The complete capacity test takes 75.291 seconds.
Both processes remain alive and their local observations are Up, approximately
130–167 ms old, while the sampled remote observations are 11–29 seconds old.
The remaining receipt and quietness assertions are not reached. The node,
dashboard and race jobs pass; those results do not waive the recovery failure.
The application and process-proof source at this head is unchanged from PR #8,
so this evidence does not attribute the failure to the dashboard port change.

Source inspection identifies an amplification path: an authenticated snapshot
request can expire while waiting for the state mutex, then still merge its page,
evaluate monitoring and construct a response after admission. Its sender keeps
the old cursors on a failed exchange and retries that position. The new
`TestPeerSnapshotCancelledBeforeAdmissionDoesNotPublish` reproduces publication
and HTTP 200 before the repair, both for an already cancelled request and for
cancellation while the mutex is unavailable. The repair checks cancellation
immediately after acquiring the mutex, before applying any state. A live retry
still commits the page. Pages already admitted retain their existing atomic
publication and evaluation behavior; paging, receipt ordering, workload,
freshness and deadlines are unchanged.

This establishes an abandoned-request defect, not its share of the observed
capacity failure. Fresh exact-source hosted acceptance remains required.

On Windows amd64, source fingerprint
`441ea3ee7aaf32340dc38cbe903cc458de9b5499a4ec691a850ad31ec3f4437c`
passes the complete changed canary package: 1,071 passing events, no failures,
and the two expected question-mark filename skips. Vet passes. The unchanged
capacity proof passes 1/1 with no failures or skips; its command takes 32.969
seconds after a 74.906-second build. Binary SHA-256 is
`f6d6dda9669ac6047100fbecf4cfcab67d97e8b45c9dd10b1fc7704cdf681a38`.
Source, fixtures and binary remain unchanged. Sampled scratch peaks at
509,266,459 bytes and is cleaned to zero, with no module-cache growth.
The private report collector fails after the successful proof while parsing
non-object JSON; the proof's own saved result preserves its pass, and a
separate bounded evidence check verifies the component results. This is not
an aggregate runner pass, an Alpine acceptance or a measured speedup. The
collector is corrected without rerunning the application tests.

## Main after PR #21: intermittent Alpine recovery failure

At main commit `d03fd6a13f0b45407519031274ea3fa25e1ee18d`, the
[Alpine proof job](https://github.com/Narehood/Uptime-Canary/actions/runs/40595560956/job/122510463717)
passes 24 of 25 process proofs. The unchanged capacity proof fails after
75.683 seconds at its 45-second all-observer recovery gate. Both processes
remain alive. The last successful capacity-a overview reports 93 up and 35
unknown observations; the sampled remote observations are approximately
7.3 seconds old, while the corresponding local observations are up and about
1.3 seconds old. Capacity-b separately returns all 128 observations up. Some
overview requests reach their two-second deadline. The other Alpine phases
pass; the remaining recovery and receipt-converged quietness checks do not.
The uploaded proof log retains the original failure and detailed diagnostics.

The same unchanged source passes the fixed workload on Windows amd64 in
76.828 seconds, including three timed-out capacity-a overview requests and
eleven timed-out capacity-b requests. All retained obligations settle. Its
source fingerprint is
`358a06dbe84b8894da0906c939bf937893cd38fec9ec8cd98dd16fc6d220a5bf`;
the guard passes in 212.360 seconds, with sampled scratch peak 462,291,671
bytes cleaned to zero. This Windows result does not clear the Alpine failure.

A separate run of the existing recovery-profile benchmark identifies repeated
JSON encoding and allocation among the work performed under the state mutex.
Its Windows filesystem-sync costs are platform-specific; the profile does not
establish the complete cause of the Alpine timeout. The candidate adds positive
equality shortcuts for unchanged attempts, notification intents, samples and
incident records. Other comparisons retain the original JSON fallback. Private
uncertainties remain excluded from JSON equality and independently checked by
the existing proof comparisons. Signed floating-point zero retains JSON's
distinction. No wire format, durability, scheduling, freshness, workload or
acceptance deadline changes.

Differential controls compare the shortcuts with the previous JSON algorithm,
including exported-field changes, independently decoded pointers, nil/empty
collections, private proof and non-finite/signed-zero sample values. The focused
Go test and comparison benchmarks pass on Windows. In these small benchmarks,
unchanged attempt/sample comparisons allocate no JSON buffers; the incident
comparison drops from 12 allocations to two. These are comparison microbenchmarks,
not application throughput or an end-to-end recovery improvement.

The focused command's outer guard encounters a transient Windows directory
access failure after its Go tests pass; cleanup still returns scratch to zero.
The subsequent full-package validation is stopped by the guard because the
separate default Go cache grows beyond its 16 MiB limit. Phoenix builds are
then paused, and the user approves clearing the remaining default compiler
cache. This restores the guard's budget without deleting modules or application
data. The resumed Windows package test is interrupted at the user's request
after its temporary executable triggers Windows Firewall. Scratch returns to
zero. Further Docker-image validation uses Linux/container CI; native Windows
application/test executables must not be launched on this workstation. No
broader package, candidate process-proof or candidate Alpine pass is claimed.
The candidate remains unverified for the original failure until hosted checks
complete.
