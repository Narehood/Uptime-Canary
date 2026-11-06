# Additional computation efficiency

This pass improves three calculation paths outside the delivery log and the
five paths improved in PR #36: central fleet deployment association, planned
maintenance holding-monitor lookup, and registered-node state summaries.
The baseline is merged PR #36, `84dffd43663f91f4a1b43204de6de5a18fdaa318`.

## Measured results

Windows x64, Node 24.22.1. Times are median batch elapsed time divided by the
identical iteration count, expressed as milliseconds per calculation. Every
primary workload exceeds the requested 25% improvement.

| Calculation | Fixture | Before ms | After ms | Elapsed time reduction |
| --- | --- | ---: | ---: | ---: |
| fleet inventory with placement association | 16 registrations, 200 monitors per node, 200 deployments with 16 placements | 161.603600 | 14.667900 | 90.92% |
| maintenance holding monitors | 200 monitors, 64 visible schedules, two holds per monitor | 0.207831 | 0.025292 | 87.83% |
| registered-node summary | 64 registrations across all eight registry states | 0.003692 | 0.001205 | 67.35% |

The fleet case includes the **entire** fleet projection, including its original
peer scopes, observations, assessment and final ordering, as well as the new
placement index. It uses independent registrations with distinct physical
monitor IDs per placement. The 16 registrations, 200 monitors per node and 200
deployments are within the server's admission limits (64 registrations, 200
monitors per node and 200 deployments). Maintenance uses 64 schedules and 200
monitors, also within node admission limits. Each registration summary covers
all eight states, including unfinished credential operations.

These are private in-memory calculation fixtures, not live Go/SQLite/network
measurements. Times include constructing the new indexes and looking up all
requested results. They exclude React rendering, network requests and any
benefit from memoization. They do not measure whole-app or human task speed.

## Implementation and preserved behavior

- **Fleet:** Build an inverted map from registration and physical monitor ID to
  deployments, then associate each existing row once. Nested maps keep opaque
  IDs separate. Sets avoid duplicate matches while preserving original row
  order. Removed deployments still consume their rows; source references,
  retiring registrations and removed-placement rules remain unchanged. No
  ownership, health inference or write path changes.
- **Maintenance:** Build holding-monitor buckets for displayed schedules once,
  preserving assessment order and references. Repeated maintenance IDs do not
  duplicate a monitor, while repeated entries in the input remain repeated.
  Zero and one visible schedule use explicit short paths. The list memoizes
  against its actual inputs; timing includes a fresh index every time. This
  projects already-reported holds and never evaluates schedules or alters alerts.
- **Registry:** Accumulate counts in one pass instead of five filtered arrays.
  Attention still comes from the existing state interpreter, including outbound
  connections, automatic local enrollment and rotation errors.

Maintenance's rendered markup is unchanged across the nine supported/missing,
empty and searched fixtures. No layout, style or authored UI text changes.

## Smaller and selective workloads

| Alternate workload | Elapsed time reduction |
| --- | ---: |
| fleet inventory, small | 3.32% |
| maintenance, single visible schedule | -3.51% |
| registry, small | 68.02% |

Negative means slower. The single-schedule case pays a small map/set overhead;
small fleet association does not meet 25%. The primary result is not generalized
to those cases. Indexes add memory proportional to the fetched placements and
matches; no persistent cache or source snapshot is introduced.

## Method and reproduction

The proof reads the exact baseline modules from Git into memory and transpiles
them without editing their logic. Relative imports resolve to unchanged
helpers. It records original blob hashes, four current production hashes and
the proof hash. No baseline source tree is copied or checked out. Git history
must include the baseline; the automation checkout fetches it.

Correctness checks use frozen inputs. Benchmarks use ordinary unfrozen data,
matching application data; exploratory frozen-data timings are not the reported
results. Each workload calibrates a batch near 25 ms, warms for 12 to 40
alternating pairs until recent ranges/drift stabilize, then measures eleven
alternating pairs. Every reported workload passed its warm-up stability check.
The benchmark retains all samples and iteration counts in
[the raw measurement record](additional-efficiency-measurements.json).

```powershell
python scripts/bounded_validation.py --timeout 180 -- node scripts/additional-operations-proof.mjs --benchmark
```

The proof performs 308 exact baseline comparisons, six complete benchmark-workload
output checks, reference/order and
frozen-input checks. Cases include overlapping/duplicate deployments, confirmed
peer scopes, source-only adoption, retiring/removed placements, stale and
identity-mismatched snapshots, opaque IDs, every registry state and maintenance
selection sizes. Existing fleet, fleet-management, registry-maintenance and
dashboard-control-plane regression suites pass, as does web type checking.
A fresh production web bundle builds into guarded disposable output.

CI runs the same proof on Linux and retains `additional-operations-evidence`.
Timing percentages are reported, not used as a noisy CI pass threshold. Hosted
image/runtime checks are separate evidence; source hashes identify the measured
implementation. No Windows application/test executable, user database or real
external notification is used. These focused checks are not complete Go/SQLite
HA release validation.
