# Independent closure and identity review, round 6

Reviewed 2026-10-22 against a byte-checked private capture of current protocol 9.
**One independently reproduced P2 remains:** a closure derivable from complete
standalone originals is unavailable to the embedded-boundary fixed point when
that predecessor is not itself embedded. The three unchanged SR5 reproduction
functions pass. The new required-behavior reproduction remains failing. This is
focused causal review evidence, with no whole-release signoff.

The read-only brief and prior responses are in `partition-successor-review.md`,
`partition-delivery-review.md`, `partition-causal-review.md`, and
`partition-causal-repair.md`. The contract still requires independent local
delivery during partitions, permits partition duplicate downs, requires
convergence after complete connected positive proof, excludes recovery from
unknown peers, and prevents old receipts from authorizing or suppressing later
outages. Embedded proof identities must not become projected aliases, delivery
intents, or receipt identities merely by supplying proof.

CR3's exact local source/destination/run markers and reversible original-source
projections, DR4's owed predecessor down exception and multi-original continuity
search, and the latest SR5 repair were treated as the candidate under review.
The last repair includes complete embedded originals in the deterministic
closure context, refreshes that context every round, includes private uncertainty
IDs in change detection, and selects final work by the persisted attempt's exact
incident identity. The supplied production hashes match this capture exactly.

**CIR6-1 — P2: standalone-only predecessor closure is never derived into the
embedded resolver context.**

Reproduction: `TestClosureIdentityReviewStandalonePredecessorClosureFixedPoint`,
`apps/node-go/internal/canary/closure_identity_review_test.go:236`.
Both `embeddedQ=false` page orders fail at line 311, independently on the
reopened private database and its passive peer. Both `embeddedQ=true` controls
pass. All pages use the real current peer decoder.

The complete positive history is:

- Standalone open Q has runs `{a:1,b:1}` and opens at time 0. Standalone seed S
  has the same runs, closes with `{a:2,b:2}` at time 5, and independently proves
  Q's closure. The ordinary projection correctly joins Q/S and is closed.
- Embedded O has runs `{a:3,b:3}` and opens at time 10. Embedded closed C has
  those same runs, closes with `{a:4,b:4}` at time 20, and names Q as predecessor.
  Both O and C have complete exact headers, facts and links.
- Later A and B open at time 30 with `a:5` and `b:5`, respectively. Each commits
  uncertainty references to O and C with its own run-5 witness. Complete Q/S
  closure permits O/C's equivalence across Q's predecessor frontier; C then
  proves O closed, putting A/B after the same known closed boundaries.

No committed fact or link remains missing, and no incident remains pending.
The fixture separately verifies Q/S's direct positive equivalence and O/C's
equivalence with their positively derived closed predecessor. It also verifies
that the actual Q/S projection is closed. Nevertheless, after duplicate proof
replay, reopening the latest SQLite database, and a complete reverse producer
page sweep into another private node, A and B remain separate.

The control additionally embeds Q's already available header in C's uncertainty
facts, using C's run-3 witness. It introduces an additional uncertainty fact and
its commitment links; it supplies no new Q/S/O/C run or closure metadata. With
that extra reference, the resolver derives Q's closure and A/B converge in both
page orders, with no predecessor or proof aliases, intents, or receipts imported.

Frozen production cause: `causal_uncertainty.go:108` copies standalone `sources`
into each context, but `:115` only iterates and updates `boundaries`. With no
uncertainty header naming Q, Q never receives a proof-memory `combine(Q,S)`.
`predecessorEpisode` at `causal_reconciliation.go:312` therefore cannot find a
closed record containing Q in that resolver context. O cannot acquire C's
closure, and O remains an open uncertainty for the later observers. The ordinary
projection computes Q/S's closure only afterwards, at `causal_store.go:147`,
following uncertainty resolution at `:131`; it does not feed that result back
into the dependent boundary fixed point.

This is a missing derived context member, despite inclusion of every original
as an initial context input. It is separate from SR5's missing complete embedded
closed original: C is present in the context here. It is also separate from
projection invalidation, because replay, restart, and peer reconstruction all
repeat the split. The reproduction establishes convergence failure; it does not
measure duplicate provider repeats or a missed recovery for this history.

Required behavior is to make affirmative closure derivations over complete
standalone originals available to dependent embedded boundaries, including
standalone-only predecessor IDs, while preserving original source provenance
and all scope, phase, frontier, and predecessor barriers. Derived proof must
remain separate from projected proof-only alias, intent, and receipt authority.
No production repair, blind merge, or evidence/hop limit is proposed here.

