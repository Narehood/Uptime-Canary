# Independent partition successor review, round 5

Reviewed 2026-10-22 against a private capture of **current protocol 8**.
**Two established P2 findings remain:** complete embedded closure proof can
leave equivalent later branches split, and a queued worker can resend an
accepted old down after a reversible alias projection splits. The three new
reproduction functions assert required behavior and remain failing. The original
DR4 reproductions pass unchanged. This is focused review evidence, not release
approval or a production repair.

## Original brief, prior responses, and outstanding obligations

The original review contract requires independent local delivery during a
partition, permits duplicate downs during that partition, requires convergence
when complete positive causal proof becomes connected, and forbids recovery from
unknown peers or old-receipt authority over a genuinely later outage. Exact
generation, destination, observer/run, maintenance, dependency, and provider
acceptance rules remain applicable. Complete embedded originals are proof; they
must not acquire projected alias, intent, or receipt authority merely by serving
as a bridge.

The read-only inputs were [partition-delivery-review.md](partition-delivery-review.md),
[partition-causal-review.md](partition-causal-review.md),
[partition-causal-repair.md](partition-causal-repair.md),
[capacity-repair.md](capacity-repair.md), and the existing causal, partition,
capacity, notification, and successor fixtures. Earlier reports describe their
own frozen inputs; their statements of then-current failures remain historical.

Prior responses supplied for this round:

- CR3's owed-alert and mixed-observer defects were addressed with durable,
  local-only, exact source/destination/run markers, strict peer commitments,
  source-local updates, and reversible causal projections. Markers do not
  replicate or authorize repeat/recovery.
- DR4-1 found both scheduler and final-provider successor vetoes dropping a
  closed predecessor's marked, intended, unsent down. Round 5 permits only that
  closed destination's unaccepted down through those vetoes. Repeats/recovery
  remain vetoed; ordinary route/generation/hold rules remain in force.
- DR4-2 found the proof-only path A--O1--O2--B split. Round 5 searches complete
  original and recursively embedded evidence over exact shared down runs. Its
  semantic quotient/cache retains referenced predecessor IDs, actual alias
  intersections, run extrema and exact uncertainty witnesses, lifetime/source
  closure and closed-boundary phase conditions. Proof IDs remain excluded from
  projected authority.
- The supplied implementation response reported full canary 109.005 seconds,
  focused 24.706 seconds and vet passing. Supplied dense-128 search observations
  were 131 states/five semantic predicates, about 5 ms/2.1 MB; chain-128 was
  257 states/about 16 ms/7.8 MB. Those are implementation observations, not
  independently repeated performance guarantees in this review.

Both original DR4 functions and every preexisting function in the final focused
selection pass. The outstanding correctness obligations are the two findings
below. No arbitrary evidence/hop cap or bounded-total-history claim is proposed.

## SR5-1 — P2: closure inference excludes complete embedded closed originals

Repro: `TestPartitionSuccessorReviewEmbeddedClosureFixedPoint`,
`partition_successor_review_test.go:33`, both page orders.

Frozen production locations: `causal_uncertainty.go:85` retains fully hydrated
embedded boundaries; `:92-95` constructs the closure context only from standalone
sources; `:110` derives boundary closure only from that context.

The complete proof is:

- Open original O commits `{a:3,b:3}`.
- Independent complete original C commits the same exact runs, closes with
  `{a:4,b:4}` at time 20, and positively proves O's earlier lifetime closed.
- A opens at 30 with `a:5`; B opens at 30 with `b:5`. Each commits uncertainty
  references to the exact O and C headers using its own positive run witness.
  Both lie after C's complete closure frontier.
- O and C arrive only as embedded headers and their complete separately paged
  facts/links. A receipt names C. There is no unresolved root after completion.

Every page passes the real protocol-8 peer decoder. Both exact embedded roots
hydrate successfully, O/C have an exact shared-run positive edge, and both
later branches pass `runsAfterClosure` against C. Before completion, separation
is correct. After full proof completion, duplicate replay and reopening the
latest private SQLite database, **A and B still remain separate** in both page
orders. Neither imports C's receipt or proof-only aliases.

The control then publishes only C's redundant standalone header. It supplies no
new fact, link, run, or closure metadata. A/B immediately join into one open
lifetime without acquiring C's receipt. C's standalone presence changes which
closed originals participate in the resolver's fixed point.

The new continuity graph cannot substitute for this missing closure resolution:
A/B have later runs, so their path to old O/C is not an exact-run continuity
path. Complete positive closure proof must first turn O's open uncertainty into
the known closed phase. This is distinct from DR4's multi-edge ongoing path and
CR3's proof-arrival invalidation: projection refresh occurs and restart repeats
the same incomplete fixed point.

