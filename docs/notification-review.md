# Independent notification/check integration review, round 1

Reviewed 2026-10-22. Scope: reusable channels, destination delivery identities,
incident/recovery/repeat scheduling, dependency suppression, local delivery
status, replicated resources/protocol, and active/service check integration.
Only this document and `apps/node-go/internal/canary/notification_review_test.go`
are reviewer-owned changes. No core changes, commits, subagents, user database
changes, or external Discord/email delivery were performed.

## Findings established before remediation

P1 means a release blocker affecting availability or peer convergence under
valid supported input. P2 means a reproducible correctness issue to fix before
calling the expanded notification integration complete. Parent is implementing
the fixes; original failing results below remain evidence of the findings.

### 1. P1: notification test batches have unbounded admission and share incident workers

Original locations: `delivery_status.go:108-136` (`startChannelTest`,
`sendChannelTest`), `runtime.go:289` (eight notification worker slots).

The five-second cooldown admits another batch while earlier recipients are
still waiting for the shared worker semaphore. Each admitted recipient gets a
persisted attempt and a goroutine; its delivery deadline starts only after it
gets a slot. With 50 recipients, batches every five seconds admit 10 jobs/sec,
while eight slots with the default five-second delivery timeout complete at
most 1.6 jobs/sec for a timing-out provider. Pending work can grow indefinitely
and occupy the same slots needed for real incident delivery.

Minimal repro: occupy the eight incident slots, start a two-recipient test,
advance the clock six seconds, and submit another test. Both return 202.
`TestReviewNotificationTestsRejectAnotherPendingBatch` establishes this without
external delivery. The loopback provider also blocks if test workers are later
separated from incident workers.

Action: admit only one bounded test batch per node, retain the cooldown, and
dispatch it with a small dedicated test worker budget. Release admission on
completion/cancellation. Do not enqueue unlimited goroutines behind a semaphore.

### 2. P1: the initial audience fix creates locally valid incidents rejected by peers

Locations when reproduced: `runtime.go:682-686` (cumulative `DeliveryTargets`
union), `wire.go:181` (maximum 801 IDs), `incidents.go:63` (alias audience union).

This was found while reviewing the parent's in-progress fix for finding 4.
The maximum *simultaneous* fanout is 16 channels x 50 recipients plus a legacy
webhook. It is not the maximum number of destinations accumulated over a long
outage or across merged aliases. Start an outage with 16 valid 50-recipient
channels, then replace one channel's recipients. There are still only 800
current targets, but the incident now retains 850 audience IDs. A page emitted
by the node is rejected by its own peer decoder. A receiver cannot acknowledge
that page, so convergence cannot progress past it.

`TestReviewAudienceRemainsReplicableAcrossRecipientEdits` reproduces this using
valid channel writes, incident evaluation, page serialization, and actual
`validateSnapshot`; it fails with `Invalid peer snapshot`.

Action: keep audience construction, alias reconciliation, and decoder bounds
consistent across supported edits. Never persist a locally generated record
that supported peers cannot decode. Merely using the simultaneous fanout cap
for a cumulative union is insufficient.

### 3. P2: credential-only/inactive-provider changes alter destination identities

Original location: `notification_targets.go:25` (`targetsForChannel`).

The route hash includes the complete Discord webhook URL, including its token,
and endpoint fields from every provider, regardless of the selected provider.
Rotating a Discord token for the same webhook ID therefore creates a new
destination. Changing unused SMTP/Mailgun fields on an otherwise unchanged
Brevo channel does the same. Existing receipts, retry identity, and matching
dependency coverage no longer apply to the destination; an ongoing outage can
be resent after an administrative credential/config cleanup.

`TestReviewDestinationIdentityUsesOnlyActiveRoute` fails both cases and also
guards that an actual webhook ID or recipient change *does* change identity.

Action: construct identity from the active provider's delivery route. Exclude
Discord's authentication segment, API keys/passwords, and inactive provider
fields; normalize wire-equivalent Discord hosts/API versions/wait options.

### 4. P2: adding a new destination while healthy replays a closed outage

Original locations: `notifications.go:66-86` (closed episode without a receipt
selects `down`), `notifications.go:253-267` (pre-I/O validation permits it).

