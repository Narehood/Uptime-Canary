# Go runtime coordination

Monitor revisions and incident lifetimes are separate. Every configuration
write still creates an immutable revision in the existing DAG. The optional
`checkGeneration` on a monitor names the ancestor revision that began the
current check generation; absence means the monitor's own revision. Edits to
`name`, `groupId`, `intervalMs`, `repeatAfterMs`, and `webhookUrl` inherit that
generation. An uninterrupted outage retains its incident ID/aliases,
`openedAt`, down runs, successful receipts (and hence `notifiedAt`), retry keys,
and closure/predecessor history across those edits. The incident's recorded
monitor name remains its opening name; webhook payloads use the current name
and destination. Changing a destination does not replay a successful initial
alert to the new destination. Repeats use the current interval after the latest
successful down/repeat delivery; slot numbers advance monotonically from prior
successful slots even when that interval changes. Recovery uses the current
destination. Grace remains anchored to the original opening.
New repeat/recovery attempts use the earliest known successful down receipt's
incident identity, even if a later sibling changes the canonical alias. Existing
attempts retain their persisted identities; payloads include all current aliases.

Measurements remain tied to the exact configuration revision. An edit cancels
old in-flight probes and schedules new ones; old-revision samples never count
as current evidence, even when their generation matches. A newly completed
down probe carries the previous down-run identity only within the same check
generation. Recovery requires fresh up probes from every assigned node under
the current revision, causally after all down runs in that incident. A genuine
later outage uses new run sequences and a new incident with retained predecessor
and closure information; sharing a generation does not merge successive outages.

Changes to `target`, `type`, `expectedStatusCodes`, `timeoutMs`, monitor `nodeIds`,
`enabled` (pause/resume), or deletion start a new check generation. At the next
evaluation, a unique configuration head retires each open incident belonging
to a different ancestor generation. Retirement durably sets `resolvedAt`, a
down-run frontier, and `retiredBy` (the new configuration revision); the API
also exposes `retirementReason`. This is administrative completion, without an
up observation or a recovery webhook. The new check must produce new-revision
evidence before opening another incident. Pause/delete cannot leave the old
incident open indefinitely, and resuming or reverting a semantic change never
reuses its generation. Already-dispatched webhook I/O may finish; its successful
receipt remains attached to the old incident. Queued work is rechecked before
provider I/O and cannot deliver for a retired generation.

Conflicting configuration heads suspend checking and sending until explicitly
resolved. A resolution preserves continuity only when every head shares the
same generation and check semantics as the resolved input. Otherwise it starts
a new generation and retires open ancestor incidents, including episodes from
losing branches. Retirement requires causal ancestry, not timestamps or merely
equal targets. Delayed config pages cannot replace their descendants; delayed
incident pages retain the original generation and are reconciled/retired again
before evaluation. Retirement markers are monotonic through merges and SQLite
restart. A previously unseen old sibling may remain a separate historical
retired record when its runs do not fit a known closure; it cannot remain open
or authorize a recovery, nor can it absorb an incident from a new generation.

Legacy persisted monitors/samples without `checkGeneration` use their original
revision as the generation. No immutable persisted revision is rewritten.
Upgrade all peers together for these lifecycle rules: older runtimes do not
understand generation continuity or retirement metadata.

Group notification suppression is delivery-first. A child initially waits its
group's `graceMs`, measured from that child's outage opening. Refreshing a down
observation or retrying a failed webhook cannot reset this deadline. Beyond that
deadline, fresh parent-down evidence suppresses children only when an open
incident for the current parent check generation/run has a successful `down`
receipt, with fresh measurements under the exact current parent revision.
A missing destination or continuously failing destination therefore fails open.
An earlier parent episode's successful alert cannot authorize suppression of a
new episode. Stale, missing, conflicting, paused, or cyclic dependencies fail
open after settling. The dependency never suppresses itself through its own
group. Reasons describe the observed dependency state and delivery evidence;
they do not diagnose internet, power, or connectivity failure.

Suppression and eligibility use the same policy in the management views and
the final send gate. Recovery for an already-notified child remains eligible
during parent failure. A child still down after suppression ends can alert;
an entirely recovered, never-delivered episode is durably marked complete and
does not replay an old outage/recovery pair. Sender rank windows begin again
at suppression release so connected candidates do not all send immediately
after a long wait. This does not restart the child's original group grace.
Persisted attempt IDs and incident
identities survive alias reconciliation and retries. Immediately before provider
I/O, the runtime resolves the captured incident/attempt into current aliases,
rechecks current configuration, observations, suppression and receipts, and
refreshes payload aliases. A successful repeat receipt for the queued slot or
any newer slot cancels that queued repeat.

