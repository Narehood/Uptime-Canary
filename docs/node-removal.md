# Removing a registered node

Administrators can choose **Remove** on a node card and enter its current name
exactly. Capitals and spaces count. The server checks the current name as well
as administrator authority; a stale name or a nonmatching confirmation changes
nothing. The bundled local node remains managed through deployment settings.

Confirmation queues a durable background job. Closing the dialog or restarting
the dashboard does not cancel accepted work. The job:

1. Removes the registration from dashboard node groups, deleting groups that
   would have no members.
2. Removes its runner assignments from dashboard-managed monitors. Shared
   monitors keep running on their remaining nodes. A monitor with no runners
   left is retired using the existing replicated deletion path.
3. Transfers a shared monitor's management anchor to a surviving peer when
   necessary, preserving its physical monitor ID and retained history.
4. Revokes this dashboard's delegated access and removes the registration only
   after the preceding steps are confirmed.

Copied notification, tag and monitor-group settings on surviving placements
remain intact. If the removed node supplied synchronized settings, the surviving
copies stop following that source and retain their last confirmed local settings.

Ordinary dashboard reads and edits for this registration stop while removal is
pending. Internal management access remains available for cleanup. The node
service itself is not shut down; its databases, native checks, accounts and peer
connections outside these managed placements are not erased.

## Unfinished removal

An administrator can choose **Remove now** on a pending removal and type the
node's current name again. This explicitly removes its dashboard registration,
node-group memberships and monitor assignments locally, without requiring the
departing node to answer. It also works for jobs already pending before this
option was added. The accepted request is durable; local cleanup waits only for
in-flight dashboard operations to finish, then commits its changes atomically.

Shared monitors keep their remaining nodes and physical monitor IDs. Definitions
with no remaining nodes are retired on the dashboard. An unreachable node may
still run checks and send alerts independently: local removal cannot confirm
that those checks stopped. Stop or reconfigure that node separately if needed.
The dashboard retains an encrypted revocation job and the abandoned command
journal, and retries access revocation separately. It never replays abandoned
monitor writes against the forgotten node. Pending writes on surviving nodes
retain their original payload before runner assignments are updated.

After local removal, administrators can find the node under **Pending access
revocations** on the Nodes page. It is separate from active nodes and remains
listed until delegated access is confirmed revoked. If the original authorizing
account changes, choose **Authorize revocation** and type the saved dashboard name
exactly to renew authority with the current administrator. This only resumes
access cleanup; it does not restore the registration or resend monitor commands.
Renewals continue to work when the bounded queue is full. This renews dashboard
account authority, not an expired or replaced credential on the remote node. If
the node refuses all saved credentials, revoke this dashboard's delegated
accounts directly on that node; local removal still completes independently.

The regular **Retry removal** action below continues to require confirmed remote
cleanup. **Remove now** is the explicit alternative when that cannot finish.

A blocked or not-yet-started monitor operation does not have to become applied
before removal can clean up its intended assignments. An unstarted definition
with no remaining runners is cancelled without creating a monitor. A shared
unstarted definition keeps its other desired nodes; their unrelated preflight
errors do not keep the removed registration around. Existing physical monitors
still require retirement or survivor confirmation.

A prepared last-runner definition can also be cancelled if its durable journal
proves every command is still unsent and no monitor was previously assigned.
Unstarted shared definitions retain a frozen channel/tag snapshot when their
source is removed. New target placements install it once, including on legacy
nodes; existing survivors retain their confirmed settings. Edits before that
first apply keep the snapshot. Once installed, ordinary local references remain
and the frozen payloads are discarded.

If a saved monitor command may already have run, cleanup keeps its exact payload
and sequence. The administrator authorizing removal also authorizes resolving
that saved command before cleanup proceeds, including waking a blocked worker,
replacing a refused editor credential, or renewing authorization after an account
changes. This does not invent a successful acknowledgement or bypass node-side
version/revision checks. Already-queued
removals use this recovery automatically after the dashboard is updated.

An offline runner or unconfirmed management command keeps the entry visible as
**Removal unfinished**, with a progress/error message. Automatic retries retain
the exact saved commands. This flow does not blindly forget a permanently
unreachable node or claim that it stopped checking. Unrelated node removals can
proceed independently. Shared dependencies still require their own confirmations.

Repair an address, certificate or credential before starting removal if it is
already known to be wrong. If the dashboard account authorizing cleanup changes,
another administrator can use **Retry** to renew authority without replacing an
unknown command. Concurrent edits and conflicted node settings can require
review; cleanup does not silently overwrite those settings.

## API compatibility

`DELETE /api/dashboard/nodes/:id` accepts:

```json
{"force": true, "confirmation": "Exact dashboard name"}
```

For an already-pending removal, add `"forget": true` to request the separate
local-removal path. Its `202` response confirms a durable local cleanup request,
not remote retirement or revocation. A repeated name-confirmed request can renew
the saved revocation authority after a dashboard administrator account changes.

A successful queue returns `202` with the public registration in `removing`
state. This confirms acceptance, not completion. Repeating an accepted request
does not create a second job. The browser never automatically repeats a failed
destructive request; it refreshes the registry to investigate uncertain answers.

An empty-body DELETE retains the older behavior and refuses active fleet
placements. Existing clients therefore cannot accidentally opt into cleanup.

## Validation scope

Regressions cover exact confirmation, authorization, journal failure/rollback,
restart recovery, offline and independent removals, last-runner retirement,
surviving copies and notification references, and explicit HA anchor handoff.
The focused Chrome path uses the actual React page and Go server with private
loopback fixtures and disabled monitors. These component checks do not establish
complete Go/SQLite HA release readiness or live-provider behavior.