Create and resolve an ungrouped outage, then add a channel/recipient while the
monitor remains up. The new destination has no receipt, so the scheduler emits
the old down followed by recovery, although that recipient was not configured
while the outage was open. This affects ordinary metadata edits; changing
check semantics is unnecessary.

`TestReviewNewDestinationDoesNotReplayClosedOutage` observes both historical
messages through a loopback receiver. The passing companion
`TestReviewExistingDestinationRetainsFastOutageDelivery` occupies every worker,
observes a real down then up for a preexisting destination, and verifies that
releasing workers still yields exactly one down and one recovery. A fix that
simply drops every undelivered closed episode breaks this required behavior.

Action: persist incident delivery intent/audience while fresh down evidence
keeps the incident open; carry it durably through restart and causal alias
merge. A new recipient added during an ongoing outage can join the audience;
one added after closure cannot. Keep the final pre-I/O gate consistent with
the scheduler and preserve already accepted receipts.

### 5. P2: unavailable destinations still advertise scheduled retries

Original location: `delivery_status.go:31-58` (`attemptView`).

Deleting, disabling, or conflicting a referenced channel removes its targets
from scheduling. Removing a monitor's channel reference has the same effect.
An existing attempt nevertheless remains `retrying` with `nextAttemptAt`, even
though no scheduler can perform that retry. Only incident retirement was
projected as cancellation.

`TestReviewUnavailableDestinationDoesNotClaimScheduledRetry` fails all four
cases. It additionally guards that an accepted receipt remains accepted, the
incident stays open/unretired, and reenabling a valid channel restores owed
active-outage work using the original retry deadline.

Action: project current route availability for unaccepted incident attempts,
using an explicit inactive/unavailable outcome and clearing a claimed future
retry. Preserve receipt truth and the attempt's durable scheduling data so a
valid route can resume while the incident is still owed delivery.

## Semantics inspected without an additional established blocker

- The last provider-I/O gate rechecks monitor/channel revisions, current alias
  receipts, later repeat slots, retirement/supersession, fresh outage evidence,
  dependency coverage, and recovery's prior destination-specific down receipt.
- Named-channel dependency suppression requires a matching parent destination
  receipt for the current fresh down episode. Failed/missing delivery releases
  at the child's bounded grace; legacy webhook compatibility is explicit.
- Channel resource conflicts fail closed for delivery. Complete-head
  resolution, immutable current-revision collisions, tombstones, and stale
  writes follow the existing resource DAG implementation. Protocol 3 rejects
  older peer semantics before merge; API and peer bodies remain bounded.
- Email fanout uses one recipient per provider attempt. Provider acknowledgement
  parsing, disabled redirects, fixed error codes, and hashed provider IDs keep
  raw provider responses/credentials out of status. Acceptance means provider
  acceptance, not inbox delivery or universal provider-side idempotency.
- Service targets are derived from endpoints; SQL DSNs/queries, credentials,
  private topics/paths, and PEM options stay out of viewer service config.
  Network credential fields are redacted on viewer monitor views. Check errors
  are fixed messages rather than raw transport/provider errors.
- Docker pending observations do not open/close an incident, authorize recovery,
  or get inverted to up/down. Recovery still requires fresh up observations
  from every assigned node. Service options participate in check-generation
  comparison, so semantic edits retire prior incidents without recovery.
- SQLite receipt persistence precedes an accepted attempt outcome; replicated
  receipts can reconstruct acceptance if the attempt write is interrupted.
  A crash after external acceptance but before receipt persistence can still
  duplicate delivery, consistent with delivery-first semantics.

These are code-review observations, not assertions that every permutation or
provider/server behavior was independently proven by this review.

## Concrete retained shipping limits

Receipts, incidents, aliases, predecessor frontiers, channel tombstones, and
local attempts are retained; the history retention setting only bounds probe
history. Repeated notification tests also add durable local attempts. Paging
bounds transmission size, not retained database/heap growth or work per tick.
`reconcileEpisodes` compares episode pairs; scheduling scans historical records
and attempts; delivery status projects all attempts before its result limit.
Large retained histories therefore increase time spent under the node lock.

The 512 resource capacity includes tombstoned channel IDs. Ancestor,
predecessor, and alias arrays also have peer validation limits (10,000 IDs), and
a single record must fit the 1 MiB peer body. These remain finite lifetime/load
limits rather than an unlimited-scale retention design. They do not justify
discarding receipts/tombstones without a separate coordination design.

