# Causal record capacity repair

The working coordinated peer protocol is **12**. It retains the protocol 7
causal representation described below and adds explicit unknown observations,
measurement-validity metadata, and tag/public-projection configuration. Protocol
9 additionally replicates passive heartbeat registers; 10 adds HTTP OAuth2 and
JSONata options, 11 adds group parent links, and 12 adds Globalping options. All reject older peers
before merge. Previously captured protocol 6, 7 and 8 test artifacts remain
evidence for those checkpoints only. Startup also refuses a
database whose recorded coordination schema is newer than this executable,
before identity or migration writes. Upgrade all peers together; do not downgrade.

Protocol 7 removed the cumulative per-record ancestry and incident-evidence
limits demonstrated by [capacity-review.md](capacity-review.md). It does not
raise the 1 MiB request ceiling, 512-record producer page limit, 32 direct-parent
limit, or local configuration admission limits. The parent change separately
admits convergent unions of at most 32 times local admission. Static membership,
stable node identities and retained databases remain assumptions. Protocol 6
introduced the paged representation; protocol 7 adds required immutable
uncertainty facts so interrupted incident synchronization cannot stop local
monitoring indefinitely.

## Revision contract

Monitor, group and resource heads name an immutable `RevisionEdge` by `edgeId`.
The content-addressed edge contains the entity kind and ID, revision, payload
digest, monitor generation where relevant, and at most 32 direct parents. The
old `ancestors` field is a bounded direct-parent compatibility summary for new
heads; it is no longer the transitive closure. Ordinary edits and full conflict
resolutions retain all parent edges, payload digests and tombstones. Channel
configuration stays in the replicated resource domain; accounts, credentials,
sessions, attempts and local observation history do not enter these facts.

Reachability through the retained graph proves domination and administrative
retirement of old check generations. Metadata changes retain generation
continuity. Missing edges cannot prove domination, and a head whose edge or
generation proof is missing cannot execute. Receivers durably retain unresolved
candidates, including more than 32 apparent heads while intermediate links are
missing. Later edges recompute the frontier. Mutations require complete proof
for every observed head. Conflict resolution joins larger complete frontiers
through multiple immutable edges with at most 32 direct parents each, persisted
atomically with the final head; an incomplete frontier must finish synchronizing.
Every remembered revision digest continues to reject payload collisions after
its head has been dominated. Cycles are checked iteratively.

## Incident contract

`IncidentFact` records preserve run membership keyed by monitor, original
incident, generation, node and run, and preserve alias and predecessor relations
as separate facts. `EvidenceLink` records form content-addressed immutable
append chains (and support union links). A summary's `evidenceRoot` commits the
complete set. Appending evidence needs small new facts and links, never a larger
wire record. Facts remain under their original identity after canonical aliases
change. No receipt or notification intent is truncated or compacted.

Persisted and wire incident summaries contain at most 32 aliases, 32 predecessor
IDs and 32 run numbers per node. Those arrays are informational summaries when
an evidence root is present. Complete hydration recovers all committed evidence
before equivalence, closure, active-episode selection or receipt lookup. Missing
links or facts leave a separately stored pending summary. A pending summary
cannot borrow authority from its inline suffix, audience or claimed aliases.
It does not stop probes or history for an independently known valid monitor
configuration. Facts and summaries may arrive in either order and across
interrupted sweeps.

Original complete source summaries and pending summaries are stored separately
from the canonical incident projection. Reconciliation is recomputed from
complete sources rather than feeding inferred projections back into the source
set on every tick. Local updates to an inferred projection retain each original
identity; new run deltas follow the source witnessed by fresh matching runs.
This permits a later closure page to reverse an earlier open-source alias
inference even after local evaluation and restart. The existing round 3 positive predecessor-closure rules in
`causal_reconciliation.go` remain in force. A complete successor can be evaluated
while an older predecessor incident page is absent; a matching fresh run is
still required to extend it. Missing predecessor history never permits an old
receipt to acquire successor authority. Maintenance completion uses the parent's
`maintenanceCombine` reproof after alias intervals expand, and the peer decoder
requires a valid coverage interval for a completion claim.

## Delivery during interrupted incident synchronization

Fresh down observations create or extend a complete local source even when an
arbitrary number of historical incident roots remain unavailable. Each such
source commits separately paged `IncidentFact` records of kind `uncertainty`.
A fact names one opaque header's bounded scope, root and closure metadata,
the observing node, and a positive down-run witness. It imports no historical
alias, audience or run suffix. Every new reference is retained and committed;
there is no timeout, maximum number of pending branches, or inline growing
array on a wire/store incident. A source with a new local run after a healthy
observation receives a new identity even if an unavailable peer prevents
proving global recovery of the previous source.

