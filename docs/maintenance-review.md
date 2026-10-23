# Independent integrated maintenance review

Reviewed 2026-10-22. Three actionable findings were established against the
protocol-5 maintenance integration before the in-flight protocol-6 capacity
and causal-proof changes: one P1 and two P2s.
This is a focused independent review of the new Go HA maintenance integration,
not a release certification. The aggregate-capacity issue being repaired
separately is excluded.

Reviewer writes are limited to this document and
`apps/node-go/internal/canary/maintenance_review_test.go`. Core, existing tests,
modules, UI, and other documentation were read only. No commits or subagents
were used. Fixtures use private SQLite databases, an injected clock, local HTTP
handlers, the actual protocol-5 peer decoder, and loopback notification
receivers. Nodes do not start their service listener. No user database, port
3100, environment file, external request, or live notification was used.

## M1. P1: delayed earlier evidence leaves an invalid maintenance completion

Location: `maintenance.go:315-327`, especially the unconditional OR of
`MaintenanceComplete` at line 320; `incidents.go:80` lowers `OpenedAt` when
combining incidents. The delivery scheduler and final I/O gate then trust that
completion to discard unsent delivery.

Repro: `TestReviewMaintenanceEarlierPeerAliasPreservesOwedOutage`, both
`memory` and `restart`.

1. Create the 12:00-12:10 UTC maintenance window through the management API at
   11:58. At 12:01 observe a current down sample, sequence 2, belonging to run 1.
   Resolve it at 12:03. The local incident is correctly marked complete without
   sending a down.
2. Decode and merge a valid peer page containing a closed sibling for that same
   run 1. The sibling retains its original 11:59 opening and is not marked
   maintenance-complete. Both records have the same closure frontier. This
   uses positive shared-run evidence, not the previously repaired ambiguous
   predecessor case.
3. The reconciled incident now opens at 11:59, while its maintenance marker is
   12:01. Nevertheless, `MaintenanceComplete` remains true. Advance to 12:11
   with healthy evidence and evaluate twice.

Observed: **zero downs and zero recoveries**, with `MaintenanceComplete=true`.
The same loss survives a real SQLite close/reopen. Expected: the newly known
pre-window part makes the never-delivered outage owed, so one down/recovery
pair must survive the hold. The incident and its intent remain in history;
the completion bit prevents their delivery.

Fix direction: completion cannot be an unconditional monotonic fact when alias
reconciliation can expand the observed outage. Invalidate or re-prove it against
the resulting opening, maintenance marker, and coverage evidence. Preserve the
valid entirely-planned control and recovery owed to a destination with an
accepted down. Simply retaining the minimum marker does not repair this case:
the merged opening is already earlier than that marker.

## M2. P2: a group round trip erases a genuinely uncovered interval

Location: `maintenance.go:255-270` proves historical selection using the
monitor's current group; `recordMaintenance` at lines 292-312 keeps the original
start marker after release/reentry. `maintenanceCovers` ignores the release
record and changes to the monitor's maintenance eligibility.

Repro: `TestReviewMaintenanceGroupMoveCannotEraseUncoveredInterval`.

1. Before the window, create a group-selected maintenance resource and a monitor
   in that group using the management API. Open an outage at 12:01 under
   maintenance.
2. Occupy the notification worker slots. At 12:02 update the monitor through the
   API to remove its group, then evaluate fresh down evidence. The monitor is
   now explicitly outside maintenance and has an owed down.
3. At 12:03 move the same monitor back into the group. Close the unchanged check
   generation at 12:04. Release workers and evaluate healthy samples at 12:11.

Observed: `MaintenanceComplete=true` and **zero down/recovery deliveries**.
The resource itself was unchanged, but the outage's 12:02-12:03 portion was not
selected by it. Current membership and `resource.UpdatedAt <= OpenedAt` cannot
prove continuous historical coverage.

Fix direction: retain a conservative indication that coverage/selection was
broken, or invalidate the completion proof when relevant membership changed.
At minimum, an observed release inside an outage must not disappear from its
completion decision when the monitor reenters maintenance. Continue allowing
the current selection to hold new I/O without deleting previously owed work.

## M3. P2: a queued worker bypasses the maintenance release stagger

Location: `notifications.go:151-159` applies the release timestamp and sender
rank while scheduling; `sendNotification` at lines 253 onward rechecks current
maintenance but does not recheck that scheduling deadline after a hold/release.

Repro: `TestReviewMaintenanceQueuedWorkerHonorsHoldAndReleaseStagger`.
The `active-window` control passes; `release-stagger` fails.

Use node `b` with candidates `[a,b]` and the default 5,000 ms sender stagger.
Queue an actual rank-1 down worker at 11:59:06, after its original opening-based
deadline. Pause it before its state-lock/I/O gate. While it is paused, evaluate
fresh samples inside maintenance at 12:01, then at the exact 12:10 end. The
local attempt records the hold and release. Resume the queued worker.