The service package already documents that its pinned MySQL driver can allocate
metadata before post-decode row/column limits. The driver panic boundary and
wire/result bounds are useful protections, not a complete hostile-server heap
sandbox. This review did not establish a new exploit or change that package.

## Verification

Commands use the bundled Go toolchain and private in-memory SQLite databases:

```powershell
.canary/tools/go/bin/go.exe test ./apps/node-go/internal/canary -run '^TestReview' -count=1
```

Before remediation, destination identity (two subcases), closed-outage replay,
pending-batch admission, and unavailable status (four subcases) failed as
described. The legitimate fast-outage guard passed. The cumulative audience
fixture subsequently failed against the initial intent implementation.

Parent remediation is in progress. The most recent focused rerun stopped
before compilation with `go: updates to go.mod needed; to update it: go mod
tidy`, during concurrent module work. The reviewer did not change module
files. Route identity, closed-audience gating, bounded test dispatch, and
unavailable status fixes have appeared in core, but their final combined
verification is still pending. The cumulative-audience regression was confirmed
failing against the initial intent implementation.

This report does not claim the entire repository, real-process proofs, or race
detector was rerun by this reviewer. Race testing requires the separate
GCC-equipped agent. The six reviewer test functions are intentionally desired-
behavior regressions and should all pass before the parent closes remediation.

## Independent round 2 current status (2026-10-22)

The five round 1 findings above have been repaired on the reviewed snapshot.
The six original `TestReview...` functions pass, including the valid 800-target
recipient replacement and legitimate fast-outage down/recovery guard. They are
retained here as historical findings, not current failures. Round 2 established
the three additional issues below. Remediation is still required before this
review can report no actionable notification findings.

Round 2 writes are limited to this appended section and the new
`apps/node-go/internal/canary/notification_review_round2_test.go`. Core, shared
packages, modules, web code, and proofs were read only. Tests used private
in-memory/temporary SQLite databases and loopback providers. No commits,
subagents, user databases, environment files, or live external messages were
used.

### R2-1. P1: a valid channel ID can produce peer-invalid delivery identities

Locations: `notification_targets.go:46` and `:53` append a dot and a 24-byte
digest to the channel ID; `resources.go:30` accepts ordinary identifiers through
`validation.go:34`, whose limit is 128 bytes. `wire.go:184-186` and `:229` apply
that same 128-byte limit to incident audience IDs and receipt destination IDs.

A 104-byte channel ID is valid configuration, including on the authenticated
replication protocol, but its derived destination is 129 bytes. A node accepts
and can emit the configuration, then observing an outage produces an incident
that its own peer decoder rejects. Any later receipt has the same defect. The
invalid record prevents acknowledgement and advancement of that peer page.
Ordinary locally generated UUID channel IDs are unaffected; this is a mismatch
between the supported resource-ID domain and its derived wire records, not an
unauthenticated injection claim.

Repro: `TestReviewRound2ValidChannelIDsProduceReplicableDestinations`
(`notification_review_round2_test.go:142`). The actual snapshot validator first
accepts the complete configuration page; after incident evaluation, validating
the newly emitted page fails with `Invalid peer snapshot`.

Fix: use a bounded derived destination identity for the entire supported channel
ID domain, or explicitly align resource admission and every derived identity's
wire limit. Preserve identities for existing ordinary routes where possible;
changing every route hash would discard receipt/retry continuity. Validate both
incident and receipt output. This differs from repaired round 1 finding 2:
current audience cardinality is bounded, but one individual destination is too
long.

### R2-2. P2: audience pruning erases owed delivery through temporary unavailability or page order

Locations: `notification_targets.go:82-95` intersects durable intent with only
currently enabled, unique, resolvable routes; `wire.go:350` applies this before
acknowledging the merged transaction, and `runtime.go:613` repeats it during
normalization. `notifications.go:41` and `:269` then correctly require that
intent to send an unsent closed episode.

Observe a real down and up for an existing named destination while incident
workers are occupied. The closed episode correctly retains its intended
audience. Temporarily disable that channel, or learn a concurrent metadata
revision of the same channel. One evaluation permanently removes its audience
entry. Reenable the identical route or resolve the metadata conflict, release
workers, and no down or recovery is delivered. Current open-outage reenabling
works because fresh down evidence can refill the audience; a closed episode
cannot. Accepted receipts remain intact, so this specifically loses owed work
before acceptance.

