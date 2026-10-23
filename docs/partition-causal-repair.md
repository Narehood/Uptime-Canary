# Protocol 7 partition delivery repair

The four findings in [partition-causal-review.md](partition-causal-review.md)
are repaired without changing the review fixtures or the peer protocol.
Incomplete historical proof continues to permit independent local monitoring
and down delivery. Unknown peers still cannot prove global recovery.

## Durable local down obligations

The local-only `records` category `localIncidentDeliveries` records an owed
down under the exact `deliveryIntent(monitor, originalSource, destination).ID`.
Its required fields are `monitorId`, `incidentId`, `destinationId`, `revision`
(the check generation), `observerId`, `nodeId`, and `run`.

A marker is captured only while a local writer updates an original complete
source with its audience, the source has the exact destination intent, and
there is a fresh down observation from this node matching a committed source
run in the current generation. Validated peer snapshots retain their sender
identity and cannot capture markers, including legacy rootless incident input.
Claiming this observer's identity or run in peer facts is insufficient.
Startup projection rebuilding also does not infer local provenance.

Markers, source summaries, facts, links, SQL indexes and intents commit in the
same `applySnapshot` transaction. Failure publishes none of those staged
changes. A successful marker remains owed after its down and UP samples have
been replaced, after probe history is pruned, and after reopening the latest
database. Markers never appear in producer snapshot pages.

The marker restores eligibility for one destination's owed **down**. It does
not add aliases, import receipts, prove a closure, or provide the fresh evidence
required for repeats or recovery. Current generation, retirement, route,
maintenance, dependency and final provider gates remain in force. An accepted
receipt deduplicates the owed down through the existing receipt path.

Loading requires exact, non-null fields, rejects duplicate/case-aliased keys,
checks the deterministic record key, and verifies this node, generation, exact
original source run and exact durable intent. Malformed retained authority
fails startup with a sanitized error.

For existing databases without markers, the parent-owned `saveEpisode` no-op
guard now also calls the side-effect-free `needsLocalIncidentDelivery` helper.
A fresh local DOWN for an unchanged original source and audience can therefore
capture its missing marker through the usual transaction. It neither starts a
new lifetime nor requires changing the committed incident evidence. Historical
peer assertions, old raw history and intent alone do not backfill markers. An
already-ended pre-marker source without retained local provenance is not
automatically certified by this upgrade.

## Positive continuity and projection refresh

Reconciliation retains original members while folding the current projection.
If comparing aggregate uncertainty sets loses a valid mixed-observer edge, a
complete original pair can still prove continuity through an exact shared down
run. A complete original carried only by embedded headers can likewise prove
the two positive edges of that path without acquiring projected alias or
receipt identity. Every accumulated predecessor, closure, time and run-frontier
barrier is checked against the combined candidates. Unresolved uncertainty references
still block inference, and closed uncertainty boundaries retain their own
inside/after phase checks across the combined candidates. Ordinary open-record
compatibility without a positive shared run cannot serve as this transitive bridge. The inferred union remains
outside the persisted original source set and can be recomputed when later
closure proof arrives.

New facts or links invalidate projections with retained uncertainty references
even if `pendingIncidents` is empty. This includes boundaries arriving only
inside uncertainty facts and nested references. Idempotent duplicate proof
pages do not require another projection refresh. The existing revision/fact
transaction, graph readiness and maintenance cache invalidation are preserved.

## Verification

The unchanged independent round-three tests pass for owed brief downs after
PENDING/new-DOWN witness replacement, late opaque headers, mixed-observer
continuity, connected repeat dedup and embedded-boundary completion. Their
scoped observer/run, nested page schedules, SQL rollback, restart and restore
quarantine controls remain intact.

The new `partition_delivery_repair_test.go` additionally covers capture before
worker/delay admission, pruned history, destination scope, marker-absent
database reopening and fresh same-run capture, non-replication, remote-only
claims with and without commitments, marker and late-source SQL failure,
identical retry, strict malformed-marker loading, positive continuity barriers,
embedded open-original continuity, embedded closed-frontier receipt isolation,
route disable/restore, maintenance hold/release, semantic retirement and absent
false repeats/recovery. All databases and provider receivers are private test
resources; receivers use loopback. No running services or user database were
modified.

Commands use `.canary/tools/go/bin/go.exe` and `-mod=readonly`:

```powershell
go test -mod=readonly ./apps/node-go/internal/canary -run '^Test(Partition|Causal|IncompletePeerHistory|Review|Maintenance|Queued|RecoveryUses)' -count=1
go test -mod=readonly ./apps/node-go/internal/canary -count=1
go vet -mod=readonly ./apps/node-go/internal/canary
```

Final shared-checkout validation on 2026-10-22 passed: the combined focused
selection in 26.865 seconds, the full canary package in 109.392 seconds, and
canary vet. `gofmt -l` reported no changes. The independent partition review
fixture retains SHA-256
`0cd76df64429c1a12f96eeef13071993259672d803bc6a9d9c17b498c527da6e`.
These results do not identify a frozen release artifact: other owners are
concurrently integrating runtime and revision joins.

## Retained limits

Markers add one retained local record per positively witnessed original
source/destination obligation. They are not deleted after delivery and do not
bound total SQLite or heap growth. Marker capture copies the local marker map
when adding a new obligation; startup loads and validates retained markers.
Existing source hydration, pairwise reconciliation and paged sweep costs still
grow with retained history. Preserving original members adds work when checking
positive continuity between aggregates; it is not a bounded-total-history
algorithm.

Partition duplicate downs remain permitted. Complete connected proof and
exchanged receipts deduplicate subsequent work through the reconciled lifetime.
Crash-after-provider-acceptance duplication remains possible. Historical
database rollback, identity/membership replacement, hostile shared-key members
and already-mismerged protocol-4 histories retain their prior limits. Race,
process/container and whole-release verification belong to the parent.

## Follow-up: successors and complete embedded paths (current protocol 8)

The independent [partition delivery review](partition-delivery-review.md)
identified two additional interleavings after the earlier protocol-7 repair.
Its unchanged tests reproduced both failures before this follow-up. The parent
has since advanced the application to protocol 8 for measurement-validity
semantics; the causal repair itself adds no new wire or local schema.

Both notification scheduling and the final provider gate previously discarded
any predecessor once a successor existed. They now preserve a closed source's
exact destination obligation only when its local marker and intent remain and
that destination has no accepted down receipt. The exception permits **down**
only. Successors still veto predecessor repeat and recovery. Generation,
retirement, current route, maintenance, dependency, rank/deadline, queued alias
and receipt checks retain their ordinary gates. Adding a destination after
closure cannot create an old marker or replay that outage. An unavailable route
keeps its existing obligation and can resume when the same route returns.

Continuity now searches exact-run edges over the complete retained original
and recursively resolved embedded evidence graph. It can prove paths through
multiple embedded originals without promoting their proof-only IDs to aliases
or receipt authority. Each explored union includes both endpoint projections
and the actual proof path's constraints. It must pass all accumulated source
closure, predecessor and closed-uncertainty phase barriers. A sequence of
individually positive edges does not excuse a mixed intermediate crossing an
earlier closure. New late closure proof can still split an earlier projection;
original source records remain intact.

The graph is constructed lazily on the exceptional continuity fallback and
indexes shared exact runs. Search visitation uses a causal constraint quotient,
not the set of all traversed proof IDs. Its state retains run extrema, exact
uncertainty-witness membership, opening/resolution times, closure frontiers,
uncertainty phase obligations, predecessors, and every alias ID actually named
by a retained predecessor. Audience, roots, observer labels and unreferenced
proof IDs cannot change those aggregate barriers. Equal causal classes share
edge and union-transition work, while exact alias intersection is retained for
the original-pair predicate. A weaker contained run range dominates its
superset only when every other barrier and exact witness membership agrees.
Genuinely different authority states remain searchable. No evidence ceiling,
hop limit, timeout or blind connected-component merge was added.

The new `partition_successor_repair_test.go` covers two-boundary completion,
withheld intermediate proof, replay/restart, late-closure reversal and receipt
isolation, a positive edge chain that must fail its accumulated predecessor
barrier, multi-destination acceptance and route restore, direct queued-worker
gates, exact witness quotienting, and dense-cycle/long-chain scaling.

