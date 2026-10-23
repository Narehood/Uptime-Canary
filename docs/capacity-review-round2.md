# Independent causal capacity review, round 2

Reviewed 2026-10-22 in the shared Windows checkout using the portable Go
toolchain. Reviewer ownership is limited to this document and
`apps/node-go/internal/canary/causal_capacity_review_round2_test.go`. Core,
modules, UI and prior fixtures were read only for this reviewer. No commits,
subagents, user databases, environment files, default executable changes, live
external notifications or requests to port 3100 were used. SQLite databases
are private; notification receivers are loopback only; new reviewer nodes are
never started as services.

This was a concurrent review of an implementation still being finalized.
Three issues were independently reproduced during the review and the
implementation agent subsequently changed their respective paths. Current
focused regressions pass. The original failures remain below with their
observed fixes, rather than being presented as current defects. This report
does not certify the whole release or unbounded operation.

The checkout has no accessible Git metadata (`git rev-parse HEAD` reports
`not a git repository`). File hashes identify the examined implementations;
test results must not be attributed to an invented commit SHA.

## CR2-1: malformed commitment becomes legacy authority (P2, repaired)

Original locations: `wire.go:validateSnapshot`; `upgradeConfiguration` and
`hydrateIncident`. The typed string fields treated JSON null like an omitted
field. Empty `EdgeID` selected legacy configuration extraction and empty
`EvidenceRoot` selected self-contained incident admission.

`TestCausalCapacityReviewRound2NullCommitmentCannotBecomeLegacyAuthority`
established all four cases through authenticated `POST /internal/snapshot`:
monitor, group, channel resource and incident. With a syntactically valid,
nonempty commitment but no proof, HTTP 200 durably retains the record without
making it executable. Replacing the commitment with null instead returned
HTTP 200 **and made the record executable**. The explicit omitted-field
legacy control returned 200 and executed as intended.

This is malformed authenticated input acceptance, not an unauthenticated
injection or evidence that the honest current producer emits null. A holder
of the shared cluster key can already submit self-contained legacy records
and other peer assertions; this change does not turn that trust boundary into
Byzantine fault tolerance. The actionable defect was letting malformed
commitment syntax select a different authority path.

Observed repair: `decodeCommittedRecord` and the custom `UnmarshalJSON`
methods distinguish true omission from null/empty commitments, reject case
aliases and duplicate commitment keys, and apply that distinction to durable
loads as well as peer decoding. Current authenticated controls reject null,
empty strings, uppercase null/empty fields, and a valid exact-spelling field
followed by an uppercase empty override. The exact omitted legacy control
continues to pass. The latter variants were added after repair and are current
passing controls; only canonical null was executed against the original
implementation. Required intent destinations retain their separately tested
strict decoder, including legitimate explicit `destinationId:""`.

## CR2-2: local evaluation makes an inferred alias irreversible (P1, repaired)

Original locations: `runtime.go:saveEpisode` and
`causal_store.go:sourceProjection`. Source records were retained during peer
merge, but `saveEpisode` cleared the root on an updated canonical projection
and submitted the entire combined projection as a new original source.

`TestCausalCapacityReviewRound2LateClosureReversesEvaluatedProjection` uses two
original open records for one generation: old `a-prior` with run `a:1`, and
newer `c-current` with run `b:3`. Neither source knows a predecessor boundary.
Those open records can temporarily alias while the old closure is delayed.
There is a successful old down receipt naming `a-prior`.

The receiver then observes fresh `a:3` and `b:3` down samples. Local evaluation
extends the inferred projection with the new local run. Only afterward does
the old source's closure arrive, with frontier `{a:2,b:2}` and a resolution
before the newer opening. That frontier cannot close the newer runs.

