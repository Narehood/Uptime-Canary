# Five dashboard computation improvements

Five dashboard calculations use at least **25% less elapsed time** on the
specified 1,000-record fixtures, compared with merged PR #35 at
`d78c81537a481f74bea4f648cb68bde4f72584a4`. They cover health assessment,
node coverage, group membership, monitor inventory and incident receipts.
The reductions range from **42.65% to 96.63%** in the Windows measurement.
Hosted Linux execution also clears 25% for each primary workload.
These are calculation benchmarks; they do not measure page loading, rendering,
network requests or human task completion.

PR #35 merged at that baseline commit. Its delivery-log optimization is separate
from these five operations. The user subsequently authorized the security refresh, final validation and
merge; matching-head hosted results remain required before proceeding.

## Measured results

Windows x64, Node **24.22.1**. Each time below is a measured batch median divided
by the batch's iteration count, expressed in **milliseconds per calculation**.
Both implementations receive the same inputs and do all the work described in
the second column, including construction and lookup of new indexes.

| Operation | Work in one calculation | Before ms | After ms | Elapsed time reduction |
| --- | --- | ---: | ---: | ---: |
| Health assessment | Assess 1,000 monitors, each assigned to 8 of 16 nodes | 1.70899 | 0.92743 | **45.73%** |
| Node coverage | Count assigned monitors and fresh results for all 16 nodes across those monitors | 1.21028 | 0.69410 | **42.65%** |
| Group membership | Find children and members for all 100 groups across 1,000 monitors | 0.82826 | 0.02790 | **96.63%** |
| Monitor inventory | Filter 1,000 monitors, calculate all five state counts and available check types | 0.19396 | 0.02549 | **86.86%** |
| Incident receipts | Associate and timestamp-sort 1,000 receipts for 100 displayed incidents | 0.89844 | 0.04309 | **95.20%** |

The primary inventory workload has an empty search and no type or tag scope;
it rotates all five selected state filters. Monitors cycle through the actual
check-type catalogue, health states and tags. Groups have nested parents and
repeated names. Receipts span 100 incidents, three notification kinds and four
destinations, arriving in descending timestamp order like the overview API.

## Hosted Linux confirmation

Linux x64, Node **24.22.1**, using the same workloads. Times are again in
**milliseconds per calculation**, normalized from the recorded batch medians.

| Operation | Before ms | After ms | Elapsed time reduction |
| --- | ---: | ---: | ---: |
| Health assessment | 2.27413 | 1.11275 | **51.07%** |
| Node coverage | 1.24424 | 0.86693 | **30.32%** |
| Group membership | 0.89153 | 0.03359 | **96.23%** |
| Monitor inventory | 0.23475 | 0.03211 | **86.32%** |
| Incident receipts | 1.04217 | 0.05830 | **94.41%** |

