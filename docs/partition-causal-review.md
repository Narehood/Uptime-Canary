# Independent causal partition review, round 3

Reviewed 2026-10-22 against the immutable protocol 7 candidate, independently of
the parent runtime/origin/check-policy integration. **Four current findings
remain: two P1 owed-alert losses and two P2 convergence/dedup defects.** The new
regressions assert required behavior and deliberately remain failing. This is
not release approval.

Reviewer writes are limited to this report,
`apps/node-go/internal/canary/partition_causal_review_test.go`, and a private
temporary validation copy. The frozen candidate, core/modules/UI, user
databases, environment files and default executables were read only. No commits,
subagents, external services/messages, host trust/DNS changes, or running node
services were used. Probe callbacks are controlled, SQLite stores are private,
and notification receivers use loopback HTTP. No request targets the user's
port 3100. The three capacity reports, existing causal/partition fixtures, and
causal/runtime/group/delivery implementation were read as review context.

## CR3-1 — P1: replacing the latest UP witness loses a captured brief down

Frozen locations: `causal_uncertainty.go:217` (`endedLocalBranch`), `:230`
(`owedLocalBranchDown`), and `:236` (`deliveryEpisodeEligible`), plus their
scheduling and final-provider gates in `notifications.go`.

Repro: `TestPartitionCausalReviewOwedBriefDownSurvivesWitnessReplacement`, both
`pending` and `down` subcases.

1. Receive a valid original header while its exact facts/links remain
   unavailable. The opaque commitment is generated with `prepareIncident`, not
   an arbitrary malformed root. The assigned peer supplies no observations.
2. Hold notification worker capacity. Real local probes report UP, DOWN, UP,
   then either PENDING or a new DOWN run. The first DOWN captures a durable
   intent and complete local uncertainty branch.
3. Release workers, reopen the latest SQLite database, and execute three more
   real probes, advancing the controlled clock by an hour each time. No
   historical proof or peer measurement arrives.

Both subcases retain all seven local history entries and the first branch's
intent, but deliver **zero** downs for that brief branch. PENDING produces no
notification at all. The new-DOWN case delivers the newer branch's down while
permanently excluding the earlier owed one for as long as that run continues.
Neither case invents a recovery or repeat.

The owed gate re-derives the branch's ending solely from the latest fresh
sample. Replacing its UP sample with PENDING removes that ending from the
gate's input. Replacing it with DOWN also selects the newer witness in
`deliveryEpisodeEligible`, excluding the older branch before its captured intent
can be considered. Continued probes and elapsed time do not recover the missing
authority; the historical UP exists in raw history but is not consulted here.

Required behavior: retain complete local evidence that a captured brief
outage's down remains owed across subsequent measurements/runs and restart.
Deliver each owed down once without borrowing any opaque alias, audience or
receipt. A later healthy boundary must still produce an independent new
outage; repeat and recovery eligibility must retain their current evidence
requirements. Restoring the old outage's down does not authorize a global
recovery while a peer is unknown.

## CR3-2 — P1: a late opaque header suppresses a previously completed owed outage

Frozen location: `causal_uncertainty.go:236` (`deliveryEpisodeEligible`).

Repro: `TestPartitionCausalReviewLateOpaqueHeaderPreservesAlreadyOwedDown`.

With worker capacity held, the node observes a local DOWN then UP while its
assigned peer has fresh UP evidence. The ordinary complete local source closes
with a valid all-up frontier and retains its owed down intent. This predates any
interrupted history, so it has no uncertainty fact.

After closure, inject an unrelated historical opaque header for the same monitor
and generation. Its remaining pages never arrive. Release workers, reopen the
latest database, and continue local UP probes over three logical hours.
The opaque case delivers **zero** notifications despite retaining the complete
closed source and its intent. The otherwise identical **no-header control
delivers exactly one owed down**. Recovery remains blocked when the peer's
measurement becomes stale in both histories.

`interruptedHistory` makes the new header affect every source in this monitor's
generation. The completed preexisting source cannot satisfy `localBranch` or
`endedLocalBranch`, since both require uncertainty facts that it never needed.
The header thereby revokes delivery eligibility from independent complete local
history. `evaluateInterrupted` does not backfill authority for this already
completed source, and normal UP probes cannot resolve the opaque root.