The disconnected dense controls explore the entire same-run component, rather
than succeeding on a convenient short path. Sizes 8, 32 and 96 retain linear
causal-state counts. One-iteration benchmark observations on the shared Windows
host after semantic transition caching were approximately 0.18/1.17/4.99 ms for
dense sizes 16/64/128 and 1.11/5.95/16.16 ms for the corresponding chains. The
128-node dense search retained 131 distinct visited causal states, evaluated
five distinct semantic edge predicates and allocated about 2.1 MB in total;
the chain visited 257 states and allocated about 7.8 MB. These are sampled
search costs, excluding graph construction, and are not latency or peak-heap
guarantees. The benchmark command uses the pinned toolchain:

```powershell
go test -mod=readonly ./apps/node-go/internal/canary -run '^$' -bench '^BenchmarkPartitionSuccessorRepairPositiveGraph$' -benchtime=1x -benchmem
```

This removes irrelevant path-subset enumeration for ordinary dense ongoing
outages. Distinct incomparable closure/predecessor/uncertainty constraints can
still require many states. Indexing follows retained exact run memberships;
dense neighbor enumeration can still be quadratic, with deterministic sorting
and per-constraint work. Independent incomparable barriers can have a large
antichain, so no polynomial worst-case bound for all retained histories is
claimed. Total retained history, CPU and heap are not bounded by this repair.
Reconciliation still runs under the existing node lock, so
this is not a hard lock-latency guarantee for arbitrary retained histories.
Incomplete historical proof remains independent of a local source's down
authority, and no failed search retracts its local marker or intent.

Final current-protocol-8 validation on 2026-10-22 passed: the broad
partition/causal/review/notification/queued/recovery/liveness/maintenance/intent
selection in 24.706 seconds, the full canary package in 109.005 seconds, and
canary vet. Formatting is clean. The two-function independent delivery review
fixture remains SHA-256
`290674cf4d0004b08a7f791647d5678f6dacbc16b304e900af1a0abcc5883d2b`;
the earlier partition and marker fixtures also retain their original hashes.
Earlier protocol-7 validation and frozen review evidence remain historical
inputs, not current protocol-8 process/container signoff.

## Follow-up: embedded closure dependencies and queued identity (protocol 9)

The independent [round-five successor review](partition-successor-review.md)
reproduced two more failures: complete embedded closed originals did not enter
closure inference, and a queued worker could select the wrong lifetime through
obsolete captured aliases after a reversible split. All three unchanged
reproduction functions failed before this repair on the current protocol-9
tree. Protocol 9 is the parent's current integration; these two repairs change
neither wire representation nor durable schema and add no runtime hook.

`resolveUncertaintySet` now includes complete embedded originals in its closure
context alongside complete standalone sources. Each fixed-point round refreshes
that deterministic context, so one derived embedded closure can provide another
original's positive predecessor proof. Convergence detection includes the
private uncertainty identities as well as serialized incident fields. Scope,
frontier, lifetime, predecessor and uncertainty-phase checks remain required.
The derived bindings stay in proof memory: their IDs do not become projected
aliases or receipt identities, and original source facts are not rewritten.
Older-root semantic dependency cycles settle through the same finite retained
unions; missing committed facts remain missing proof.

The final notification gate now selects only the current projection containing
the queued attempt's stable `incidentId`, within its monitor and generation.
Its captured incident snapshot supplies no sibling lookup authority. Legitimate
alias growth still resolves that exact original ID; after a split, another
sibling's empty receipt set cannot cause an accepted old down to be resent,
and an unrelated sibling's receipt cannot silence the later owed attempt.
Provider idempotency and receipt identity remain the persisted attempt identity.
Current route, destination acceptance, generation, maintenance, dependency,
successor, repeat and recovery checks retain their existing gates. An absent
exact source cannot fall back to a formerly captured sibling.

Five functions in `partition_closure_identity_repair_test.go` cover recursive
embedded closure/predecessor dependencies, a withheld exact seed fact, SQL
rollback and identical retry, both page orders, restart and reverse peer replay,
foreign monitor/generation and unresolved predecessor barriers, semantic
older-root cycles, and original-source provenance. Worker controls cover legacy
and named destinations, exact versus sibling/different-destination receipts,
alias growth followed by a split, stable provider keys, and absence of the exact
source. The independent 576-case quotient differential and twelve-original
persisted-path controls are preserved unchanged.

