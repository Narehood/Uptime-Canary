# Group hierarchy and current health

Working protocol 11 adds optional `parentId` to a user-defined group. An absent
or empty parent makes the group a root. Parent links organize current health;
they do not change notification or maintenance selection.

Local writes require a unique, complete parent configuration and reject missing
parents, self-parenting and cycles. Valid concurrent peer revisions are retained
even when their union creates a cycle or a missing parent. Reparent a group to
the root to repair it. Deleting a parent preserves its children's references
and the monitors themselves.

## Reading health

The existing `GroupView.state` still describes the group's configured outage
dependency. New `healthState` describes the group and its nested descendants.
`health` contains the rollup counts when the graph is usable; `healthReason`
explains an unknown projection without exposing private configuration.

Health precedence is down, unknown, pending, then up. An empty group or a group
with only paused descendants is pending. Paused monitors are excluded from
active health. Maintenance annotates health and counts without turning a down
monitor into an up monitor. Counts describe current leaves, not uptime,
latency or an SLA. The [engine contract](../apps/node-go/internal/rollup/README.md)
defines every structural and leaf count, including empty nested groups.

Every possible live candidate parent participates in ambiguity detection.
Conflicted monitor placements similarly affect every possible component. A
missing proof, conflict, missing parent or cycle makes the connected component
unknown and omits its counts. Unrelated components can still derive health.
The projection changes no configuration, observations, incidents or deliveries.

Computation admits at most 100,000 group and grouped-monitor nodes. Exhaustion
returns unknown rather than trimming the graph or rejecting replicated data.
The retained candidate history has no universal CPU, heap or lock-latency bound.

## Alerting and publication

Parent links do not inherit dependency suppression or maintenance selection.
Those still use a monitor's directly assigned group. This change does not add
an aggregate alert monitor or publish a private hierarchy on a status page.
The dashboard presentation is a separate implementation step.

Protocol 11 requires a coordinated node upgrade; older peers cannot silently
ignore the parent relationship. Native integration tests cover states, counts,
RBAC, strict input, ambiguous placement, cycles, out-of-order proof, restart,
transaction rollback, capacity and unchanged direct notification behavior.
A compiled two-process proof passed in 17.628 seconds against frozen source
`17161f1bf1114502af467e040d4a3bba4ba032a6e34cd36c681b41d7e918e5db`.
Individually valid partitioned parent edits formed a cycle after healing; both
nodes retained it as unknown health while monitoring and connected deduplicated
delivery continued. Reparenting restored counts without a new outage, survived
restart and showed real recovery. Source, binary and proof fingerprints remained
unchanged. Evidence: `.canary/process-proofs/20261022T214157709114Z/`.
Independent integration review subsequently found no concrete defects or
unresolved objections. Its 16 tests, 20,480 oracle projections, focused race
checks and vet passed; all eight private mutations were detected. All 394
capture entries verified, including both long-path historical fixtures. See
[hierarchy review](hierarchy-review.md). Combined release validation remains
required.

The same frozen source subsequently passed all 15 compiled-process proofs in
88.686 seconds, with zero failures or skips. Its complete ordinary canary-package
run passed 815 test/subtest events in 150.366 seconds, with two expected Windows
skips for Unix question-mark filenames. This is core integration evidence; it
does not include the independently failing evaluator-package review or establish
race, Alpine or release readiness for protocol 11.
