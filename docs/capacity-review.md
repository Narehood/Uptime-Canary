# Independent core capacity and convergence review

Reviewed 2026-10-22 against the shared Go implementation, including the parent's
protocol 5 maintenance integration. Reviewer writes are limited to this document
and `apps/node-go/internal/canary/capacity_review_test.go`. Core, module, UI,
proof, environment and user database files were not changed. No commits,
subagents, live notifications, or requests to the user's port 3100 were used.
This review does not repeat notification rounds 1–3 in
[notification-review.md](notification-review.md).

The current implementation has three independently reproduced **P1 convergence
failures** and one **P2 inability to resolve otherwise valid configuration**.
P1 here means an honest supported local mutation can prevent peers from
accepting the resulting state. The tests deliberately assert the required
behavior and remain failing pending parent remediation. Passing ordinary HA
controls do not prove convergence for all valid local histories.

## C1 — P1: local admission caps reject valid partition unions

Locations: `api.go` local monitor/group admission; `channels.go` channel
admission; `wire.go:mergeVersions`, `mergeGroupVersions`;
`resources.go:mergeResources`; `maintenance.go:maintenancePlansFor`.

The same aggregate cap is used for local admission and replicated union. Two
partitions can each accept valid local creates without exceeding that cap,
then reject each other's valid state forever after reconnect:

- Monitors: 190 shared IDs, then 10 creates on each partition; each node has
  200 IDs, and the required union has 210. Both directions return HTTP 409
  `Monitor capacity exceeded`.
- Groups: the same 190 + 10 + 10 construction returns HTTP 409
  `Group capacity exceeded`.
- Notification channels: 502 shared IDs, then 10 creates on each partition;
  each node has 512, and the required union has 522. Both directions return
  HTTP 409 `Resource capacity exceeded`.
- Maintenance: 54 shared IDs, then 10 creates on each partition; each node has
  64, and the required union has 74. Both directions return HTTP 409
  `Maintenance capacity exceeded`. All fixtures share one valid schedule, so
  this is an ID-count rejection even when compiled-plan memory is very small.

Repro: `TestCapacityReviewPartitionCreatesConvergePastLocalAdmissionLimits`.
Every create goes through authenticated `ServeHTTP`, including input parsing,
reference checks and SQLite persistence. The common initial configuration
replicates successfully. Partition writes are then made without exchanges.
Every reconnect page comes from `snapshotPage`, passes the receiving node's
actual protocol decoder, respects the 512-record/1-MiB bounds, and is sent
through authenticated real loopback HTTP. Three identical sweep attempts in
each direction fail. The production `exchangePage` path also retains its
cursor on rejection.

Deleting a common object on both nodes succeeds but does not remove either
retained ID. Reconnect still returns 409. SQLite and memory retain 200/200,
200/200, 512/512 and 64/64 IDs respectively. A deletion cannot repair the
cardinality invariant, and restarting with the same retained data cannot
reduce the required union. There is no background compaction to make a retry
eventually succeed. A page can contain other observations, receipts or incident
evidence that is rejected along with configuration; pagination alone does not
make that page acknowledgeable.

## C2 — P1: actual local probes create an incident rejected at 1,001 runs

Locations: `runtime.go:tick`, `evaluate`, `saveEpisode`;
`incidents.go:addRuns`; `wire.go:validateSnapshot` run-array limit of 1,000.

One node remains down while the other alternates down/up. The persistent down
evidence keeps a single incident open, while each newly started down run adds
another sequence to that incident. Neither local evaluation nor persistence
enforces a 1,000-run limit. The peer decoder does.

Repro: `TestCapacityReviewLocallyAccumulatedRunsRemainReplicable` completes
2,001 real B probes through `tick` and its normal SQLite transaction, generating
1,001 distinct B down runs. Two real A probes supply persistent down evidence.
The nodes use a controlled clock, supported 60-second sync interval, and
100-ms monitor interval. A's observation is refreshed halfway through the
roughly 200 seconds of logical time, so it stays fresh throughout. Probe
results are controlled callbacks; observations and incidents are produced by
the runtime, not inserted as invented database rows. Sync is explicitly driven
at the two A refresh points; no external check or notification is performed.