Working peer protocol 12 retains the keyset paging introduced in protocol 2 on
authenticated `POST /internal/snapshot`.
Each page is an independently validated and durably merged partial snapshot;
absence of an item never implies deletion. Stable ordered keys identify monitor
heads, group/resource heads, notification intents, revision proofs, heartbeat
registers, receipts, incident evidence and sources, then observations. `next` names the last
included key when more records remain; empty `next` finishes the sweep. `after`
requests the other peer's next page. Each peer has independent send/receive
cursors, advanced only after a successful response and durable local merge.
Lost responses repeat an idempotent page. Four exchanges per peer per sync round
bound work; remaining cursors resume next round. A finished sweep restarts at
the beginning so edits to previously visited keys and new keys behind a cursor
are eventually transmitted. Restarting the process resets cursors and safely
replays the retained dataset. A response always pages its own dataset, even if
the request was small, so a large receiving node cannot trap the sender behind
a full-snapshot response limit.

Pages normally contain at most 512 records and target 256 KiB. An individual
larger record occupies a page alone. Every encoded HTTP request and response
remains below the existing 1 MiB ceiling, including envelope/cursor overhead.
There is no aggregate receipt or incident count ceiling on a paged sweep.
All successful-send receipts remain durable locally and eligible for peer
exchange; no repeat slots, aliases, configuration tombstones, incident closure
frontiers or predecessors are pruned. Delayed peers retain the same causal
reconciliation and deduplication information as peers with a short backlog.

Remaining limits: peer membership is statically configured. All coordination
history is retained in memory/SQLite, so
storage and total sweep work grow with history. Catch-up time depends on the
configured sync interval, peer latency and backlog, and backlog growth must not
permanently exceed exchange throughput. One serialized coordination record must
fit within 1,044,480 bytes (1 MiB less 4096 bytes reserved for the envelope).
Protocol 6 stores revision edges and incident run/alias/predecessor evidence as
independently paged immutable facts. Mutable summaries contain at most 32 direct
revision parents and 32 incident IDs/runs per node; hydrated evidence retains
the full history. Old self-contained records are transactionally migrated into
exact legacy claims. Missing commitments remain pending until their complete
evidence arrives. The old 10,000-ancestor/1,000-run message cliffs therefore do
not discard valid local history. Local admission still counts retained IDs:
200 monitors, 200 groups, 512 resources and 64 maintenance IDs. Replication can
accept a valid partition union up to 32 times those local bounds, while new
local creation stops at its admission bound. Tombstones count toward admission.
This preserves convergence without promising bounded total retained history.
Protocol 12 nodes must be upgraded together. Older protocol inputs are
rejected before merge so an old decoder cannot silently discard new check or
notification-channel semantics. Versioned resources share the paged exchange;
account credentials, browser sessions, raw history and local delivery attempts
never enter it.

Delivery intent is a separate monotonic coordination fact. Destinations are
captured only during an observed, open outage, then retained through temporary
channel disablement, conflicting revisions and delayed configuration pages.
Recipient churn adds separately paged intent rows rather than growing an
individual incident record. The incident carries at most 801 audience IDs as a
summary; receipt and intent lookup uses all reconciled aliases. An intent and its
incident are committed atomically. Newly added destinations do not receive
already-closed historical outages, while an existing destination can still
receive the down/recovery pair for a brief outage it was configured to observe.

Each file-backed node takes an exclusive kernel ownership lock on its canonical
database path. A second process cannot open that node database. The kernel
releases ownership after process death; the sidecar lock file is retained and
must not be removed while a process is using the database. Startup failure and
normal shutdown release both the SQLite connection and ownership lock.

The native real-peer regression exchanges 5000 durable receipts from each of
two SQLite-backed HTTP nodes, 100 closed episodes with repeat slots/frontiers/
predecessors, 200 monitors and 200 groups, including large revision ancestry.
It loses a response after the receiving peer commits, resumes across bounded
rounds, then verifies new monitor/group revisions and new receipts still reach
the other node. It also introduces a delayed old incident sibling and checks
that the old closure survives and a later outage remains independent. Every
request/response body is measured against the unchanged 1 MiB ceiling.

Native lifecycle regressions use actual HTTP probes/webhooks and two peer HTTP
servers backed by SQLite. They cover group-only and name/interval/repeat/webhook
edits during a delivered outage, stale authenticated config/incident/sample
pages, a partition, both peers restarting, one real recovery, and a distinct
later outage with a delayed old sibling. Additional cases cover each semantic
retirement category, pause/resume, metadata-only and mixed-generation conflict
resolution (including a target change followed by reversion), durable retirement,
and a previously unknown late run. Docker/Alpine execution and race detection
remain unverified in this environment; native builds use `CGO_ENABLED=0`.
An HTTP/webhook repeat-policy regression lengthens, shortens and disables the
repeat interval after multiple successful repeats while retaining the incident
identity and advancing delivery slots.
A release-window regression verifies that a secondary sender waits only its
sender stagger after a long suppressed outage, without restarting group grace.