**Proven controls on this capture.**

The unchanged selection passes all five SR5 functions, including its original
three repro functions, the 576-case exact-path/cache differential, and the
twelve-original persisted path with late closure. The original CR3/DR4 fixtures,
all five latest closure/identity repair functions, and the selected prior
partition, capacity, marker, notification, maintenance, queued-worker and
recovery controls also pass. Those retain the covered local-only marker,
destination/run/observer scope, SQL rollback/retry, missing predecessor, foreign
monitor/generation, owed predecessor down, route restoration, reversible split,
and unknown-peer recovery protections.

The new `MultiRootCycleWithheldSeed` function at line 18 passes six deterministic
shuffled page schedules. Three current originals form a semantic cycle through
older roots; both older and current roots of one identity are referenced at
once. Withholding one exact seed run prevents closure authority and later-branch
convergence. Completion settles all referenced boundaries, joins the later
observers, remains stable on duplicate replay, and survives database reopen and
a reverse passive peer sweep. Seed receipt authority stays isolated and all
persisted originals retain their own aliases and original open state. These
observations demonstrate termination and isolation for these finite schedules.

The new `QueuedFollowupsAfterAliasGrowthAndSplit` function at line 122 passes
16 cases: repeat/recovery, legacy/named destinations, and old-sibling, exact,
other-destination or unknown-peer conditions. The real scheduler persists each
follow-up under the identity of its accepted down before worker admission.
Subsequent alias growth changes the canonical ID, and a late closure splits
that captured projection. Repeated direct executions of the actual final-send
method, followed by another execution after reopening SQLite, retain the exact
later incident ID, current aliases, repeat slot, persisted receipt identity and
provider idempotency key. Exact receipts suppress replay; other destination and
old-sibling follow-up receipts do not suppress that later attempt. An unknown
peer blocks recovery while a matching fresh local down still permits repeat.
These are real persisted scheduler attempts and final-send executions; the new
function does not claim additional concurrently launched worker trials beyond
the unchanged SR5 worker fixture.

**Inspection and limits.**

The final provider lookup at `notifications.go:252` uses monitor, generation and
`a.IncidentID` only. Captured aliases no longer select another lifetime. Receipt,
route, successor, maintenance/dependency, repeat and all-up recovery checks
remain in that final path. No further provider-visible identity defect was
established by the selected unchanged or new controls.

Resolver binding deduplicates complete header identities before following
recursive references, so older-root semantic cycles do not endlessly add queue
entries. Within closure inference, runs, aliases and uncertainty IDs accumulate
from finite retained inputs; closure/frontier/time metadata and predecessor
removal follow finite combinations. Change detection now includes the private
uncertainty IDs. Inspection and the finite multi-root/cycle fixtures support
termination of these cases; they are not a general safety proof over arbitrary
histories. The concrete missing standalone derivation above remains unresolved.

No new false recovery, proof-only alias/intent/receipt promotion, scope crossing,
SQL atomicity defect, or semantic-cache disagreement was established. Retained
history, CPU, heap, storage and lock latency remain unbounded under the already
documented limits. This review does not reclassify that known retention limit
as a new finding, or propose an arbitrary cap. Historical database rollback,
identity/membership replacement, hostile shared-key peers and already-mismerged
protocol-4 provenance retain their earlier limits.

The current protocol-9 passive heartbeat and other parallel integration are
outside this causal review. No full canary suite, race run, process/container
validation, or cross-platform release check was performed here. The supplied
protocol-8 full-race/process observations are historical and do not certify
these current fixes.

**Exact inputs, execution and hashes.**

Private frozen source:

```text
C:/Users/Michael/AppData/Local/Temp/uptime-canary-closure-r6-e7851f07c73845cca1ce63de01ec4443/source
```

The capture includes 206 original Go files under `apps/node-go` (105 production
files and 101 fixtures), `go.mod`, `go.sum`, and the four requested reports:
212 inputs. Shared before/copy/after SHA-256 matched for every file. All 212
private inputs matched again after validation. Only the new review fixture was
added to the private source; no captured input was edited. The shared checkout
has no accessible Git metadata, so no commit identity is claimed.

Evidence beside `source`:

```text
source-manifest.json
299605e75cc601a122db056ce01c098939ac879e388c9725966b7f30d308c206
production-manifest.json
74e5e851d09dee347c587875bbc38e85dc81125ea8037d5552b0ea661c9c38ae
final-focused-review.jsonl
9bbc0614037e8053f4345639c80446c507c4a376e6463cede13a47989b20328b
```

