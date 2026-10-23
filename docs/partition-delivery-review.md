# Independent partition delivery review, round 4

Reviewed 2026-10-22. **Two new findings remain: one P1 owed-down loss and
one P2 complete-proof convergence failure.** The unchanged round-three review
and all nine delivery-repair functions pass. The two new reviewer functions
assert required behavior and remain failing. This is focused review evidence,
not release approval.

Shared writes are limited to this report and
`apps/node-go/internal/canary/partition_delivery_review_test.go`. Production,
the original reviewer fixtures and the immutable baseline were read only.
Execution used a new private clone, private SQLite stores, controlled probes
and loopback notification receivers. No nodes were started as services; no
environment files, user databases, default executables, live providers,
commits, nested agents or requests to development ports 3100/5173 were used.

The review read `partition-causal-review.md`, `partition-causal-repair.md`,
`notification-review.md`, `capacity-review-round2.md` and `capacity-repair.md`.
The required behavior remains independent delivery during partitions, permitted
partition duplicates, convergence with complete connected evidence, no recovery
from unknown peers, and no old-receipt authority over a genuinely later outage.

## DR4-1 — P1: a successor permanently excludes a marked, unsent predecessor

Repro:
`TestPartitionDeliveryReviewOwedClosedDownSurvivesSuccessor`, at
`partition_delivery_review_test.go:17`.

Frozen root locations:

- `notifications.go:25` computes predecessor supersession;
  `notifications.go:32` skips the predecessor before considering its durable
  intent, local obligation or accepted down receipt.
- `notifications.go:279` independently rejects predecessor work at the final
  provider gate whenever another episode names it as a predecessor.

With notification workers held, real controlled probes observe UP, DOWN, UP.
An assigned peer has fresh UP evidence, so the first outage closes with a
complete all-up frontier. Its exact original-source/destination intent and
local down marker are committed. A subsequent real DOWN creates an independent
successor that names the first episode as its predecessor. Workers are then
released, the latest SQLite database is reopened, and three further probes
advance the clock by an hour each. No historical database rollback occurs.

Both `successor` and `opaque-successor` deliver exactly one new down and
**zero downs for the earlier marked outage**, while retaining the earlier
marker and intent. The opaque variant introduces an unrelated unavailable
header after the first closure; the ordinary successor case also fails. The
otherwise identical `pending-control`, which introduces no successor, delivers
the owed earlier down exactly once. No case emits recovery or repeats while
the peer is stale/unknown.

The marker repairs delivery eligibility, but neither supersession veto asks
whether this destination still has a positively witnessed, unsent down. Thus
reopening, continued probing and elapsed time cannot redeem the retained
obligation. This is distinct from round-three witness replacement: its first
outage now has a complete global closure and a real successor relationship.

Required repair: preserve the exact destination's owed down through both
scheduling and final provider gates when a successor exists. Keep recovery,
repeat, generation, maintenance and dependency rules intact. Do not transfer
the predecessor's receipt to the successor or fabricate a recovery for either.
Changing the scheduling veto alone leaves the provider veto in place.

## DR4-2 — P2: positive continuity stops at one embedded original

Repro: `TestPartitionDeliveryReviewEmbeddedPositivePathConverges`, at
`partition_delivery_review_test.go:111`.

Frozen root locations:

- `causal_reconciliation.go:18` (`positiveContinuity`) accepts a positive direct
  pair, or a path through one resolved embedded original at a time.
- `incidents.go:148` retains projected members, but the fallback at
  `incidents.go:161` invokes that bounded bridge search for each original pair;
  it does not connect multiple proof-only originals.

The complete sources establish the positive path A–O1–O2–B:

- A commits run `a:3` and an uncertainty reference to O1.
- O1 commits `{a:3,c:3}`.
- O2 commits `{c:3,b:3}`.
- B commits run `b:3` and an uncertainty reference to O2.

O1 and O2 arrive through their exact embedded headers and independently paged
facts/links. Every edge has a shared exact down run and passes the actual
positive-continuity predicate. Both visible original sources are complete;
there are no unresolved roots after completion, predecessors, closures or
healthy boundaries that could prohibit convergence. All pages pass the real
peer decoder. The fixture uses three configured static members.

Before evidence completion the two lifetimes correctly remain separate.
After completion, duplicate proof replay and an actual SQLite reopen, the
`standalone=false` case **still has two lifetimes**. In the passing
`standalone=true` control, publishing O1/O2's redundant standalone headers makes
the same already-complete evidence converge to one lifetime. No additional
run or closure proof is supplied by that control.

This is distinct from round-three projection invalidation: proof arrival does
refresh these projections, and restart cannot repair the split. Neither O1
nor O2 alone shares runs with both endpoints, so the new single-original bridge
never recognizes their complete connected path. This fixture establishes the
convergence defect; it does not claim a separately measured connected-repeat
provider reproduction for this longer path.