The same destructive intersection applies to independently mergeable peer
pages: merge a closed incident containing intent before its channel resource
arrives, then merge the channel. The node has acknowledged and persisted an
empty audience, and the late configuration cannot reconstruct it. This can
arise with multiple peers or configuration inserted behind an active keyset
cursor. Page ordering normally sends resources first, but is not a causal proof
that absent configuration means obsolete intent. Re-fetching the incident from
a surviving peer can repair the loss, but that dependency defeats independent
durable acceptance and fails if that source is unavailable.

Repros: `TestReviewRound2ClosedAudienceSurvivesTemporaryChannelUnavailability`
(`notification_review_round2_test.go:13`) fails both `disabled` and
`conflicting`, observing zero messages rather than the owed down/recovery pair.
`TestReviewRound2ClosedAudienceSurvivesOutOfOrderConfiguration` (`:78`) confirms
the missing intent in the SQLite incident record after both merges.

Fix: distinguish temporary/unknown route availability from causally established
route replacement when pruning. Keep unsent intent for a temporarily disabled,
conflicting, or not-yet-known route; restoration of the same identity must be
able to resume it. Preserve the fresh-open-only rule for adding genuinely new
destinations and keep the 801-target decoder/cardinality repair intact. If
intent must be split into separately bounded records to meet these guarantees,
do so explicitly rather than treating missing configuration as deletion.

### R2-3. P2: interrupted test batches remain queued/sending forever after restart

Locations: `runtime.go:291-293` reloads local attempts unchanged;
`delivery_status.go:31-65` projects them unchanged for `kind=test`;
`delivery_status.go:161-168` and the stopped/fault return after provider I/O
leave test attempts at their last durable queued/sending state. No test-job
resumption or startup terminal-state reconciliation exists.

The new batch bound itself works: only one batch of at most 50 recipients is
admitted, two dedicated workers process it, and normal completion/cancellation
releases admission. However, stop/crash or a persistence failure can interrupt
the batch. Restart resets the in-memory batch counter, so it does not block a
new batch, but old attempts still claim `queued` or `sending` indefinitely even
though no worker can perform them. A partially persisted admission has the same
projection issue. The status endpoint therefore advertises abandoned work.

Repro: `TestReviewRound2InterruptedNotificationTestsDoNotRemainPending`
(`notification_review_round2_test.go:108`) writes the two durable crash
boundaries into a temporary database, closes it, and creates a new node. Both
states reload unchanged. This is a persisted-boundary fixture, not a separate
claim of actual provider acceptance or a killed-process notification proof.

Fix: durably reconcile unfinished test attempts at startup to an explicit
terminal interrupted/cancelled/unknown outcome, with no next attempt, or
implement intentional bounded resumption. A previously `sending` attempt may
already have reached the provider, so do not fabricate acceptance or an
unambiguous rejection. Retain historical accepted/failed test results.

### Repairs and adjacent behavior independently verified in round 2

- Active-provider-only identity, Discord token stability, historical-new-route
  exclusion, fast legitimate outage preservation, current fanout cardinality,
  pending test admission, and unavailable-route retry projection pass the six
  original reviewer fixtures.
- Existing focused fixtures pass for per-recipient retries and recovery,
  per-destination dependency receipts and release, failed/stale/missing-current
  parent evidence, current-episode repeat suppression, alias receipts at the
  final provider-I/O gate, stable retry identities, later repeat slots,
  credential/error redaction, Discord acknowledgement parsing, response/retry
  bounds, and disabled redirects.
- Existing metadata edit/restart, shared-generation conflict resolution,
  SQLite identity/history/receipt persistence, retry-key restart, repeat policy
  edits, release staggering, grouped short-episode handling, and service-option
  privacy/persistence fixtures pass.
- Database ownership uses a retained sidecar file with a kernel lock, resolves
  existing directory/file symlinks, closes the database and lock on `Stop`, and
  releases the lock when `New` fails. The native Windows tests pass for duplicate
  owners, failed startup/reopen, and actual process death/reacquisition. This
  reviewer did not rerun the Unix/Alpine ownership path. No additional concrete
  database-lock blocker was established in this focused round.

### Round 2 verification and retained limits

The bundled Go toolchain completed these focused checks:

- Before adding round 2 fixtures, `go test ./apps/node-go/internal/canary -run
  '^TestReview|^TestStore' -count=1`: PASS (5.043s); this includes all six round 1
  reviewer functions.
- Selected existing provider/channel/fanout/dependency/alias/repeat and database
  ownership tests: PASS (13.771s).
- Selected existing SQLite/retry/metadata restart, conflict resolution, release
  staggering, grouped recovery, and service privacy/persistence tests: PASS
  (10.220s).
- `go test ./apps/node-go/internal/canary -run '^TestReviewRound2' -count=1`:
  FAIL (2.426s), reproducing R2-1 through R2-3. All four new functions assert
  desired behavior and can be retained as passing regressions after parent
  repairs; they do not assert that bugs are expected behavior.

The retained storage/capacity constraints from round 1 still apply. Bounded test
admission does not bound the lifetime number of durable local test attempts.
Incident/receipt/alias/frontier retention, pairwise reconciliation, historical
scheduling/status scans under the node lock, 512 lifetime resource IDs including
tombstones, and finite peer-array/single-record limits remain material operating
constraints. This round did not establish that these are unlimited-scale or
unlimited-lifetime designs.

This is a focused notification and database-ownership review. It does not
certify all Kuma-equivalent features, all repository Go tests, the final Alpine
build/container suite, or the race detector. The parent's reported real-process
proof results were not duplicated here; the previously reported race timing
failures were not rerun or dismissed as benign.

## Independent round 3 (2026-10-22)

All prior sections were read and preserved. The round 1 and round 2 reviewer
regressions pass on the initial round 3 snapshot. The new durable intent rows,
bounded incident summary, protocol 4 boundary, bounded derived destination IDs,
and interrupted-test reconciliation repair the previously reported cases.
Round 3 established two further issues below; this is not a shipping sign-off.

Reviewer writes are limited to this appended section and the new
`apps/node-go/internal/canary/notification_review_round3_test.go`. Core, shared
packages, modules, UI and proofs were read only. Tests used private in-memory or
temporary SQLite databases, injected SQLite failures and loopback receivers.
No commits, subagents, user databases, environment files or external delivery
were used. Other work was changing the shared checkout during this review.

### R3-1. P1: partial incident pages irreversibly associate an old audience/receipt with a later outage

Locations: `incidents.go:30-56` (`equivalent`), `:58` (`combine`), and `:111`
(`reconcileEpisodes`); `wire.go` reconstructs the incident map from that result
in `mergeSnapshot`. The intent lookup in `notification_intents.go:36` and the
receipt lookup in `runtime.go` then trust every resulting alias.

Consider three valid records for the same monitor and check generation:

- A is a completed outage, with run `a:1`, closure `a:2,b:2`.
- B is a delayed open sibling of A, with run `b:1`, opened within A's lifetime.
  Its audience fact and successful down receipt name B.
- C is the genuine later outage, with run `a:3` and predecessor A.

Receiving all three together correctly joins A with B and keeps C independent.
Receiving the two open records before A's closure record instead joins B with
C: there is no shared run, but the final unconditional `return true` considers
them equivalent, and C's predecessor A does not yet name B. Every individual
fixture page passes the actual protocol 4 decoder; no ID collision, reused run,
invalid record or unsupported channel configuration is required.

When A subsequently arrives, the receiver does not undo that association.
The old B audience fact and receipt now apply to C. With fresh `a:3` down and
`b:4` up observations, the loopback receiver observes **zero new downs**, while
the identical complete-history control sends exactly one. The old successful
receipt has silenced a real later outage. The ordering also fits keyset IDs
`b-delayed-sibling`, `c-current`, `z-prior`; pages and different peers need not
deliver an incident's predecessor first.

Replaying the complete source history is not a safe repair. An intersecting
alias is accepted before checking predecessors, so the replay can combine all
three records and attach A's old closure to C's newer run. The original
successor identity is lost; later evaluation can create another incident for
the still-current run rather than restore C. This is an existing causal
reconciliation defect exposed by independently mergeable pages, not a SHA-256
collision or loss of the new intent rows themselves.

Repro: `TestReviewRound3DelayedPredecessorCannotMoveOldReceiptIntoNewOutage`
(`notification_review_round3_test.go:60`). The complete-history control passes;
the predecessor-last case fails both alias/receipt isolation and actual delivery.