Required behavior: allow fully resolved embedded closed originals to contribute
positive closure proof, including recursive dependencies, without granting their
IDs standalone alias/receipt authority or bypassing phase/predecessor guards.
This fixture establishes convergence failure, not a separately measured
connected-repeat provider failure for this particular history.

## SR5-2 — P2: captured aliases select the wrong lifetime at final provider I/O

Repros:
`TestPartitionSuccessorReviewQueuedSplitKeepsExactReceiptAuthority:98` and
`TestPartitionSuccessorReviewScheduledWorkerAfterSplit:173` in the new test file.

Frozen production locations: `notifications.go:249-254` unions the captured
projection's aliases with the exact attempt identity, then selects the first
current episode intersecting any captured alias. `:276` checks receipts for that
selected episode. Accepted delivery at `:340` records the original attempt's
incident identity.

Two original open sources initially share a reversible projection. The earlier
source has runs `{a:1,b:1}`; the later source has `{a:3,b:3}` and opens after the
earlier outage's eventual closure. Fresh controlled samples match the later
runs. The actual scheduler creates an old-identity down attempt while its
captured projection still lists both aliases.

Before provider I/O, a validated page supplies the old source's closure frontier
`{a:2,b:2}` and an accepted down receipt for that exact old source/destination.
Projection correctly splits into the old closed and later open lifetimes. The
later lifetime correctly has no receipt. The captured worker then resumes.

If unordered map iteration selects the later sibling through the obsolete
captured alias list, the gate checks the later sibling's empty receipt set and
sends the **already accepted old down** again. The payload retains the old
`incidentId` while listing only the later sibling in `incident.aliases`; its new
receipt is stored under the old attempt identity. The later lifetime still has
no receipt. This is a provider-visible mismatch, not merely a helper result.

The final run observed:

- Direct execution of the exact captured final-gate job sent nine old downs in
  64 checks for the legacy destination and nine in 64 for a named destination.
  Every check should have refused the accepted exact old attempt. A fresh
  post-split scheduler control independently delivers the later down once.
- A separate fixture launches **one real scheduler worker per private node**,
  holds the ordinary state mutex while the late closure/receipt page commits,
  then releases that worker. Six of 64 independent schedules resend the accepted
  old down. No replacement worker function or production hook is used.

Counts vary with Go map iteration. Each observed send is independently invalid:
its exact old down was accepted before the worker acquired the final-gate lock.
These are connected accepted-receipt replays, outside the partition-duplicate
allowance. No false recovery or permanent suppression of the later down was
established by this fixture.

Required behavior: resolve queued work to the current projection containing its
persisted attempt/source identity. Obsolete captured sibling aliases must not
select a different lifetime after a reversible split. Preserve legitimate alias
growth, stable provider idempotency identity and destination receipt checks.
This existing lookup defect is not claimed to have been introduced by the new
successor exception.

## Passing controls and limits

The new exact-path differential control enumerates visited-node subsets without
semantic caches or dominance pruning. Across 192 deterministic six-original
histories and three endpoint pairs each, all **576 comparisons** agree with the
optimized search: 194 positive, 382 negative. Variants include exact interior
witness membership, overlapping aliases, predecessors naming original IDs,
closed frontiers, lifetime times, missing references, and open/closed uncertainty
conditions. This is bounded differential evidence for the quotient/cache;
it is not a proof over arbitrary retained histories.

The new persisted twelve-original embedded-path control passes in both page
orders. Withholding one intermediate run keeps endpoints separate. Completion
converges with only endpoint aliases and no proof receipt. Late closure splits
the mixed inside/after phases; reopening SQLite and a complete passive peer
sweep preserve the split and receipt isolation.

The unchanged successor tests pass for accepted/owed/new destinations, disabled
route restoration and restart, direct queued-worker gates, absent markers,
retirement and maintenance, predecessor repeats/recovery, exact witness
quotienting, dense cycles, long chains, and accumulated predecessor barriers.
Earlier fixtures retain scoped observer/run authority, local-only markers,
atomic rollback/retry, unknown-peer recovery exclusion and connected-repeat
dedup for their covered histories.

No new false recovery, proof-only alias promotion, marker replication, or semantic
cache disagreement was established. The two concrete findings above are separate
from documented capacity limits: incomparable closure/predecessor/uncertainty
barriers can retain many searchable states; total history, CPU, storage, heap and
lock latency remain unbounded. No new capacity defect or polynomial worst-case
bound is claimed. Historical database rollback, membership/identity replacement,
hostile shared-key peers and already-mismerged protocol-4 provenance keep their
prior limits.

The parent subsequently reported a native protocol-8 full-race run with one race
warning and two failing functions, attributed to an unlocked existing test
`evaluate` call and a status-page fixture timeout; its two fixture-only changes
and derived capture `816202e2` were reported as undergoing focused race reruns.
Those parent reports and later push integration are outside this private frozen
capture. Neither fixture was edited here. This review does not certify those
race results, the unknown/raw-latency/tag integration, or whole-release behavior.