At 1,000 B runs the emitted page validates. At 1,001 B runs the incident remains
open and durable, but its roughly 5,697-byte page fails the decoder and the
authenticated peer endpoint returns HTTP 400 `Invalid peer snapshot`. The
failure is far below the byte and page-record budgets. Closing the outage
would retain the same run array; deleting or semantically changing its monitor
also retains the historical incident, so those actions do not remove the
invalid record from subsequent sweeps.

## C3 — P1: successful monitor/group resolution emits invalid ancestry

Locations: `runtime.go:writeMonitor`, `writeGroup`;
`api.go` full-head resolution; `wire.go:validateSnapshot` ancestor limit of
10,000; `wire_pages.go:snapshotPage` individual-record handling.

Repro: `TestCapacityReviewConcurrentEditAncestryRemainsResolvableAndReplicable`
uses exactly 32 statically configured members and one shared root object.
Each member makes 313 sequential metadata edits through the actual writer,
using that member's own current heads on every edit. The only input change is
a short valid Name. There are no fabricated ancestors or stale local writes.
The root is created through the authenticated management API; root and branch
tips replicate through authenticated bounded real HTTP pages.

All 32 branch tips are independently valid and merge as 32 retained heads,
within the supported conflict bound. A full-head authenticated resolution names
every current revision exactly once. Both the monitor and group API return
HTTP 200 and durably store the resolution with
`1 + 32 * 313 = 10,017` ancestors. The resulting single-record page is about
391 KB, within the 1-MiB ceiling, but fails the peer decoder. The peer endpoint
returns HTTP 400 `Invalid peer snapshot`.

This reaches the failure with shallow valid branches and only root/branch
replication exchanges, rather than 10,000 network rounds or a fabricated
10,000-entry ancestry array. Further edits/deletion continue to carry the
ancestor union. The soft 256-KiB page target correctly allows a larger single
record; it cannot repair the independent 10,000-ID schema mismatch. Removing
only that schema limit would eventually encounter the hard single-record byte
limit instead.

## C4 — P2: channel conflicts can become impossible to resolve

Locations: `resources.go:writeResource`, `validateResource`;
`channels.go` full-head resolution.

The channel subcase of the C3 test creates the identical valid 32-branch DAG.
All 32 heads replicate and remain retained. Its valid complete-head resolution
returns HTTP 400 `Invalid replicated resource`, because `writeResource`
validates the 10,017-ancestor candidate before persistence.

This is deliberately classified separately from C3: the channel writer does
**not** persist or emit a peer-invalid resolution. Replication of the existing
conflicted heads remains possible, but the supported resolution operation is
unavailable. An ordinary DELETE also requires a unique current head, so it
cannot resolve this state. Resource heads are not pruned. An equivalent serial
resource reaches a finite edit/delete lifetime when adding the next ancestor
would exceed 10,000. Maintenance uses this same resource writer; the executed
32-branch fixture covers channels, not an additional maintenance DAG run.

## Other retained arrays and the scope of the evidence

The peer schema also limits aliases and predecessors to 10,000 IDs.
`evaluate` includes every closed same-generation episode's IDs when opening a
successor; `combine` unions predecessor and alias sets. Those arrays are not
split into independently paged facts. Source inspection therefore identifies
another lifetime constraint: after 10,001 distinct completed same-generation
outages, opening the next one constructs at least 10,001 predecessor IDs.
This full 10,001-outage history was not executed: it also produces growing
copies of predecessor history and exercises expensive reconciliation. It is
not presented as another measured threshold or an independently demonstrated
infinite-alert-silence finding. No honest-history fixture crossing the alias
cap was established; seeing an unbounded union in `combine` alone does not
prove that arbitrary alias arrays are reachable under 32-member semantics.

C1–C3 establish rejection of honest producers and stalled durable acceptance.
They do not establish that every local alert stops: delivery-first partition
behavior may continue local delivery, with missed remote configuration or
duplicate sends while receipts cannot catch up. No new infinite alert silence
was independently demonstrated here. The causal silence issue already covered
by notification round 3 belongs to that review and its separate repair.

Independently of those correctness failures, keeping receipts, intents,
incidents, DAG ancestry, aliases, predecessors and tombstones forever grows
SQLite and heap use. `snapshotPage` collects and sorts remaining record keys
for each page; incident reconciliation and delivery work inspect retained
history. Bounded request bodies and four pages per round bound transmission
and scheduling opportunities, not retained memory, total catch-up work or
lock-hold time. Increasing counts/byte limits changes thresholds; it does not
create a bounded lifetime retention design.