Before repair, the persisted `a-prior` source had acquired the inferred
`c-current` alias and both newer runs. The delayed closure then produced a
closed projection containing `c-current` and the old receipt, even after an
actual SQLite reopen. The identical history without the intervening local
evaluation correctly separated the old closed source and the newer open
source. Thus source preservation during peer merge alone was insufficient;
ordinary local evaluation destroyed the reversibility it was meant to retain.

The reproduced failure establishes wrong lifetime/closure and old-receipt
authority after complete evidence arrives. It is not presented as a measured
infinite-silence result: later evaluation can create another episode for fresh
runs, which does not restore the original newer source's correct identity.

Observed repair: `causal_local.go:localIncidentUpdates` preserves original
source identities, selects the source witnessed by current fresh runs, and
records local deltas there instead of certifying inferred cross-source aliases.
The current regression checks old-receipt isolation and open newer identity
after restart. It also exercises real loopback delivery: both the interleaved
case and its control subsequently deliver exactly one owed newer down and no
recovery. This extends the original structural reproduction with a passing
delivery check after repair.

## CR2-3: an incomplete legacy ancestry commitment executes (P2, repaired)

Original locations: `revisionGraph.matches`, `groupVersionReady`, and
`resourceVersionReady`. A matching head and edge could execute while the
edge's legacy root still lacked its independently paged exact ancestry claims
and links. This was reproduced for a group and active maintenance resource,
including after restart. The maintenance resource actually advertised an
active window without the remaining commitment evidence.

The envelope is the normal output of `legacyRevision`: a bounded head, an edge
with `LegacyRoot`, and exact `RevisionClaim`/`EvidenceLink` facts delivered
later. It does not invent a digest collision or a direct-parent chain from a
transitive legacy ancestor list. This ordering is possible during normal
paginated legacy-history catch-up. The initial monitor attempt had a fixture
URL-normalization mismatch and is excluded from pre-repair evidence.

Observed repair: the revision graph tracks complete evidence and propagates
readiness through direct parents; `matches` requires the edge to be complete.
`TestCausalCapacityReviewRound2LegacyCommitmentWaitsForExactClaims` now passes
for monitor, group and maintenance, before and after restart. Supplying the
final exact claims/links enables the configuration at the same logical time,
including invalidating the previously evaluated maintenance cache. These are
current monitor controls, not a claimed original monitor reproduction.

## Prior findings, responses and remaining objections

The original brief is an independent Go/SQLite HA runtime for at most 32 static
members: immutable conflict DAGs and tombstones, exact fresh revision samples,
check-generation lifecycle, staggered connected delivery with receipt dedup,
failover and delivery-first partition behavior, causal outage aliases and
predecessors, recovery/repeats, recipient-specific intent and parent-group
fail-open behavior, and local raw history/accounts. This review reads the prior
[capacity](capacity-review.md), [notification](notification-review.md), and
[maintenance](maintenance-review.md) evidence in that context.

- **C1, partition unions exceeding local caps:** separate replica bounds now
  admit up to `32 * localCap`; local management admission remains 200 monitors,
  200 groups, 512 total resources and 64 maintenance IDs. The four original
  partition-union subcases pass in this review. Admission and merge maps count
  retained IDs, including tombstones and learned IDs. Maintenance counts its
  kind within the shared resource namespace and also consumes total resource
  admission. Channel and maintenance keys are kind-scoped. Ordinary deletion
  does not replenish the retained-ID budget.
- **C2, locally accumulated 1,001 runs becoming peer-invalid:** the original
  real 2,001-probe regression passes. Wire/durable summaries are bounded while
  the complete hydrated run set remains available. New reviewer controls also
  withhold one original-source fact across real page boundaries and restart;
  no inline suffix acquires authority, and all older runs survive completion.
- **C3/C4, 10,017 ancestors emitted or a complete channel resolution rejected:**
  writers now create immutable edges with at most 32 direct parents, payload
  digests, kind/entity/revision scope and generation. Legacy ancestry migrates
  to exact claims. The new smaller scope/collision/cycle tests and the existing
  greater-than-32-unresolved-candidate regression pass. The 32-member by
  313-edit monitor/group/channel benchmark was deliberately **not rerun** by
  this reviewer. Its historical failure is not reclassified as an independently
  repeated large-case pass here.