## Exact source, writes, and reproducibility

Private frozen source:

```text
C:/Users/Michael/AppData/Local/Temp/uptime-canary-successor-r5-c116b28f21ff448498912b2452cd7801/source
```

The capture contains 197 Go files under `apps/node-go`, `go.mod`, `go.sum`, and
the four requested reports: **203 original inputs**. Each file's shared
before/copy/after SHA-256 matched during capture. All 203 private input hashes
were checked unchanged again after testing. The only added Go file is the new
review test. No Git identity is claimed; the shared checkout has no accessible
Git metadata.

Manifests and final evidence beside `source`:

```text
source-manifest.json       b367d52ad9c415c88e7db3ac36e179587a76a3b4768135f964712a1baa9297a8
production-manifest.json   06649a2037bfec203c7114bbf6c644e6c743687f5a063b1143bf751103e85f3f
final-focused-review.jsonl 451f50add837fdc36e64e80326962a9f1134290c8f168f3c8a436bf040af68cb
```

`production-manifest.json` contains all 101 captured production Go hashes.
Relevant frozen production SHA-256 values:

```text
causal_local.go          a771a34912111b44cb88be51e8f91f7d33f324701d6ea4c2f2fe7e6a3ccc86c4
causal_reconciliation.go fb12938ed5734ff718ee2377609c28e077ef40401bc8d1bd4f5da8a02ce19aac
causal_store.go          5e36d33dfbe2d8fa6c35bc3dd7ed17fa18c2c9883401d91a4a52dabebe7c4ddd
causal_uncertainty.go    36b8376788a0ac0b3df3c5294c9828cca492bda8143cf192fd974f1d669c90d1
incidents.go            00c2cfafc97b9a60ca5649e906e4be5594b75512d6b74e7b1c5f6657e34cd3a3
notifications.go        20e0ee369930a46a999e43c28e0a14e2345a51c0112d7a4d02c7eb6553b764c0
runtime.go              0714d3add87323d261f7f1ab2bca3b0e97842cdf7f3338f4ffd917431ca511ab
wire_pages.go           296a2c53b2638393efd63e2bccfd35739fddf9c4fa12830475b2aba1479e38ac
```

Unchanged original fixtures:

```text
partition_causal_review_test.go   0cd76df64429c1a12f96eeef13071993259672d803bc6a9d9c17b498c527da6e
partition_delivery_review_test.go 290674cf4d0004b08a7f791647d5678f6dacbc16b304e900af1a0abcc5883d2b
partition_delivery_repair_test.go 85f532eb5cd266e33d5761ee924ca7951a18cf0d70a405028e821e16fbd671d2
partition_successor_repair_test.go 8c37e4abd8f47f3564572462ec24ee9f169331394610d5f37009ec2d9b0a017b
```

Using pinned `go version go1.27.3 windows/amd64` from the private source:

```powershell
& 'C:/Users/Michael/Documents/DEV/Uptime-Canary/.canary/tools/go/bin/go.exe' test -mod=readonly ./apps/node-go/internal/canary -run '^Test(Partition|IncompletePeerHistory|CausalCapacityReviewRound2|Queued|RecoveryUses)' -count=1 -json
& 'C:/Users/Michael/Documents/DEV/Uptime-Canary/.canary/tools/go/bin/go.exe' vet -mod=readonly ./apps/node-go/internal/canary
```

The final focused run failed in **19.754 seconds** (package event 19.756):
48 top-level functions pass and three new functions fail for the two findings.
There are 164 passing and seven failing test/subtest events; parent/subtest
events do not represent seven defects. All 46 preexisting functions in this
final selection pass. The earlier narrower original-only selection separately
passed 43 functions in 12.134 seconds. Canary vet passes; formatting is clean.
No full package, race, process/container, or cross-platform release validation
was run by this reviewer.

Final new review-test SHA-256, byte-identical in shared and private copies:

```text
fa5a04940893a02a14f4dbdfc1cb3fb89fbce7a2a98e0f045d2223943ff3ee34
```

`review-validation.json`, `source-recheck.json`, `original-focused.jsonl`,
the initial reproduction/control logs and `vet.txt` retain the observations.
`shared-drift.json` separately records the parent's later changes to 14 captured
paths (11 production files, two fixtures, one report); none was overlaid or
attributed to this reviewer. Locations and results above identify the frozen
input, not the subsequently changing shared integration.

Reviewer shared writes are exactly
`apps/node-go/internal/canary/partition_successor_review_test.go` and this report.
Production/core/shared/modules/UI and earlier reports/fixtures were read only.
New fixtures use private SQLite stores, controlled samples and loopback HTTP
receivers; nodes never `Start`. No user environment/database, default executable,
live provider, development port 3100/5173, commit or nested agent was used.