Only a matching independently witnessed local branch can deliver while history
is interrupted. Captured intent for a brief local outage remains owed if its
local up observation arrives before a worker becomes available. That permits
one down alert; repeat alerts still require fresh down evidence, and recovery
requires fresh up evidence from every assigned node exceeding the branch's
known runs. Unknown peers cannot authorize recovery. Maintenance, generation
retirement, dependency suppression and the final provider-I/O eligibility check
still apply. Old parent receipts cannot extend child suppression through an
opaque boundary. These rules permit duplicate down alerts across independent
nodes and uncertain historical boundaries, preserving delivery-first behavior.

When the missing evidence arrives, a deterministic projection hydrates every
referenced boundary, including nested references, before reconciling sources.
An unresolved reference blocks cross-source alias inference. A complete open
boundary requires the exact recorded run witness in both candidates. A closed
boundary requires positive proof that both candidates fall within its closure,
or both follow its complete closure frontier. The existing causal predecessor
checks still apply. Complete closed original sources may prove a boundary's
closure through a finite monotonic fixed point. Those derived closures and
aliases stay in the projection; they do not rewrite original source facts.
Late closure evidence can therefore reverse an earlier inference after local
evaluation and restart. Receipts remain scoped to their original identity until
this proof establishes equivalence. A permanently unavailable proof can leave
historical sources separate permanently, while new probes, history and locally
witnessed outage delivery continue.

A boundary may name an older committed root of the projected canonical
identity itself. This occurs when a later source referenced the earlier source
before the two were proved equivalent. Equality of their incident IDs neither
completes the root nor grants it authority. Preserving this reference also
keeps the resulting immutable projected facts valid on a later peer sweep.

## Atomic persistence and upgrade

Heads, edges, extracted legacy claims, incident facts, evidence links, original
sources, pending summaries, projections, intents and receipts commit in one
SQLite transaction before acknowledgement or memory publication. SQL indexes
retain parent/child revision lookups and original-incident run fact lookup.
Incoming pages are copied before legacy normalization, so a failed transaction
does not mutate the caller's retry page. Maintenance window caches invalidate
when new causal proof changes readiness, including edge-only pages.

`New` upgrades existing prototype databases automatically. It loads retained
facts and extracts legacy inline ancestry, runs, aliases and predecessors into
the paged domains. Legacy transitive ancestry becomes exact `RevisionClaim`
facts committed by a legacy root; it is not reconstructed as an invented chain
of direct parents. This also preserves old heads with more than 10,000 known
ancestors and incidents with more than 1,000 runs. The extraction, rewritten
bounded root records and schema-version record commit atomically. Failed
extraction leaves the prior root records intact and startup fails. Restart
rehydrates sources and rebuilds the projection and memory indexes from the
committed facts. New-format pending records remain pending across restart.
Only omission of `edgeId` or `evidenceRoot` denotes a legacy self-contained
record. Explicit null, empty, duplicate and case-aliased commitment fields are
rejected by the decoder, including when loading SQLite records. Stored immutable
facts are checked against their content IDs before building the indexes.

This code has been exercised only against private test databases. No running
user database, environment file, default executable or root dependency file was
changed by this repair.

Current protocol 9 rejects every other peer protocol before merging any
configuration or evidence. The required uncertainty fact kind was introduced in
protocol 7 and was not negotiated within protocol 6.
Stop the cluster, back up each node's SQLite database including its WAL state,
upgrade all nodes together, and restart against the retained databases. Mixed
protocol operation is unsupported. Do not downgrade an upgraded database to an
older executable: older code cannot understand the retained fact domains.
An old database snapshot can reuse node run sequences already known to peers
and resurrect stale delivery evidence. Byte-copy rollback is therefore not a
safe recovery procedure. The restore prototype atomically marks its result
`restoreQuarantined` and removes sessions. Immediately after opening a store,
`New` refuses a true or unreadable marker with the sanitized error "Restored
database requires node identity re-enrollment before monitoring", before any
identity or causal migration writes. There is no bypass. The offline
`activate-restore` command now supports the same identity only when its latest
separately retained origin directory is available: it reserves fresh sequences,
forces new check generations, closes old incidents administratively and revokes
sessions before clearing quarantine. See [backups.md](backups.md) for the exact
procedure and validation scope. Losing that directory still requires a fresh
identity; automated re-enrollment and static-membership changes remain gaps.
Direct byte-copy rollback is unsupported. No automatic history deletion occurs.

Protocol 4 histories that were already incorrectly aliased lack the original
source provenance needed to split lifetimes safely. The upgrade preserves their
existing evidence; it cannot identify or invent a repair for those histories.

## Verification and limits