## Design alternatives and the parent's proposed admission repair

**Separate local admission from the convergent replica union.** Keep the
original local admission caps and continue counting all retained IDs,
including tombstones. For honest static membership of at most 32 nodes, each
node can originate at most its local cap over its lifetime: its retained-ID
count never falls, and learned IDs only reduce its remaining admission. The
union is therefore at most 32 times that cap. A replica safety bound of 6,400
monitor IDs, 6,400 group IDs and 16,384 total resource IDs admits all such
histories. Maintenance is a subset of resources, with at most 2,048 IDs under
a separate 64-per-node admission rule. The parent's proposed repair is sound
under these assumptions. A smaller bound based on identical configured member
counts is possible if membership consistency is enforced.

Keep local maintenance admission separate too: changing only the peer
`maintenancePlansFor` limit must not accidentally allow a node to originate
2,048 maintenance IDs. Preserve immutable revisions, tombstones, complete-head
resolution, transaction-before-acknowledgement and cursor retry semantics.
Do not deterministically discard IDs to force the union back to 200/512/64.
The bound assumes retained databases and stable membership; resetting a
database/identity or replenishing creation rights requires an explicit
membership/bootstrap protocol. A hard bound on malformed authenticated excess
does not substitute for an origin/quota design if hostile members are in scope.

**Budget maintenance execution separately from replicated configuration.**
One compiled Plan retains at most 65,536 windows of two int64 values, or 1 MiB
of window backing storage; this is distinct from the 1-MiB HTTP page limit.
Schedules, rather than the compiled window arrays, are replicated. The cache
deduplicates identical schedules and skips deleted/conflicted resources, but
up to 2,048 distinct unconflicted schedules could retain roughly **2 GiB** of
window backing storage, plus node state, compilation scratch and transient
maps/arrays. Disabled unconflicted schedules are also compiled by the current
cache builder. The old nominal 64-plan maximum was about 64 MiB. The C1 test
uses one shared schedule and does not allocate this worst case.

Choose an explicit supported memory/admin budget, a globally allocated plan
budget, or bounded execution storage/cache with defined evaluation behavior.
Do not acknowledge a maintenance resource while silently dropping its hold
semantics. An execution admission gate that simply reintroduces permanent peer
merge rejection would recreate C1. Fixed global quotas or escrow rights can
enforce a strict cluster-wide 64-plan ceiling, but partition-local creation is
then restricted by preallocated rights rather than granting every node 64
independent slots. Review maintenance-ID summaries and decoder bounds together
when increasing the replica bound; current summaries are truncated to 64.

**Replace cumulative ancestor arrays with paged revision-edge facts.** The
parent's proposed durable direction is credible: retain one immutable record
keyed by entity kind/ID and revision, containing at most 32 direct parent
revisions, check generation where applicable, and a payload digest. Local
writes already name all current heads, so 32 parents suffice for the supported
honest static-member write model. Keep current head payloads and tombstones;
old payload bodies need not be retained if the digest still detects a replay
with the same revision and different immutable content. Persist the new head
and its edge fact atomically in SQLite before accepting the local write.

Domination and retirement then require proven graph reachability, rather than
membership in an ever-growing UUID array. Store incoming edge/head facts
durably in any page order and index parents/children so later links can resolve
previously unknown relationships. Missing links leave candidates unresolved;
they cannot justify discarding a candidate, retiring an incident generation or
authorizing recovery. Once the causal path arrives, the same dominance rules
remove stale candidates while retaining the edge evidence for delayed peers.
Generation continuity must remain independent of metadata-only revision changes.

One implementation trap: missing links can temporarily make **more than 32
stored candidates appear incomparable**, even when the true eventual frontier
has at most 32 heads. Do not reintroduce a valid-page rejection by applying the
32-head bound before ancestry is available. Separate durably stored unresolved
candidates from the proven execution frontier and defined conflict behavior.
SQLite reachability indexes and bounded incremental work are needed so an
unbounded history is not recursively loaded/traversed under the node lock on
every page or evaluation tick. Edge paging removes the record-size cliff;
it does not by itself bound total retained edges or traversal cost.

