# Planned maintenance

Maintenance is replicated configuration. An administrator or editor chooses all
monitors explicitly, individual monitor IDs, group IDs, or a combination of the
last two. Names and groups are entirely user defined. Empty selection is an
error. Group selection follows a monitor's current, unconflicted group.

The node continues checking and recording observations and incidents during
planned work. Its measured state remains up, down, pending or unknown. Active
maintenance is separate metadata; it does not turn a failing service green.
New outage, repeat and recovery notifications are held. Explicit channel tests
remain available.

When a window ends, a continuing outage can notify. A destination that already
received an outage gets its owed recovery after maintenance. A brief outage
observed entirely inside an unchanged, previously configured window remains in
incident history without replaying a stale outage/recovery pair. An outage owed
before the window is preserved, including one that recovered while delivery was
held. Connected senders stagger again after release.

This is a conservative delivery policy. Configuring or editing a schedule after
an incident opens holds notifications now, but does not retroactively erase an
owed incident. Proving whole-episode coverage uses only unchanged schedules
configured by opening time. Up to 128 touching/overlapping window transitions
are considered; uncertain coverage keeps the owed delivery. Deleted, disabled,
conflicting or unavailable schedules do not hold alerts. Partitioned nodes act
on their own configuration and clocks, so a node that has not learned a
maintenance revision can notify. Keep node clocks synchronized.

## API and CLI

All routes require an account bearer. Viewers can read; admins and editors can
write. Writes use the same explicit revision/conflict/tombstone rules as monitors.

| Method | Route | Result |
| --- | --- | --- |
| GET | `/api/maintenance` | Schedule views with current window, next start, conflict and reason |
| POST | `/api/maintenance` | Create, returning a schedule (201) |
| GET | `/api/maintenance/:id` | One current schedule |
| PUT | `/api/maintenance/:id` | Replace input with the current `revision` |
| DELETE | `/api/maintenance/:id` | Tombstone with `{ "revision": "..." }` (204) |
| GET | `/api/maintenance/:id/conflicts` | Current candidate schedule revisions |
| POST | `/api/maintenance/:id/resolve` | Input with `revisions` naming every current head |

Example input:

```json
{
  "name": "Weekly home lab updates",
  "enabled": true,
  "groupIds": ["your-group-id"],
  "schedule": {
    "strategy": "weekly",
    "timezone": "America/New_York",
    "startDate": "2026-10-22",
    "endDate": "2027-10-21",
    "startTime": "02:00",
    "durationMinutes": 60,
    "weekdays": [7]
  }
}
```

Use `allMonitors: true` instead of selection lists to include all monitors.
`monitorIds` and `groupIds` reference existing, unconflicted resources when
creating or editing locally. Peer pages can arrive out of order; a missing
selection does not prevent other coordination facts from being retained.

CLI commands use `CANARY_URL` and `CANARY_TOKEN`:

```sh
canary maintenance
canary add-maintenance schedule.json
canary update-maintenance ID edited-schedule-with-revision.json
canary maintenance-conflicts ID
canary resolve-maintenance ID chosen-schedule-with-revisions.json
canary delete-maintenance ID REVISION
```

Overview includes `maintenance`. Monitor views expose active `maintenanceIds`;
incident views retain maintenance IDs and a `maintenanceComplete` marker for a
whole planned episode. Delivery views show held work without a scheduled retry.
Never-notified completed planned episodes are cancelled with
`completed_during_maintenance`; successful delivery receipts remain accepted.

## Schedule semantics

Strategies are once, every N local calendar days, daily, weekly, monthly and
five-field cron. Each schedule has an explicit IANA timezone (or UTC), inclusive
start/end dates, and an elapsed duration in minutes. The host's `Local` timezone
is rejected. Minimal Alpine deployments include Go's timezone database.

Windows include the starting instant and exclude the ending instant. Touching
and overlapping windows merge. Nonexistent DST start minutes are skipped;
repeated start minutes use their first occurrence. An overnight window is valid;
the final window ends no later than midnight after the inclusive end date.

Finite plans cover at most 3,660 local dates and 65,536 disconnected windows.
Compilation is bounded and runs before admission, outside the node state lock;
runtime evaluation uses cached immutable plans and binary search. A busy
per-node compiler returns 503 so callers can retry. Schedules with no occurrence
are rejected. See the [engine contract](../apps/node-go/internal/maintenance/README.md)
for cron syntax, compile work limits, examples and DST evidence.

The backend and shared DTO are implemented. The optional dashboard maintenance
forms and explanations remain outstanding. Independent integration review and
final coherent native race/Alpine validation are release gates.