The original real 2,001-probe / 1,001-run test and all three 32-member by
313-edit/full-head-resolution cases use the actual runtime writers, bounded
pages and authenticated loopback HTTP. Representation-aware assertions check
the complete durable/replicated evidence and graph reachability while requiring
bounded summaries. New `TestCausalCapacity*` tests cover delayed edges with more
than 32 candidates, generation retirement, dominated-revision collision replay,
reverse-order evidence pages with 10,001 predecessors and 1,005 runs, aliases,
pending and complete restarts, fact/index/receipt rollback, and atomic extraction
of 10,017 legacy ancestry claims plus 1,001 legacy runs.

Protocol 7 controls in `causal_partition_test.go` additionally cover ongoing
versus later outages, multiple opaque branches, a one-hour partition without a
timer-based escape, latest-database restart, metadata and semantic edits, stale
header replay, maintenance release/completion, child suppression, brief outage
intent retention, header audience isolation, injected SQL rollback/retry, nested
proof chains and both page orders. Complete evidence and receipt projections
are compared after a passive peer sweep. An HTTP control rejects protocol 6
before partial merge. The parent liveness regression uses the actual monitor ID
and check generation.

The earlier protocol 6 checkpoint was validated on 2026-10-22:

```powershell
.canary/tools/go/bin/go.exe test -mod=readonly ./apps/node-go/... -count=1
.canary/tools/go/bin/go.exe vet -mod=readonly ./apps/node-go/...
```

Both passed. The canary package took 100.231 seconds in the full run; checks,
delivery, maintenance and services also passed. This includes the parent-updated
real capacity regressions, all original causal/notification controls, and the
current independent round-two capacity regression file. Its controls cover
commitment null/omission, late closure after local evaluation, exact legacy claim
readiness, immutable scope/cycles, SQL rollback, and complete bounded hydration.
The retained-history paging control committed the full 10,002-receipt union to
both SQLite stores with maximum observed bodies of 258,145 bytes.

A separate source snapshot was checked byte-for-byte before and after capture
and built with `-mod=readonly -trimpath` for Windows amd64, Linux amd64 and Linux
arm64 (Linux builds use `CGO_ENABLED=0`). All succeeded in temporary directories;
the Windows artifact's help command also succeeded without starting a node.
The snapshot runtime manifest has 72 entries and SHA-256
`28ab9882a3b1d0dfdecd1059a03fe47534dce7e4cf8bba6d11aef6b27050e59f`.
Those results and manifest describe protocol 6 only. Protocol 7 validation and
its new source/artifact manifest are recorded separately below. Native
race/process proofs and whole-release signoff are separate parent work.

Protocol 7 testing on 2026-10-22 includes the full canary package and the
partition, corrected parent liveness, round 3, round 2 capacity and delayed
older-boundary controls. The nested proof control was extended to perform a
complete peer sweep; it reproduced a decoder rejection of valid same-identity
older-root references, then passed after that check was corrected. Vet passed
for canary, checks, delivery, maintenance and checkpolicy. The earlier explicit
checks, delivery and maintenance tests passed; separate services reviewer
controls reported Kafka metadata-validation and Oracle cancellation failures,
which remain outside this repair's ownership. These results do not claim a
passing whole-release services suite.

The final immutable capture's full canary run passed in 97.133 seconds, with
339 passing test/subtest events, zero failures and two Windows-only skips for
Unix question-mark filename recovery controls. Its JSON events are retained as
`canary-test.jsonl`, with the commands, artifact hashes and validation scope in
`validation.json` beside the source manifests. The source manifest was checked
again after testing and building.

The protocol 7 capture is
`%TEMP%/uptime-canary-protocol7-4cc20060823340e9859308368d734832`.
Its `runtime.sha256` has 88 entries and SHA-256
`107381d043dd064a26f85555d780a22b79943d4a189b43e24ecba5cd557c7138`;
`source.sha256` includes 164 runtime/module/test files and has SHA-256
`564d211b188340f8e9cc4e9f23a606ff6156d46787cae7ccba358403bfdb3b5c`.
The capture preserves the first byte-verified protocol 7 snapshot, with only
the final causal decoder and its regression test copied from the working tree
and verified before/after copying. All other baseline bytes were checked against
their original manifest. The prior `658b4a86...` capture is superseded; parent
runtime integration had begun separately while this final capture was tested.
Go 1.27.3 builds from this capture passed with `-mod=readonly -trimpath` and
`CGO_ENABLED=0` for Windows amd64, Linux amd64 and Linux arm64. Windows help
also passed without starting a node. Artifact SHA-256 values, in that order:

```text
90a73e2f268a007bf99b6e245559f2afc5eb5414a198b6a00875c4b281dc5f0c
390e5860bb95bfdee54c372544de8a42f019d4731c4d1a74373b7f2eb731418a
c6e46b715705c06f98672922ebc194c862a85b4204b33ad095024ecb41206835
```