Required repair: recognize finite positive paths through multiple fully
resolved original witnesses while retaining every aggregate uncertainty-phase,
predecessor and source-closure barrier. Embedded witnesses must remain
proof-only; a path must not manufacture their alias or receipt identities.

## Passing scope and limits

The final focused selection passed all **35 preexisting top-level functions**,
including all ten unchanged round-three reviewer functions, all nine parent
delivery-repair functions, partition/protocol-7 fixtures, interrupted-history
liveness and all six round-two capacity-review functions. Relevant controls
cover destination scope and route restoration, marker non-replication,
peer-only/false-observer claims, fresh pre-marker re-witnessing, history pruning,
strict marker loading, marker/late-source SQL rollback and identical retry,
maintenance and generation holds, negative closed bridges, the one-embedded-
original positive bridge, delayed closures, stale receipts, nested page orders
and unknown-peer recovery exclusion.

Those passing fixtures do not cover the two new interleavings. The review did
not establish a new stale-receipt transfer or SQL atomicity failure. Retained
history, identity/membership replacement, hostile shared-key peers, historical
rollback and already-mismerged protocol-4 histories keep their documented
limits. Parent ICMP unknown-state/status integration is absent from this private
baseline and was not reviewed. No whole canary/stress suite, race pipeline,
container/process proof or integrated release validation was rerun.

## Exact inputs and reproducibility

Private review source:

```text
C:/Users/Michael/AppData/Local/Temp/uptime-canary-delivery-r4-40a910040e5d4a5aad3ef748b473234a/source
```

It was copied from the immutable baseline:

```text
.canary/alpine-validation/windows-race/snapshots/3dffaf5e416337ac317c36c75ae288292bc49a9c7e4ce0b15670c17bbd015467/source
```

Only the five final production files and current
`partition_delivery_repair_test.go`/unchanged `partition_causal_review_test.go`
were overlaid, followed by the new owned review test. The baseline already
contains the exact `needsLocalIncidentDelivery` guard at `runtime.go:631`;
no runtime patch or other parent-current changes were added. All 235 baseline
files were hashed and rechecked unchanged. No private-copy difference outside
the authorized overlay set and the new review file was found.

The private production hashes match the brief:

```text
causal_local.go          D922FDB6E5AAF87A360FA141546775B0687F975B02B431D79678F1EED99FD65F
causal_store.go          5E36D33DFBE2D8FA6C35BC3DD7ED17FA18C2C9883401D91A4A52DABEBE7C4DDD
causal_uncertainty.go    36B8376788A0AC0B3DF3C5294C9828CCA492BDA8143CF192FD974F1D669C90D1
causal_reconciliation.go DF810FFC076CF0F191CC6B9DD8E790F6A9BA7974533B67EE1101CE6CDBAA6870
incidents.go            12168288EAE2905432599962B84C25BA60DD7B303B1B3272222F7AE146E379CF
```

The original round-three fixture retains SHA-256
`0cd76df64429c1a12f96eeef13071993259672d803bc6a9d9c17b498c527da6e`.
The parent repair fixture in the clone has SHA-256
`85f532eb5cd266e33d5761ee924ca7951a18cf0d70a405028e821e16fbd671d2`.
No Git commit is claimed; the shared checkout has no accessible Git metadata.

From the private source, using pinned Go 1.27.3 windows/amd64:

```powershell
& 'C:/Users/Michael/Documents/DEV/Uptime-Canary/.canary/tools/go/bin/go.exe' test -mod=readonly ./apps/node-go/internal/canary -run '^Test(Partition|IncompletePeerHistory|CausalCapacityReviewRound2)' -count=1 -json
```

The final command failed in **13.357 seconds**: 35 top-level functions passed,
and only the two new review functions failed. There are 128 passing and five
failing test/subtest events; the failures comprise three failing subcases and
their two parent functions, not five defects. The new pending and standalone-
header controls pass. `gofmt -l` reports no changes to the new test, which is
byte-identical in the shared checkout and private copy.

Evidence beside the private `source` directory includes
`baseline-manifest.json`, `review-inputs.json`, `source-recheck.json`,
`existing-focused.jsonl`, `new-initial.txt`, `final-focused-review.jsonl` and
`review-validation.json`. The preexisting focused run separately passed in
13.538 seconds; the initial two-function reproduction failed in 3.111 seconds.

Final review-test SHA-256:
`290674cf4d0004b08a7f791647d5678f6dacbc16b304e900af1a0abcc5883d2b`.
Final JSON-event-log SHA-256:
`6d1bd62207225bfedfa861564c8f495b6f51d27091bb6c175fb1a09204c9b3ad`.

The findings and line numbers identify this private frozen input, not the
subsequently changing shared runtime. No production repair or assertion
weakening was performed by this reviewer.