Required behavior: receiving opaque historical state cannot invalidate an
already captured down backed by complete locally witnessed closure and intent.
Preserve that source's local provenance across restart. Do not implement this as
an unconditional exception for arbitrary old peer sources or their receipts.

## CR3-3 — P2: complete mixed-observer continuity cannot deduplicate an ongoing outage

Frozen locations: `causal_uncertainty.go:131` (`uncertaintiesCompatible`) and
`incidents.go:33,115` (`causallyEquivalent`, `reconcileEpisodes`).

Repros: `TestPartitionCausalReviewMixedObserverContinuityConverges/ongoing` and
`TestPartitionCausalReviewMixedObserverConnectedRepeatDedup`.

The exact original ongoing source O commits runs `{a:3,b:3}`. Independent
observer A's branch commits `{a:3}` with an A/a:3 uncertainty witness against O;
observer B's branch commits `{b:3}` with a B/b:3 witness against the same root.
Before completion both branches correctly remain separate. Once O's exact
evidence arrives, O has a direct positive continuity witness to each branch.
Nevertheless, repeated complete proof replays leave two lifetimes:

```text
{O, A}    {B}
```

The first greedy union carries A's uncertainty into the aggregate. Comparing
that aggregate with B now requires B itself to contain A's run, although O
already provides the complete bridge between the two original sources. The
choice of which branch joins O can depend on the branches' ordering; the
remaining split is the defect.

The runtime repro creates both observers' uncertainty branches using actual
probes and independent partition down delivery. It then completes O, performs
multiple full bounded validated sweeps of all facts/sources/receipts, refreshes
the real probes, and serializes repeat delivery: A sends slot 1, all its current
receipts are applied to B, then B evaluates. **Both observers send a slot-1
repeat**, retaining two lifetimes despite complete proof and receipt exchange.
This is connected behavior after reconciliation, beyond the duplicates allowed
while partitioned. It is not a demonstrated missed-down or stale-closure defect.

Completed-boundary and genuinely later-outage subcases pass: positive closure
converges the former, and the latter stays separate from its predecessor.
Required behavior: preserve affirmative continuity through complete original
sources when reconciling multiple observers, while keeping all closure and
predecessor barriers. Unconditional transitive aliasing or using absent closure
as proof would reintroduce the earlier stale-receipt defect.

## CR3-4 — P2: completing an embedded boundary does not refresh the live projection

Frozen location: `causal_store.go:441` (`applySnapshot` projection invalidation).

Repro: `TestPartitionCausalReviewEmbeddedBoundaryCompletionReprojects`, both
page-order subcases.

Two complete observer sources reference an older closed boundary through their
committed uncertainty facts. The header is embedded in those facts; its
standalone Incident record has not arrived. Both sources are complete and
`pendingIncidents` is empty, but their referenced boundary proof is incomplete,
so the initial separate lifetimes are correct.

Send every remaining boundary fact/link on independent validated pages,
duplicating every page. `hydrateIncident` now confirms the exact root is
complete. The live projection still has **two** lifetimes. Reopening the same
committed database immediately produces **one**, in both forward and reverse
page orders.

`projected` is invalidated by incoming incidents, initial loading, or proof
pages while `len(pendingIncidents) > 0`. An incomplete reference inside an
otherwise complete source is absent from that pending map. Its fact/link pages
therefore never invoke `resolveUncertaintySet`. A later incident update or
restart can incidentally repair the projection; completion alone does not.

Required behavior: new facts/links that complete a referenced uncertainty
boundary must re-evaluate the dependent sources even without a standalone
pending Incident. Idempotent duplicate pages, a missing later header, or a quiet
ongoing outage must not make correctness depend on process restart.

## Passing controls and limits

New independent passing controls:

- `ObserverAndRunScope`: matching observer/run can retain its own success;
  a foreign observer or different run cannot silence the independently owed
  local branch or transfer the foreign receipt.
