# Protocol 6 validation checkpoint

Recorded 2026-10-22. These results apply to the frozen source manifest
`6bd11e2676f4ba492a74bae3dbcaa7ac951b58bd370209074c7f861328fe687d`,
stored under `.canary/alpine-validation/windows-race/snapshots/<manifest>/source`.
They cover protocol 6 and 21 public monitor types. Later recovery, backup,
Kafka/Oracle and interrupted-history changes are outside this checkpoint.
This is validation evidence, not release approval.

## Independent processes

The complete TypeScript black-box suite passed **11/11**, with no skipped or
cancelled cases, in **93.936 seconds** using the compiled Go executable
`.canary/bin/canary-proof-protocol6-20261022.exe`. Its SHA-256 is
`923dadbfbaa582cb6d74ddb91fcd8ee254a9a2e8ca7f5326b1b7f689ad5e22c1`.

The suite includes independent process death/restart, connected deduplication,
partition delivery and healing, successive outages, local history, HTTPS and
real ICMP, grouped alerts, metadata edits, provider acceptance and retries,
per-recipient delivery, credential separation and viewer restrictions. Provider
traffic uses local fixtures; no live email or Discord delivery is claimed.
See [the notification proof contract](notification-proof-results.md).

The proof process and temporary-directory checks found no remaining proof
executable or `uptime-canary-proof-*` database directory. The user node and its
database were not used.

## Full native race run

Portable Go 1.27.3 and the verified portable GCC toolchain ran:

```powershell
python scripts/windows-race-validation.py --snapshot .canary/alpine-validation/windows-race/snapshots/6bd11e2676f4ba492a74bae3dbcaa7ac951b58bd370209074c7f861328fe687d/source --label protocol6-21types-20m --timeout 20m
```

The command exited **0**. All five packages with tests passed, with **1,020
passing test/subtest events**, **zero skipped tests**, and **zero race warnings**.
The command package has no tests. Package elapsed times were:

| Package | Seconds |
| --- | ---: |
| canary | 571.807 |
| checks | 2.869 |
| delivery | 2.961 |
| maintenance | 2.958 |
| services | 10.828 |

Evidence, JSON test events, commands and timestamps are under
`.canary/alpine-validation/windows-race/runs/20261022T143511Z-protocol6-21types-20m/`.
The runner verified the source manifest before and after execution; it remained
unchanged. No fixture assertion or individual application/test timing was edited.

The preceding ten-minute run failed at the **overall package timeout** during
`TestPagedRealPeersRetainHistoryAndSynchronizeNewState`. Its stack showed active
snapshot serialization/application; four other packages passed and there were
no individual assertion failures or race warnings. That failed run remains in
`runs/20261022T141607Z-protocol6-21types-fixed/`. The larger overall allowance
permitted the unchanged suite to finish; the first result is not reclassified
as a pass.

## Remaining boundaries

Actual Alpine container verification is tracked separately in
[container validation](container-validation.md). Windows race success does not
establish that older emulated Linux failures were harmless.

A subsequent regression established that receiving an incomplete incident
header and then losing the peer can stop independent local checks under
protocol 6. That repair introduces new causal evidence and a protocol 7
boundary. This checkpoint does not establish liveness for the interrupted-proof
case or validate later source changes. A coherent final source needs new
process, race, container and dependency validation after integration.