This snapshot contains the other owners' origin allocator and probe-worker
packages/CLI dispatch as captured, before parent runtime lifecycle integration.
Those later integrations require their own validation. The earlier protocol 6
native race and Alpine evidence does not validate protocol 7.

Record size and per-request transfer work are bounded; total exact retained
history, heap usage and sweep work are not. The implementation loads retained
facts and an adjacency/run projection into memory. Graph traversal is iterative
and caches queried reachability pairs; irrelevant receipt/fact pages avoid
rehydrating complete incident sources. A history that changes incident evidence
still requires complete source hydration and reconciliation under the node lock.
Nested uncertainty proof resolution adds fixed-point work over retained source
boundaries; it does not provide bounded total-history CPU or heap usage.
Snapshot paging sorts remaining retained record keys. This repair removes the
finite record-size lifetime cliffs, not those total-history cost characteristics.
An explicit membership epoch/acknowledgement and rebootstrap design would be
needed for safe bounded total storage. No fact is silently dropped to simulate
that property, and this work is not whole-release signoff.

## Bounded fan-in for repeated restore branches

The earlier 32-head local-resolution restriction is superseded by
`revision_joins.go`. Repeated restores can create more than 32 genuine
concurrent heads even with static membership of at most 32 nodes. The physical
direct-parent bound remains 32; the number of observed heads to resolve is not
bounded by membership. Existing API request-body limits and exact full-head
selection checks still apply.

`joinMonitorRevision`, `joinGroupRevision` and `joinResourceRevision` return a
final version plus all ordinary immutable edges for one resolution. They check
that the supplied versions exactly match the complete currently observed head
set, reject duplicate or altered versions, require every parent readiness proof,
and require a fresh scoped final revision. Callers hold the node lock, retain
their normal configuration/reference checks and persist the final version with
all returned edges in one `applySnapshot` transaction. They must return the
helper's final payload, including any changed monitor generation. Construction
does not publish configuration or persist records.

Sorted tips are partitioned into a first join of at most 32 tips, then joins
with the previous join plus at most 31 further tips. Sorting chooses batches;
it does not infer an ordering between independent tips. Every observed tip is
retained transitively. Each intermediate revision commits the exact chosen
configuration at that revision. Only the final payload needs a mutable head
record; ordinary edge facts already preserve intermediate payload hashes and
generation reachability. No wire kind or protocol change is needed. Missing
intermediate edges keep the final head non-executable across page permutations
and restarts.

If every live monitor head describes the same check and generation as the
chosen configuration, every join preserves that generation. Otherwise the first
join begins the new chosen check generation, and subsequent joins inherit its
revision as their generation root. This includes restored branches whose check
payloads match but whose forced generation roots differ. For at most 32 heads,
the single-edge metadata, semantic-reset, deletion and new-root behavior remains
the prior contract. Group/resource payloads, including tombstones, are carried
unchanged apart from each intermediate revision.

New `TestRevisionJoins*` controls use 71/100 real independently committed restore
and edit branches, all four entity domains, and enough retained edges to cross
the actual 512-record producer boundary. They check full reachability, exact
intermediate payload digests, generation preservation/reset, final-head-first
delivery, a withheld middle proof through restarts, both actual page orders,
complete edge-set convergence, dominated intermediate payload collisions, and
SQL-injected atomic rollback/retry in every domain. Boundary counts include
0/1/31/32/33/63/64/71/100 heads. Exact vote-set and missing-proof rejection are
also exercised.

The helper-only capture is derived from the immutable protocol 7 `4cc20060...`
source plus the two new files; it does not include parent runtime/origin/restore
integration or new API tests. The focused helper suite passed in 3.684 seconds,
canary vet passed, and isolated Windows amd64/Linux amd64/Linux arm64 builds
passed with `-mod=readonly -trimpath` and `CGO_ENABLED=0`. Windows help passed
without starting a node. Parent callsite/endpoint validation and fresh public
review of the helper remain separate from these results. The original frozen
protocol 7 source and evidence remain unchanged.

The full isolated helper capture passed canary in 95.835 seconds: 367 passing
test/subtest events, zero failures, and the same two Windows-only Unix filename
skips. Evidence is retained in
`%TEMP%/uptime-canary-revision-joins-4bb6d2084cf34a78ac82ec95f2902aea`.
Its 89-entry runtime manifest has SHA-256
`f38112894a37a8f91677965fc8020d523d43374526ab8fa5f703d2d406d6cdcd`;
its 166-entry runtime/module/test manifest has SHA-256
`039951f8ac02b9fedc4535a1e582a7d7323111e51ecba2ca8c5c783d73537d2c`.
The command record, JSON test log, source checks and artifact hashes are in
`validation.json` beside those manifests. This is helper implementation evidence
over the frozen base, rather than validation of the parent's later integrations.