- **Notification rounds 1-3:** the historical test-batch admission, cumulative
  audience size, route identity, closed-outage replay, inactive retry status,
  long destination IDs, lost owed intent during temporary/unordered channel
  availability, interrupted test status, delayed predecessor aliasing, and
  missing/null intent destination regressions pass in the adjacent rerun.
  Separately paged intent rows preserve the complete audience. CR2-2 above was
  an additional local-projection interleaving beyond the prior frontier repair.
- **M1-M3:** the original earlier-opening completion, observed group-release
  coverage, and queued-worker release-stagger tests pass. Current code re-proves
  completion after alias expansion, preserves release evidence against a false
  continuous-coverage claim, and rechecks the current release/rank deadline at
  the final provider gate. The new legacy-maintenance test independently
  verifies proof-arrival cache invalidation.

The 32-times-capacity argument is sound **only** for at most 32 honest static
members retaining their admission-counted databases and identities. Each node
can originate at most its local cap over its retained lifetime; replicated IDs
only reduce its remaining admission. It is not an origin-authentication or
escrow protocol. Unknown sender IDs are rejected and placements/run-node facts
use configured known members, but a configured shared-key peer is trusted to
make assertions about other members. Database resets, replacement identities,
membership expansion and hostile peers require a separate model; the replica
cap does not prove those histories bounded.

The remaining operating and release objections are explicit:

- Paged records bound individual messages, not retained SQLite/heap size,
  complete-source hydration/reconciliation under the lock, graph cache size,
  page-key sorting or total catch-up time. This repair is not bounded total
  storage. Large distinct maintenance plan sets retain the separate memory
  concern documented in the first capacity review; no worst-case plan-memory
  workload was allocated here.
- A pending incident commitment intentionally suspends its check generation
  through `current()` and evaluation until hydration succeeds. This includes
  consumers used for local checking and final delivery gating. Missing a
  complete predecessor *record* is different: complete newer-source tests
  continue evaluating without it. This review does not prove a time bound on
  resuming if the remaining commitment facts are unavailable after a partition,
  and does not certify unconditional delivery-first liveness in that condition.
- Automatic extraction cannot split already incorrect protocol-4 aliases
  without original source provenance. Preserve the documented coordinated
  upgrade gate: stop/backup every node including WAL state, upgrade all peers
  together, and do not downgrade a migrated database. An atomic representation
  migration is not a semantic repair of previously mismerged history.
- No claim is made that all repository Go packages, the final whole canary
  package, race detection, Alpine/container/process proofs or a running user
  database migration passed in this review. The parent still owns integrated
  release validation and any stronger availability claim.

## Independent passing controls

`ImmutablePayloadScopeAndCycleChecks` checks both head-first and edge-first
arrival of mismatching immutable payloads, valid subsequent proof, cross-entity
edge rejection, monitor/group/resource reuse of identical bare IDs/revisions,
second edge identity for one scoped revision, a cycle completed by a later page,
and cross-monitor incident-fact scope. Rejections publish neither staged maps
nor the SQL revision index. Content commitments detect altered immutable
content; they do not authenticate a peer or defend against a SHA-256 break.

`FailedSQLLeavesInputAndIndexesRetryable` injects independent failures in
`revision_parents`, `incident_fact_index`, and a late `incidentSources` write.
Every failure rolls back records, revision indexes, fact indexes, projection,
intent/index publication, and the caller's exact legacy input remains
unchanged. Retrying that same decoded object succeeds; reopening and replaying
it preserves the committed identities and intent authority.