Fix direction: do not permanently alias causally ambiguous open records across
an unresolved predecessor boundary. Retain enough original causal evidence to
resolve delayed siblings without transferring old receipts or audience to a
successor. Merely bounding the inline summary or persisting more intent rows
cannot repair an incorrect alias relation. Preserve legitimate concurrent
partition aliases and the delivery-first behavior while fixing this case.

### R3-2. P2: missing/null intent destinations become legacy-webhook authority

Location: the `s.Intents` validation loop in `wire.go` (line 86 on the latest
inspected snapshot). It checks the unmarshaled string and deterministic hash,
but does not require the four raw record fields to be present and non-null.
Go unmarshals both an absent `destinationId` and JSON null to the empty string.
The empty string is also the valid, meaningful legacy-webhook destination.

A correctly hashed intent with either malformed destination representation is
therefore accepted, persisted and indexed as delivery authority for that route.
The actual authenticated `POST /internal/snapshot` handler returns HTTP 200 in
both cases. In the fixture, a monitor closes without a destination, a legacy
webhook is added while healthy, and ordinary evaluation correctly sends
nothing. Merging either malformed fact then causes **one historical down and
one recovery** to that newly configured webhook.

Repro: `TestReviewRound3IntentRequiresExplicitNonNullDestination`
(`notification_review_round3_test.go:129`). This is malformed authenticated-peer
input acceptance, not an unauthenticated injection or a claim that the current
honest producer emits null destinations. Explicit `destinationId:""` must stay
valid for legitimate legacy intent; the fix is presence/type/null validation,
not removal of that destination.

Fix: validate every raw intent object with required non-null `id`, `monitorId`,
`incidentId`, and `destinationId` before the existing type/identifier/hash
checks. Reject the complete malformed page before any merge or acknowledgement.

### Round 3 independent passing checks

- All round 1 and round 2 reviewer tests, plus the parent's
  `TestPagedIntentBeyondIncidentSummarySurvivesDelayedPagesAndRestart` and
  `TestDeliveryIntentAndEpisodeCommitAtomically`, pass together (4.549s).
  This includes 851 paged audience facts, delayed configuration, restart,
  recipient edits, restored routes, safe test interruption and the prior
  legitimate fast-outage guard. A final rerun after maintenance compilation
  resumed also passes (5.018s), using `-run
  '^TestReview|^TestPagedIntent|^TestDeliveryIntent' -skip '^TestReviewRound3'
  -count=1`.
- `TestReviewRound3MergedAliasIntentTransactionRollsBackAndRestarts` passes.
  An injected incident-write failure after new intent writes rejects the page
  and preserves both memory/indexes and SQLite state. Retrying that identical
  page commits canonicalization, removes the obsolete alias record, and retains
  a fact naming the old alias across an actual database reopen.
- `TestReviewRound3MaximumLengthIDsValidateWithIntentAndReceipt` passes.
  Valid 128-byte channel, monitor and incident IDs produce a bounded destination
  and a page accepted by the real decoder, including both intent and receipt.
- The existing entire canary package suite, excluding the new round 3 fixtures,
  passes: `go test ./apps/node-go/internal/canary -skip '^TestReviewRound3'
  -count=1` (44.070s), before the concurrent maintenance integration. This
  includes existing real HTTP peer paging, lifecycle,
  channel/provider/dependency/repeat, SQLite ownership and security fixtures.

The final round 3 run compiled after temporary build failures during the
parent's maintenance integration. `go test ./apps/node-go/internal/canary -run
'^TestReviewRound3' -count=1 -v` completed in 2.274s: two failing functions for
the findings above, and two passing functions for rollback/restart and maximum
IDs. The complete-history alias control and explicit empty legacy-destination
control also pass. Both missing/null variants return 200 and produce the
unwanted down/recovery pair. No core changes were made to bypass build errors.

Storage/heap retention and sweep costs remain the operating limits documented
in earlier rounds. Intent paging bounds record/transmission size; it does not
bound retained fact count or total work per sweep. This review did not rerun
the compiled-process proof suite, Alpine build, native/Linux race detector, or
all repository Go packages. Earlier proof successes predate this intent fix,
and the separately investigated race timing failures remain outside this
review's verification. The parent must rerun final integrated checks after
remediation and complete maintenance/check integration separately.