This expands fixed-point context work to the retained embedded proof set;
retained-history CPU, heap, storage and lock latency remain unbounded as already
documented. No evidence cap, timeout, blanket merge or recovery shortcut is
introduced. Final current-protocol-9 validation on 2026-10-22 passed: the combined
new controls and unchanged successor review in 2.813 seconds, the broad
partition/causal/review/notification/queued/recovery/liveness/maintenance/intent
selection in 25.517 seconds, the full canary package in 110.945 seconds, and
canary vet. Formatting is clean. The unchanged independent successor review
fixture remains SHA-256
`fa5a04940893a02a14f4dbdfc1cb3fb89fbce7a2a98e0f045d2223943ff3ee34`;
all earlier independent partition and delivery fixture hashes are preserved.
This validates the shared source captured by those commands, not a release
artifact. Historical protocol-8 race/process captures do not certify the
changing current integration; this follow-up did not run race or process tests.

## Follow-up: standalone predecessor closure in mixed proof (protocol 9)

The independent [closure and identity review](closure-identity-review.md)
reproduced a remaining P2: a complete standalone predecessor Q was present in
the resolver context but never received a derived closure from standalone seed
S. Ordinary projection closed Q/S afterwards, too late for dependent embedded
O/C boundaries. Both page orders of the unchanged standalone-predecessor
reproduction failed before this repair; embedding an otherwise unnecessary Q
header was the review's passing control.

`resolveUncertaintySet` now makes separate proof-memory copies of complete
standalone sources and includes those copies as closure fixed-point targets
alongside complete embedded originals. A derived standalone closure can thus
satisfy another original's predecessor frontier, which can enable the next
embedded closure. Every round refreshes context deterministically and retains
the existing affirmative equivalence, scope, frontier, lifetime, predecessor
and uncertainty-phase checks. Returned source records are distinct from these
proof copies: derived aliases, closure and delivery authority are not persisted
as original facts. The final-send identity gate is unchanged.

`closure_standalone_repair_test.go` adds an alternating dependency chain: seed
closes standalone-only Q; embedded C closes standalone-only R in proof memory;
R permits embedded K to close embedded O; later A/B then converge. Q and R
are never named by embedded headers. Seed is tested both standalone and
embedded, in both page orders. Withholding one exact seed run prevents the
derivation. An injected SQL projection failure leaves memory and retry input
unchanged, and reopening before retry verifies durable rollback. Identical
completion, replay, a second reopen and a reverse passive peer sweep converge
without importing proof aliases, closures, receipts or intents into the later
lifetime. Original sources retain their own aliases and original open state.

Eight complete-proof negatives retain foreign-monitor/generation, uncovered
seed run, late predecessor closure, missing middle predecessor, mixed old/new
middle runs, early top lifetime and closed-uncertainty phase barriers. The
unchanged reviewer cycle, queued-follow-up identity and all prior causal
fixtures remain part of validation.

With no complete embedded boundary, standalone proof-copy inference is skipped;
ordinary projection continues to handle standalone lifetimes. Otherwise each
fixed-point round considers the retained standalone and embedded proof set,
with quadratic candidate-pair work plus predicate/union costs and the required
number of rounds. This repair adds no cap or timeout. Retained history, CPU,
heap, storage and node-lock latency remain unbounded; no polynomial bound over
arbitrary histories or release-level race/process signoff is claimed.

Current-protocol-9 validation on 2026-10-22 used pinned Go 1.27.3 with
`-mod=readonly`. The unchanged closure/identity and successor review selection
plus the prior closure repair controls passed in 6.245 seconds. The final new
mixed-proof controls passed in 3.383 seconds, and the broad
closure/partition/causal/review/notification/queued/recovery/liveness/maintenance/
intent selection passed in 32.534 seconds. Canary vet passed and formatting is
clean. The unchanged `closure_identity_review_test.go` remains SHA-256
`ebf5e0892d430a0a7a1b704554bf9b8c428e029697abf9ef70932f5ed4835ecb`;
the previous independent fixtures and final-send production file also retain
their recorded hashes.

The full shared canary run failed in 147.294 seconds, reporting only two
concurrent push-review functions: `TestPushFinalReviewAdmissionAcrossPassiveLifecycle`
(disabled/unready/conflict restart and disabled live-prune admission cases) and
`TestPushFinalReviewIndexLateSQLFailureAndEdgePublication` (unready-head token
reservation). No closure/causal failure was reported. Those push files are
outside this repair's write scope and were left untouched; this is not a full
suite pass. No race, process or release artifact validation was run by this
implementation follow-up. The parent received the exact failures for its
separate push integration.