Observed: the loopback provider receives the down at **12:10:00**, rather than
waiting until **12:10:05**. A second evaluation after the stagger confirms the
outage still delivers only once locally. Resuming the same worker inside the
active window correctly makes no provider call.

This is a deterministic paused-worker fixture; it does not claim a normally
scheduled worker routinely waits ten minutes. It establishes that a job
crossing the transition bypasses the promised release staggering, creating an
avoidable duplicate opportunity before the preferred sender's receipt can
arrive. Partition duplicates remain permitted by the architecture.

Fix direction: revalidate the current release deadline/rank at the final gate,
or invalidate queued jobs across the hold transition and let the scheduler
readmit them. Preserve retry/receipt identity and the existing active-window
I/O check.

## Passing independent controls

The eleven additional functions completed together successfully in 6.152s:

- Exact inclusive start/exclusive end, millisecond edges, and backward clock
  movement invalidate/evaluate the window cache correctly.
- Unchanged overlapping resources prove uninterrupted union coverage; an
  unobserved gap retains the owed down/recovery pair.
- Delete, disable, retime, and create-after-opening cases conservatively retain
  owed work.
- Previously accepted downs retain owed recovery; repeats and recovery remain
  held until the end. Explicit named-channel tests are allowed during maintenance.
- A peer resource-write transaction failure leaves SQLite, the resource head,
  atomic compiled-plan cache, and evaluated window cache unchanged. Retrying
  commits the new plan; reopening SQLite recompiles that committed revision.
- Demoting or deleting an editor through the real identity API during uncached
  preflight produces 403 or 401 when the maintenance request resumes. Neither
  the resource nor its precompiled plan is published. The fixture's context
  hook is a deterministic preflight interleaving, not a compilation benchmark.
- A real `tick` with an injected local probe still writes down history and an
  open incident while maintenance holds delivery; the public raw state stays
  down.
- Explicit monitor/group selection uses OR. Deleting a selected group fails
  open while an explicitly selected monitor stays held.
- A real peer resource conflict fails open. Resolving all current heads enables
  maintenance and holds an already-owed recovery until the end.
- Learning a previously missing schedule after an unsent closed episode does
  not retrospectively erase that owed work, even when the resource timestamp
  predates the outage.

The standalone maintenance engine suite also passed (1.458s), including its
existing bounded recurrence/DST tests. The reviewer did not add engine tests or
change that package.

## Commands and verification limit

Use the portable `.canary/tools/go/bin/go.exe` from the repository root.

```powershell
# Three finding functions: FAIL, with the passing active-window control.
.canary/tools/go/bin/go.exe test ./apps/node-go/internal/canary -run '^TestReviewMaintenance(Earlier|Group|Queued)' -count=1 -v

# Eleven completed controls: PASS (6.152s).
.canary/tools/go/bin/go.exe test ./apps/node-go/internal/canary -run '^TestReviewMaintenance' -skip 'Earlier|GroupMove|Queued|Compiler' -count=1 -v

# Existing engine suite: PASS (1.458s).
.canary/tools/go/bin/go.exe test ./apps/node-go/internal/maintenance -count=1
```

A fifteenth reviewer function,
`TestReviewMaintenanceCompilerAdmissionAndInvalidPlanDoNotPublish`, covers busy
compiler admission, immutable cache reuse, invalid-plan rejection, and admission
release. Its execution, a combined rerun of all reviewer tests, and a rerun of
existing maintenance/notification/causal regressions were blocked by concurrent
shared-checkout compilation errors in `internal/services/services.go`:
`SNMPConfig`, `SIPConfig`, `RabbitMQConfig`, `checkSNMP`, `checkSIP`, and
`checkRabbitMQ` were undefined. Those files were left untouched. The eleven
control successes and three concrete finding failures above completed before
that unrelated build break.

The blocked baseline command was:

```powershell
.canary/tools/go/bin/go.exe test ./apps/node-go/internal/canary -run '^TestMaintenance|^TestReview|^TestCausal' -skip '^TestReviewMaintenance' -count=1
```

The parent subsequently reported protocol 6 and an aggregate-capacity pass.
The new `maintenanceViews` readiness gate waits for independently paged revision
proof. The parent has already assigned cache invalidation on later edge arrival
to the implementation agent; it is not an additional independently established
finding here. This reviewer inspected the gate but did not certify its final
edge-arrival behavior. Replaying peer-resource controls against protocol 6 may
require the corresponding revision proof envelope; an awaiting-proof result
must not be mistaken for remediation of M1-M3. No large proof suites were
repeated during the migration.

No claim is made that the final integrated canary suite, the complete repository
suite, container/process proofs, or a race-detector run passed. Existing
aggregate-capacity/retention work is not duplicated by these findings.