- `MixedNestedPageSchedules`: eight deterministic shuffled schedules, direct
  and nested references from multiple observers, separately paged facts/links,
  duplicate pages, a mid-sweep SQLite reopen, and one withheld exact older run.
  Partial proof transfers no old receipt or aliases. Completion joins the
  correct middle lifetime, preserves its closure, and leaves both its old
  predecessor and completed later outage independent. A complete reverse
  producer-page sweep reproduces the result at a passive peer.
- `CompletionRollbackAndRetry`: injected late `incidentSources` SQL failure
  leaves source/pending/projection/intent state and the caller's input unchanged.
  Restart retains the incomplete boundary; the identical proof page retries and
  reproduces at a peer without conflating old/new lifetimes.
- `PreparedRestoreRemainsQuarantined`: the supported private restore output
  cannot start under its former identity; `New` returns the re-enrollment gate.
  This is not a claim that arbitrary byte-copy database rollback is repaired.

The preexisting partition/protocol-7 tests, corrected interrupted-history
liveness test, and all six round-two capacity-review functions pass in the
combined focused run. This includes the parent's 40-opaque-root fixture,
brief-UP control, nested full sweep, maintenance, child-dependency fail-open,
strict commitments, source-local delta, and earlier SQL rollback controls.
Those passes do not cover the additional failing interleavings above.

No core fix, ceiling change, assertion weakening or migration was performed by
this reviewer. No current stale-receipt transfer was demonstrated by the passing
partial/nested/scoped fixtures; their finite schedules are not a general safety
proof. Retained-history CPU/heap/storage costs, hostile shared-key members,
identity/membership replacement, historical database rollback, already merged
protocol-4 histories, and the parent's new lifecycle/services integration remain
outside this review's guarantees. Old protocol-6 race/process/Alpine results do
not sign off protocol 7. The whole capacity suite, race pipeline and containers
were not rerun.

## Exact candidate and reproducibility

Frozen candidate:

```text
C:/Users/Michael/AppData/Local/Temp/uptime-canary-protocol7-4cc20060823340e9859308368d734832
runtime.sha256 (88 entries):
107381d043dd064a26f85555d780a22b79943d4a189b43e24ecba5cd557c7138
source.sha256 (164 entries):
564d211b188340f8e9cc4e9f23a606ff6156d46787cae7ccba358403bfdb3b5c
```

Every manifest-listed source file was checked against both the frozen source
and private copy before testing. The frozen baseline is preserved; only the new
review test is overlaid into the private copy. The final source recheck and
focused result are recorded below. All 164 original source files matched again
in both trees after the final run (328 file-hash checks), and both manifest
hashes still matched the brief. There is no accessible Git metadata in the
shared checkout, so no commit identity is claimed.

Private validation copy:

```text
C:/Users/Michael/AppData/Local/Temp/uptime-canary-cr3-review-26f6d35b246a49e988a6dd86c27574e2/source
```

From that copy, using `go version go1.27.3 windows/amd64`:

```powershell
& 'C:/Users/Michael/Documents/DEV/Uptime-Canary/.canary/tools/go/bin/go.exe' test -mod=readonly ./apps/node-go/internal/canary -run '^Test(PartitionCausalReview|Partition|IncompletePeerHistory|CausalCapacityReviewRound2)' -count=1 -json
```

The final command **failed in 11.410 seconds**: 21 top-level functions passed
and five new functions failed for the four findings (CR3-3 has separate
structural and real-delivery repros). The event log has 85 passing test/subtest
events and 11 failing test/subtest events; those counts include parent functions
and are not 11 distinct defects. All failures belong to this new review file.

The final event log is `final-focused-review.jsonl` beside the private `source`
directory; `review-validation.json` records the selection, hashes and counts.
Log SHA-256:

```text
51362cc28c9c9eb377f05743e288881468ec561c328423a718e4e0170833be26
```

The new reviewer test is byte-identical in the shared workspace and private
copy; `gofmt -l` reports no formatting changes. Its SHA-256:

```text
0cd76df64429c1a12f96eeef13071993259672d803bc6a9d9c17b498c527da6e
```

These results describe the frozen causal baseline with only this test added.
They do not describe the subsequently changing shared runtime or certify the
final integrated release.
