# Fixed-workload capacity and recovery proof

The release objective requires evidence about operation under load, beyond
individual monitor and notification fixtures. `apps/proof/test/capacity.test.ts`
adds a fixed workload against two independently running Go/SQLite nodes. It is
not a maximum-capacity benchmark or a promise about arbitrary retained history.

## Workload and assertions

- 64 HTTP monitors, assigned to both nodes: 128 observing slots, one-second
  intervals and 500 ms probe timeouts. All targets are private loopback fixtures.
- Two independent webhook destinations per monitor. One accepts immediately;
  the other delays each response by 75 ms and returns HTTP 429 with a four-second
  Retry-After until explicitly released by the fixture.
- All observing slots must reach up, then down. Every fast destination must
  accept while the other destination remains rate limited.
- Twenty-four authenticated overview/metrics reads run during the backlog.
  Each has a ten-second abort deadline. The proof records P95 and maximum
  latency; these observations are not an SLA or an adaptive pass threshold.
- Both node processes are killed and restarted against their own private
  databases. Persisted failed attempt identities, failure counts and retry
  deadlines must survive. No migration or graceful receipt handoff is used.
- The second destination is released. Every pending down must drain, followed
  by a separate accepted recovery for each destination after probes return up.
- Each node is configured to retain at most eight raw samples per monitor.
  Histories must stay local and respect that cap while probes keep running.
- After several election/sync windows, each monitor/destination pair must have
  exactly one accepted down and recovery. Retries and restart must keep the
  same delivery key. Partition duplicates are outside this connected scenario.

The diagnostic reports workload size, API latencies, outage-to-fast-acceptance
time, backlog drain time, accepted notifications and provider concurrency. The
history cap concerns raw measurements; it does not retire causal incident,
configuration, receipt or proof history.

## Execution and storage

Run one command through the shared bounded runner:

```text
python scripts/bounded_validation.py --timeout 480 -- python -B scripts/process-proof-validation.py --test capacity.test.ts
```

The existing proof runner builds one disposable production binary and creates
fresh private SQLite databases under the guard's temporary directory. The
guard enforces the repository, scratch and shared-cache budgets and removes
its own outputs after the command. No user database, `.env`, external provider
or paid measurement is involved. No persistent VM, copied source tree or
private build cache is created.

## Evidence and limits

The first Windows run failed before the backlog phase. Source fingerprint
`e0c190ab53133b91020f2d119a169ac587fea2c16fe05d5142ef00f529465ed3`
passed workspace typecheck, then the process proof failed after 48.621 seconds:

```text
Timed out after 45000ms waiting for all 128 observing-node slots are down
Last condition error: The operation was aborted due to timeout
```

The fixture recorded 1,899 target requests and recent peer proxy responses
including HTTP 502. Both node processes were still alive. These observations
show a failed liveness gate; they do not identify the cause or prove that all
node observations were down. Backlog, restart and later recovery assertions
were not reached and are not counted as passes. The guard completed in 95.843
seconds with unchanged source, scratch peak 187,180,873 bytes cleaned to zero,
and no shared module-cache growth. The original workload and deadlines remain
unchanged while an isolated runtime profile investigates the cost.
The initial run uses the merged protocol-16 baseline on a separate validation
branch while the Steam protocol-17 PR is in hosted review. Any later result
must identify its exact source and platform; it cannot be silently attributed
to another branch or tagged release.

This workload does not establish sustained multi-day behavior, memory bounds,
disk-space reclamation, arbitrary peer counts, long retained-proof antichains,
maximum monitor count, maximum safe polling rate, queue fairness under every
ordering, or throughput for expensive SQL/JSONata/remote probes. Those remain
distinct capacity and operating-limit questions. Wire-page limits and capped
probe workers alone do not bound total node history or reconciliation work.


### Investigation in progress

The isolated evaluation fixture completes 12 full evaluations of 64 outages
without destinations in about 104 ms; 32 validated peer sweeps take about
552 ms. Adding the two configured channels increases those observations to
about 260 ms and 1.148 s. The latter CPU profile attributes substantial work to
JSON encoding/decoding and notification-target construction. These are scoped
profiles, not end-to-end acceptance or a confirmed complete root cause.

A candidate change evaluates only the changed monitor after a sample commit,
while the existing scheduler tick still evaluates every monitor. The unchanged
process proof on source
`7157aa8a52bb2042637e5a12375d16e39465a1c579f29c95ed76188e2860ece5`
**still fails** at the same 45-second down-observation gate. It records 3,505
target requests, but the later assertions remain unexecuted. This candidate is
not called repaired or ready. Guard time was 125.609 seconds, source unchanged,
scratch peak 471,400,738 bytes cleaned to zero and no module growth.

`BenchmarkCapacityLiveRuntime`, run explicitly with `-benchtime=1x`, adds a
23-second profile of live local probes, peer HTTP and destination scheduling.
It is diagnostic only; the process proof remains the liveness authority.


The scoped-reconciliation candidate on source
`93b0a28dce3df59555f8283eb9691cdfc9eb5cccc18fd9472e12922bc91d8ad6`
clears the initial down-observation gate, both destination assertions, the 24
API reads and persisted-attempt checks after restarting both processes. It then
fails the original 45-second backlog-drain assertion (118.312 seconds in the
proof; 195.937 seconds guarded). Later history/recovery assertions were not
reached. Scratch peaked at 508,090,082 bytes and returned to zero; source and
modules were unchanged.

The original new fixture's drain budget conflicts with the accepted existing
retry policy: `notifications.go` schedules every fifth failed attempt 60 seconds
later, independently of a shorter provider Retry-After. A provider becoming
healthy does not cancel that persisted deadline. The fixture will be corrected
to allow that existing backoff plus the original 45-second processing window,
without changing production retry policy, workload or earlier phase deadlines.
The failed fixture is retained in Git before that correction; this failure is
not relabelled a product pass. The final report must distinguish this fixture
correction from the production evaluation/reconciliation repairs.