The production manifest contains all 105 production Go hashes. Relevant frozen
production SHA-256 values are:

```text
causal_uncertainty.go
039fc7b6b76fadf4619be1655e987ad9eb7f214df28b5da1dbc093d3df501fd4
notifications.go
015081a996c3e070ff712db1d06c2da17f447692304d17702369a40c50db00dc
causal_store.go
0e799fda0a6cb6a6270bde7545cde4b8c8d9c86e8de66a82259c770716ffcb9d
causal_reconciliation.go
fb12938ed5734ff718ee2377609c28e077ef40401bc8d1bd4f5da8a02ce19aac
incidents.go
00c2cfafc97b9a60ca5649e906e4be5594b75512d6b74e7b1c5f6657e34cd3a3
```

Preserved original fixture SHA-256 values:

```text
partition_causal_review_test.go
0cd76df64429c1a12f96eeef13071993259672d803bc6a9d9c17b498c527da6e
partition_delivery_review_test.go
290674cf4d0004b08a7f791647d5678f6dacbc16b304e900af1a0abcc5883d2b
partition_successor_review_test.go
fa5a04940893a02a14f4dbdfc1cb3fb89fbce7a2a98e0f045d2223943ff3ee34
partition_delivery_repair_test.go
85f532eb5cd266e33d5761ee924ca7951a18cf0d70a405028e821e16fbd671d2
partition_successor_repair_test.go
8c37e4abd8f47f3564572462ec24ee9f169331394610d5f37009ec2d9b0a017b
partition_closure_identity_repair_test.go
d66af28a29d17864e4eefa9c6f8e2ae9daf1b14aab36bbe4eb4b7693ef8ef616
```

Commands ran from the private source using pinned
`go version go1.27.3 windows/amd64`:

```powershell
& 'C:/Users/Michael/Documents/DEV/Uptime-Canary/.canary/tools/go/bin/go.exe' test -mod=readonly ./apps/node-go/internal/canary -run '^Test(Partition|IncompletePeerHistory|CausalCapacityReviewRound2|Queued|RecoveryUses)' -count=1 -json
& 'C:/Users/Michael/Documents/DEV/Uptime-Canary/.canary/tools/go/bin/go.exe' test -mod=readonly ./apps/node-go/internal/canary -run '^Test(ClosureIdentityReview|Partition|Causal|IncompletePeerHistory|Review|Maintenance|Queued|RecoveryUses|Notification)' -count=1 -json
& 'C:/Users/Michael/Documents/DEV/Uptime-Canary/.canary/tools/go/bin/go.exe' vet -mod=readonly ./apps/node-go/internal/canary
```

The initial unchanged selection passes 56 top-level functions in 10.935 seconds
(package event 10.936). Final combined validation fails in 30.332 seconds with
115 top-level functions passing and only the new standalone-predecessor function
failing. All 113 preexisting functions in that final selection pass. There are
313 passing and three failing test/subtest events; the three failures represent
two page-order subcases and their parent, all for the single P2. Canary vet
passes and `gofmt -l` is empty.

Final new fixture SHA-256, byte-identical in shared and private copies:

```text
ebf5e0892d430a0a7a1b704554bf9b8c428e029697abf9ef70932f5ed4835ecb
```

`review-validation.json`, `source-recheck.json`, `unchanged-focused.jsonl`,
`vet.txt` and the development reproduction logs retain the observations.
`shared-drift.json` separately records later shared changes to `accounts.go`,
`api.go`, `causal_store.go`, `push.go`, `runtime.go`, and `push_review_test.go`.
Those changes were not overlaid into this frozen review. The requested
causal/partition/repair fixtures and the two supplied production hashes
(`causal_uncertainty.go` and `notifications.go`) remain unchanged at the final
recheck. The other listed production hashes identify this frozen capture,
including its earlier `causal_store.go`, rather than the changing integration.
All findings identify the frozen inputs.

Reviewer shared writes are exactly the new
`apps/node-go/internal/canary/closure_identity_review_test.go` and this new
`docs/closure-identity-review.md`. Core, existing fixtures/reports, modules, UI
and shared integration files were read only. New review nodes never `Start`.
The unchanged HA/provider controls use their existing `local`/`cluster` helpers
when needed; those start private test nodes with `Port: Ptr(0)`. Databases,
controlled samples and loopback HTTP receivers are private. No user database,
environment file, default executable, development port 3100/5173, live delivery,
commit or nested agent was used.
