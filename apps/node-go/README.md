# Go headless runtime

The compiled node runs monitoring, local SQLite storage, peer reconciliation,
notifications, management HTTP APIs, and a CLI independently of the optional UI.
The superseded TypeScript implementation is retained only as an ignored local
reference under `.canary/typescript-node-reference`.

From the repository root, using Go 1.27.3:

```powershell
.canary/tools/go/bin/go.exe test ./apps/node-go/...
.canary/tools/go/bin/go.exe build -o .canary/bin/canary.exe ./apps/node-go/cmd/canary
```

Set `CANARY_CLUSTER_TOKEN` and, on first startup, `CANARY_ADMIN_TOKEN` to distinct,
externally generated random tokens (24–256 printable characters). Run
`canary.exe` or `canary.exe serve`. The default listener is `127.0.0.1:3100`.
No default credentials are supplied. The first administrator and node identity
are persisted; later startup can omit the admin bootstrap token. A conflicting
explicit node ID is rejected.

Environment settings retain the reference names:

| Setting | Default |
| --- | --- |
| `CANARY_NODE_ID`, `CANARY_NODE_NAME` | Persisted identity, otherwise generated ID |
| `CANARY_HOST`, `CANARY_PORT` | `127.0.0.1`, `3100`; port `0` allocates an actual ephemeral port |
| `CANARY_DB` | `.canary/canary.db` |
| `CANARY_ADMIN_TOKEN` | Required for first administrator bootstrap |
| `CANARY_CLUSTER_TOKEN` | Required on every startup; separate from management accounts |
| `CANARY_PEERS` | JSON array of `{ "id", "url", "name"? }`; persisted |
| `CANARY_CORS_ORIGINS` | Comma-separated exact HTTP(S) origins; persisted |
| `CANARY_SYNC_INTERVAL_MS` | `1000`; persisted |
| `CANARY_NOTIFICATION_DELAY_MS` | `1500`; persisted, including explicit zero |
| `CANARY_PEER_TIMEOUT_MS` | `2000`; persisted |

For CLI commands set `CANARY_URL` (default `http://127.0.0.1:3100`) and
`CANARY_TOKEN`, falling back to `CANARY_ADMIN_TOKEN`. Commands:

```text
status
monitors
add <json-file>
update <id> <json-file>
delete <id> <revision>
history <id>
accounts
add-account <json-file>
conflicts <id>
resolve <id> <json-file>
groups
add-group <json-file>
update-group <id> <json-file>
delete-group <id> <revision>
group-conflicts <id>
resolve-group <id> <json-file>
```

API JSON follows `packages/shared/src/index.ts` and `docs/api.md`, including
empty arrays serialized as `[]`. Viewer responses omit webhook destinations;
account hashes and credentials are never returned. Peer snapshots replicate
monitoring configuration over separately authenticated peer transport. Account
credentials and raw measurement history stay local. Use HTTPS or a trusted
private network for peer configuration containing webhook credentials.

Groups have arbitrary user-provided names and configured node membership.
`outageMonitorId` names an ordinary monitor. Beyond the child's grace period,
suppression requires fresh down evidence and a successful notification receipt
for the dependency's current outage. A missing or failing dependency webhook
therefore releases child delivery after grace. The dependency's own normal
cluster notification remains eligible. `graceMs` is required, from 0 to 120000;
the dashboard's suggested value is 10000. Unknown, stale, paused, deleted,
conflicting, or cyclic dependencies fail open after the bounded grace period.
Management changes require valid references and reject cycles. Replication can
arrive out of order without dropping monitors or fabricating root causes.

Suppression never replaces observations or local history. Group and monitor
views show current dependency evidence; node reachability is counted separately.
Incidents expose suppression and retain causal runs, aliases, predecessors, and
all-up frontiers. The grace deadline is measured from the child's outage opening;
dependency retries do not reset it. A child still down after dependency recovery
can alert once that deadline has passed. A child which recovered entirely while suppressed does not
replay an old outage/recovery pair. A previously notified child can still send
its recovery while the dependency is down; its repeats remain suppressed.

Monitor and group configuration use explicit revision DAG heads and tombstones.
The SQLite `records` and `history` tables retain the reference schema and record
formats. New groups use category `groupHeads`. Protocol 2 exchanges bounded,
resumable keyset pages containing configuration, latest observations, incidents,
and receipts. Lost responses replay idempotent pages; all causal records remain
available to delayed peers. Upgrade all nodes together because old clients
cannot consume paged responses. Storage, memory, and total sweep work grow with
retained coordination history. See [runtime coordination](RUNTIME.md) for page
budgets, individual-record limits, and the durable large-backlog regression.

Connected senders use staggered candidates and durable success receipts.
Partitions prioritize delivery and can produce duplicates; receivers should
honor `Idempotency-Key`. Failed sends retry exponentially in five-attempt bursts
with a minute cooldown. Receipts, retry timers, and keys survive restart.

`internal/canary.New(Options)` exports `Start`, `URL`, `Done`, `Error`, and
idempotent `Stop`. Options support injected clocks, probes, and HTTP clients.
Injected I/O must honor request contexts. Production HTTP probes read headers
only, bound redirects/timeouts, and refuse HTTPS downgrade redirects. Ping uses
fixed direct-process arguments and bounded output; install `iputils` in Alpine.
State and persistence are serialized; probe/provider/peer I/O runs outside the
state lock and is cancelled at shutdown. Edited probes cannot save stale results.

`modernc.org/sqlite v1.61.0` is pinned with module sums. No CGO is needed:

```powershell
$env:CGO_ENABLED='0'
$env:GOOS='linux'
$env:GOARCH='amd64'
.canary/tools/go/bin/go.exe build -o .canary/bin/canary-linux-amd64 ./apps/node-go/cmd/canary
```

Tests cover real HTTP targets and webhooks, three HTTP listeners, partitions,
provider isolation, successive incidents, persistent state, retries/repeats,
RBAC, validation, HTTP bounds, actual loopback ICMP when `ping` is installed,
configuration conflicts/tombstones, and dependency suppression/recovery.
The race detector additionally requires CGO and a supported C compiler.
Docker image execution and the independent multi-process proof are validated
outside this runtime package.