**Page run evidence separately from the incident summary.** Immutable facts
keyed by `(monitor, original incident, node, run)` can retain every locally
observed down run while the incident carries only a bounded inline summary.
Include/check the incident generation. Index the original incident identity
through current aliases so a later canonical-ID change neither hides facts nor
changes their keys. Closure checks, old-run continuity, equivalence and receipt
lookup must consult the complete fact index, not just the inline summary. An
incident update and its new local run facts must commit atomically. A receiver
can durably acknowledge an independently valid fact or summary before the
other arrives, but missing evidence cannot transfer a successful old receipt
to a successor or authorize a premature recovery.

Aliases and predecessor relations also need individually paged evidence or a
proven bounded causal frontier; keeping their current 10,000-ID arrays merely
moves the next lifetime cliff. Retain the original causal records needed to
reassess ambiguous relationships: an irreversible alias union inferred before
its predecessor evidence arrives repeats the round 3 defect even if the union
is split across small records. Use separate record domains/cursors and a
coordinated protocol upgrade, including out-of-order pages, interrupted commits,
restart, late old runs/receipts, metadata edits and tombstone replay in review.

A causal version representation based on persisted origin counters and a
bounded context per static member is another DAG alternative, provided it
preserves conflict detection, transitive dominance, immutable revision checks
and restart safety. These are protocol changes, not requests to silently
truncate arrays. Merely rejecting the 1,001st run or 10,001st mutation avoids
emitting an invalid record but creates a finite monitoring/configuration
lifetime instead.

If bounded *total* retention is required, use an explicit stable membership
epoch/acknowledgement scheme for safe compaction, with a rebootstrap path for
peers older than the retained causal boundary. Arbitrarily delayed peers,
unlimited mutation history, retained exact evidence and fixed total storage
cannot all be promised by the current representation.

## Verification

Bundled toolchain; actual private in-memory SQLite stores and loopback HTTP:

```powershell
.canary/tools/go/bin/go.exe test ./apps/node-go/internal/canary -run '^TestCapacityReview' -count=1 -v
```

The regression file follows `peerProtocol` rather than hard-coding a version.
`TestCapacityReviewOldProtocolsRejectedBeforeConfigurationMerge` explicitly
rejects every earlier protocol before any configuration is merged, then accepts
the current-protocol control.

The four C1 subcases initially failed in 0.50 seconds. The actual-probe C2
fixture failed in 2.52 seconds. The three C3/C4 DAG subcases failed in 7.11
seconds: monitor/group returned 200 then emitted invalid pages; channel
resolution returned 400 before persistence. Durations are observations of the
review snapshot, not performance promises.

The final combined reviewer run against the pre-remediation baseline again
reproduced all four C1 cases, C2, and the monitor/group/channel DAG cases. The
old-protocol rejection/current-protocol acceptance control passed. Baseline
evidence is frozen here before the parent's aggregate-capacity-only repair;
that repair does not claim to address C2–C4. No further 32-by-313 branch reruns
are required for this handoff.

Focused pre-existing controls were also run:

```powershell
.canary/tools/go/bin/go.exe test ./apps/node-go/internal/canary -run '^Test(ThreeHTTPNodesSuccessiveOutages|PreferredSenderDeathAndProviderIsolation|PartitionReconcilesAliasesAndRecovery|UnknownAssignedNodeCannotAuthorizeRecovery|PagedRealPeersRetainHistoryAndSynchronizeNewState|ConcurrentEpisodesAndCompletedPartitions|DelayedSiblingDoesNotAbsorbLaterOutage)$' -count=1 -v
```

All passed together (15.660 seconds): connected successive-outage dedup,
preferred-sender death and provider isolation, delivery-first partition
alias/recovery convergence, recovery's unknown-node guard, episode controls,
and real bounded peer paging with retained history. The paging control started
with 5,000 receipts per node, retained the 10,000-receipt union, and synchronized
later edits/receipts in 53 HTTP exchanges; largest observed request/response
were 315,749/315,672 bytes. These existing controls use their existing fixtures;
they do not override the honest-local-mutation counterexamples above.

This reviewer did not run the entire repository, race detector, compiled-process
proof suite, container/Alpine proofs, live delivery, or UI checks. Parent core
repairs and final integrated verification remain separate work.