[Automation run 42028412477](https://github.com/Narehood/Uptime-Canary/actions/runs/42028412477)
executed these measurements at `7587868`. All six production-file hashes and
the proof hash match the retained Windows run and current files. Its bounded
runner reports an unchanged source tree, successful exit and no storage pause.
[Linux raw measurements](dashboard-efficiency-linux-measurements.json) retain
all samples and the workflow, job and artifact identifiers.

## What changed

**Health assessment** classifies each assigned location in one pass, reading its
first observation once. Previously, three separate filters repeatedly searched
the observations, followed by membership scans to find unknown locations. The
existing wording, severity, pending handling and conflict/paused priority stay
the same. This reduces repeated work without adding a per-monitor cache.

**Node coverage** accumulates counts across monitor assignments once, instead
of filtering all monitors independently for every node card. It counts a
duplicate assignment once and considers any matching non-unknown observation
fresh, preserving the old node-card rule. Only displayed nodes are indexed.

**Group membership** constructs child and member buckets once for the catalogue.
Each group card then reads its own buckets. Child names keep their stable sort,
members keep assessment order, and the index owns the arrays it sorts.

**Monitor inventory** combines filtering and the five counts into one pass.
An empty query no longer constructs tag search strings that would be ignored.
A set of observed check types replaces one full monitor scan per catalogue
entry. Counts still respect search, type and tag scope independently of the
selected state chip; available types still reflect the complete snapshot.

**Incident receipts** groups and sorts receipts once for the Incidents list,
then supplies the appropriate bucket to each row. The Overview and monitor
detail views retain their existing per-row lookup. Timestamp ties retain source
order, and receipt objects are never cloned or changed.

The view indexes also use React memoization with all input identities in their
dependencies. The measured improvements exclude any benefit from reusing a
memoized result. Index arrays/maps add bounded memory proportional to the
current fetched snapshot; no persistent cache is introduced.

## Smaller and selective workloads

The 25% finding is specific to the primary workloads above. Smaller fleets and
search-dominated work do not improve uniformly.

| Alternate workload | Windows reduction | Linux reduction |
| --- | ---: | ---: |
| Health, 50 monitors with 3 assignments each | 0.49% | 8.81% |
| Node coverage, those 50 monitors across 4 nodes | **-2.27%** | **-2.35%** |
| Membership, 10 groups and 50 monitors | 65.25% | 61.47% |
| Inventory, 50 monitors without search | 86.97% | 87.02% |
| Receipts, 10 displayed incidents and 50 receipts | 38.77% | 18.28% |
| Inventory, 1,000 monitors searched for `service` | 4.83% | **-1.48%** |
| Inventory, 1,000 monitors searched for `not-found` | 3.64% | **-2.66%** |

Negative values mean slower. The small node calculation changes from 0.00812ms
to 0.00830ms; index overhead outweighs the reduced scans there. String creation
and matching dominate searched inventories. No search-speed or whole-app speed
claim follows from the primary result. Duplicate-receipt detection was also
investigated, but its initial 14.52% reduction did not meet the target; that
candidate change was removed.

## Reproduction and verification

Run from the repository root:

```text
python scripts/bounded_validation.py --timeout 180 -- node scripts/dashboard-operations-proof.mjs --benchmark
```

The proof contains the original calculations from the pinned baseline and calls
the production helpers directly. It checks **2,806 exact before/after cases**,
plus frozen-input, identity/order, missing-catalogue, timestamp-tie and real-view
server-render checks. The cases cover all state filters, case-insensitive
search, type/tag scope, empty data, missing observations, duplicate assignments
and duplicate observations. Indexed and legacy incident rows render identical
markup. This is static React rendering with private in-memory data, not browser
interaction or an external provider test.

Each workload calibrates its batch size against the baseline to approximately
25ms, then uses the same batch size for both versions. Warm-up alternates order
for 12–40 pairs, requiring a recent four-sample range below 15% and median drift
below 10% relative to the preceding four. Eleven measured pairs alternate order;
the reported reduction compares their medians. All workloads met the warm-up
stability check in both recorded runs. These are `performance.now()` elapsed times, not
process CPU time. Timing varies with runtime, hardware and contention.

[Raw measurements](dashboard-efficiency-measurements.json) retain every warm-up
and measured sample, batch size, runtime, baseline SHA, proof hash and hashes of
all six affected production files. Existing automation contracts, frontend
regressions and web type checking also pass locally. The Linux automation
workflow runs the new equivalence checks and benchmark and retains its bounded
log as `dashboard-operations-evidence`; performance percentages are reported,
not used as a noisy CI pass threshold.

One independent GPT-6.1 Sol review approved the behavior changes and benchmark
method with no actionable findings. The final run above follows a whitespace-only
proof-file cleanup; its arithmetic and source hashes were rechecked by root.

All four hosted workflows succeeded at `7587868`, including container Go, race
and proof checks, all five real automation-browser cases on Linux Chrome, image
builds and the Linux release package. Those browser records report zero real
external sends. The broader Dashboard browser proof job was skipped;
CodeRabbit also skipped this draft. No Opus or Cursor review was requested.
Local execution used no Go application/test executable, user database
or real notifications. These focused frontend results do not establish complete
Go/SQLite HA release readiness.