`PendingPagesHydrateAllFactsAndStayBounded` uses 230 runs, 45 aliases and 60
predecessors, crossing actual 512-record pages. It delivers the summary first,
restarts, reverses the remaining pages and withholds one original-source run
fact. No episode executes until that final fact arrives. Complete hydration
then retains every fact, including the oldest run; SQL fact-index cardinality
matches the fact store; a second restart retains all roots and projections.
The fixture accounts for canonical alias ordering and distinguishes
original-source facts from projection-generated facts when withholding proof.
This is a bounded synthetic evidence test, not another executed 10,001-outage
lifetime or a performance benchmark.

## Commands and snapshot identity

Portable focused review:

```powershell
.canary/tools/go/bin/go.exe test ./apps/node-go/internal/canary -run '^TestCausalCapacityReviewRound2' -count=1 -v
```

An initial combined run reproduced CR2-1 and CR2-2 in 2.218s. The initial
legacy-commitment run reproduced group/maintenance execution before complete
proof. A later six-function focused run passed in 2.881s after concurrent
repairs. The subsequently expanded malformed-field controls passed, and the
expanded projection/real-delivery control passed in 4.058s. Durations are
observations, not latency guarantees.

Adjacent controls, excluding the expensive 32-by-313 ancestry fixture:

```powershell
.canary/tools/go/bin/go.exe test ./apps/node-go/internal/canary -run '^Test(Causal|Review|CapacityReviewPartition|CapacityReviewLocallyAccumulated|CapacityReviewOldProtocols|Maintenance|PagedIntent|DeliveryIntent|MonitorConflicts|ConcurrentDelete|NamedGroups|AuthenticationStateStaysOut|ThreeHTTPNodesSuccessiveOutages|PreferredSenderDeath|PartitionReconciles|UnknownAssignedNode)' -count=1
```

The final combined command **passed in 67.010s**, including all six new review
functions and their expanded controls. Earlier adjacent capacity/notification/
lifecycle/maintenance controls passed in 18.034s. Shared implementation files
changed during review; early failing and later passing invocations are distinct
snapshots, and this reviewer did not fix core or weaken older tests. The final
combined run leaves no independently reproduced current failure in these
reviewed paths; the operating/release objections above remain.

The following SHA-256 hashes matched before and after the final combined run
(observed at 14:15:31 and 14:18:44 UTC):

```text
causal_facts.go      C966267AC04FB74ED535DB08EEF7D9FFDF8B45FF74F454844E08CA4FA5671AE0
causal_store.go      32627D9D9B48E65ADA7258BAFC30DB2ED87C662981ECEC99D248AC9DB7423497
causal_validation.go 9C4B6E0E9C8D925EC56E11135A52AD6C43449E607B610BBAC2F053CA04A94471
causal_local.go      C25C00B0418FC9E6DFCAAD0DBAB82330C016BECCD08BDD2F9962A89B3462CF27
wire.go              F94A23ED3BE4754D73C849021C9D40A100AEAF8498DFECFE04ACB1D7EC157A7B
runtime.go           6E6225467596A366EACA3AD8A0985A5DBC88034F16A9C25862565515E4B73210
lifecycle.go         18E62C32383B11D258E586D394CB6571B359216903E9A15903960EA214D96E28
maintenance.go       65DA75137DA8F5F280895F48CFE5411384F95D79F9CBBAC511F6D5FBA5E5863E
```

For the original failures, the initial `causal_facts.go` hash was
`A3A44A7449BA4A0DA6FB4B1A9B4E8AB1C9B2B79017D0BE39BCA681211CAFBBAA`,
`runtime.go` was
`EAB6E9BC7F94D99DCCAA46816C3858217A8CABE99E00CA624DAA72C7BE037627`,
and `wire.go` was
`B83BA1E12A9AD613B75EDAA196CB344CD66EEE2602523FB867A3802502133DF0`.
These identify the relevant pre-repair paths, not an atomic whole-tree
snapshot. `gofmt -l` reports no formatting changes for the new review test.
