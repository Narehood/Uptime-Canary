# Pure hierarchy and health rollups

This package is a separate feature leaf. It does not change the current release
schema, replicated resources, root runtime, API, HA policy, UI, notification
delivery, or alert suppression. The existing `canary.Group` outage dependency
and grace/suppression mechanism is distinct from a hierarchy health rollup.
This package does not complete monitoring parity or change alerts.

The API is `Compile([]Node) (*Plan, error)`, `Validate([]Node) error`, and
`(*Plan).Evaluate([]Observation) (map[string]Result, error)`. Compile copies its
input into an immutable plan. Validate calls Compile and is compile-equivalent.
Evaluate returns caller-owned values with no shared mutable state. A plan can be
evaluated concurrently. A zero-value Plan is an empty forest; a nil plan errors.
Callers must not mutate an input slice during the call consuming that slice.

Node IDs are opaque, nonempty, unique, case-sensitive strings. Empty `parentId`
means a root. Only `group: true` nodes may be parents. Missing parents, self links
and cycles are errors, even for disabled branches. Enabled is explicit; its zero
value is disabled. A forest may contain multiple roots or no nodes.

Callers supply exactly the relevant current monitor hierarchy and current
observations, having resolved replication/conflict/revision and peer evidence
policy themselves. There is no clock, timestamp, scheduler, history, persistence,
latency, I/O or network dependency in this leaf. Callers compute freshness using
an explicit external clock; there is no implicit wall-clock decision here.

Only leaves (non-group nodes) accept observations. Group observations, duplicate
observation IDs, unknown observation IDs and any state other than `up`, `down`,
`pending`, `unknown` are errors, even for stale evidence or disabled leaves.
Observed `paused` is invalid: only compiled disabled nodes produce that state.
Missing or `fresh: false` health evidence is `unknown`; a fresh enabled leaf
preserves all four accepted states. No evidence becomes healthy by omission.

Each result treats that node as a subtree root. Its disabled ancestors do not
change its standalone result. A disabled node itself is `paused` and excludes its
entire branch from its ancestors' active totals. Enabled groups recursively fold
active direct child branches with precedence `down > unknown > pending > up`.
No active children means `pending`, including empty and all-paused groups. An
enabled empty group remains an active pending child of its parent, despite having
no leaves. Empty groups are never synthesized as healthy.

All Result fields use the exact camelCase JSON names below, including zero values:

- `state`: effective state of this subtree root.
- `maintenance`: true exactly when `maintenanceCount > 0`.
- `directChildCount`: all immediate children, including disabled children.
- `activeChildCount`: immediate enabled children, or zero if this root is disabled.
  A child group counts once, regardless of its number of leaves or its health.
- `descendantCount`: all strict descendants, including groups and disabled nodes;
  excludes this root.
- `leafCount`: all non-group leaves in this subtree, including self for a leaf;
  includes leaves below disabled groups. An empty group has zero leaves.
- `activeLeafCount`: leaves reachable from this root through an entirely enabled
  path, including this root. Zero for a disabled root.
- `upCount`, `downCount`, `pendingCount`, `unknownCount`: active leaves in each
  health state. A missing/stale leaf counts as unknown. These counts do not count
  groups, including an empty group's pending state.
- `pausedCount`: leaves excluded by a disabled node on their path from this root,
  even if a descendant is individually enabled. A disabled empty group contributes
  no paused leaves. `leafCount = activeLeafCount + pausedCount`.
- `maintenanceCount`: active leaves whose supplied current Maintenance annotation
  is true. Fresh applies to health State only; a stale maintained leaf counts as
  both unknown and maintained. Disabled branches contribute no maintenance.

`activeLeafCount = upCount + downCount + pendingCount + unknownCount`. Maintenance
overlaps those health counts, never changes state and never removes a leaf from
that denominator. These are snapshot counts, not uptime percentages, elapsed
durations or a historical uptime denominator. An integration calculating uptime
must preserve that distinction and must not exclude maintenance by inference.

Compile uses an iterative leaf-to-root traversal for validation, cycle detection
and cached structural counts. Evaluate visits every node once and folds each edge
once, without recursive traversal or map iteration determining health. Both use
O(nodes + observations) time/space (Compile has no observations). Output map
iteration order is unspecified; result values are invariant to input order.

`MaxNodes` and `MaxObservations` are each 100,000, inclusive. Exceeding either
returns an error wrapping `ErrCapacity`, without partial results or truncation.
These are admission limits for this computation only, not replicated config
limits. Future root integration MUST fail health unknown if capacity is exceeded,
retain the valid replication union, and never truncate that union or reject its
merge merely to fit the evaluator. Other invalid inputs also return no results;
an integration must not reuse a prior healthy result as current evidence after an
evaluation error. This package provides no such root integration yet.

The reference is Uptime Kuma's
[group monitor at pinned commit 7ca4a3debd76443d8452da95d4040200a1479af4](https://raw.githubusercontent.com/louislam/uptime-kuma/7ca4a3debd76443d8452da95d4040200a1479af4/server/monitor-types/group.js).
It considers direct children and ignores inactive children. Among recognized
heartbeat states, down outranks pending, which outranks up. No heartbeat makes a
child pending; a truly empty group is pending. Its initial up survives when a
nonempty group's children are all inactive. Canary conservatively makes that
all-paused case pending and represents missing/stale evidence explicitly as
unknown, between down and pending. Canary computes nested groups from current
leaves in a single evaluation rather than accepting group heartbeat evidence.
The source was retrieved for this comparison; no upstream code is vendored.

Package validation uses the pinned `.canary/tools/go/bin/go.exe` with
`-mod=readonly`. Tests use explicit Fresh flags and Maintenance annotations;
there are no sleeps or implicit clocks. Linux amd64/arm64 `CGO_ENABLED=0`
cross-builds verify compilation only, not Linux/Alpine execution or deployment.
Runtime integration, Alpine deployment and complete release validation remain
separate work.
