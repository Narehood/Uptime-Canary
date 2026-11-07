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

A blocked or not-yet-started monitor operation does not have to become applied
before removal can clean up its intended assignments. An unstarted definition
with no remaining runners is cancelled without creating a monitor. A shared
unstarted definition keeps its other desired nodes; their unrelated preflight
errors do not keep the removed registration around. Existing physical monitors
still require retirement or survivor confirmation.

If a saved monitor command may already have run, cleanup keeps its exact payload
and sequence. The administrator authorizing removal also authorizes resolving
that saved command before cleanup proceeds, including waking a blocked worker or
one whose earlier authorizing account changed. This does not invent a successful
acknowledgement or bypass node-side version/revision checks. Already-queued
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
