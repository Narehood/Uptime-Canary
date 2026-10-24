# PM2 monitors

PM2 monitoring is in development on `feature/pm2-monitor`. It adds a local
process check to the independent Go runtime. This is not a release approval or
a claim that all remaining Uptime Kuma features are complete.

## Configuration and state

Use `type: "pm2"`, put the exact process name in `target`, and supply `pm2`:

```json
{
  "name": "Application workers",
  "type": "pm2",
  "target": "application",
  "pm2": {
    "transport": "unix",
    "home": "/home/application/.pm2",
    "namespace": "production"
  },
  "intervalMs": 10000,
  "timeoutMs": 5000,
  "nodeIds": ["node-a"],
  "enabled": true
}
```

Names and optional namespaces match case sensitively, including internal spaces
and punctuation. Each is 1–256 UTF-8 bytes, without control characters or outer
whitespace. A numeric name such as `0042` remains a name. Namespace omission
matches that name across namespaces. When supplied, the namespace must be
nonempty. This initial implementation does not select PM2 IDs or OS PIDs.

Every matching instance must report `online` for UP. A valid list without a
match is DOWN. A matching instance reporting `stopped`, `stopping`, `launching`,
`errored`, `waiting restart` or `one-launch-status` is DOWN. An unknown selected
status is Pending unless another matching instance has a definite DOWN state.
The whole response must be structurally valid before any state is accepted.
Duplicate PM2 IDs and malformed entries invalidate the response.

An unavailable, unauthorized, unsupported or unresponsive manager is Pending.
So are malformed replies, worker failures and cancellation. Pending neither
opens a new outage nor invents recovery from an existing outage; inversion does
not turn it into a definitive result. Ordinary retries still apply. PM2 results
have no latency sample (`latencyAvailable: false`, compatibility value zero).
Choose a timeout that covers each selected node's manager response time.

Each assigned node asks the daemon on its own machine. Assigning two nodes
therefore checks two local deployments. The process list is not cluster-wide.

## Access and deployment prerequisites

The daemon must already exist. Canary never invokes the PM2 CLI or Node API,
starts a missing manager, subscribes to its event bus, or sends process-control
requests. It sends only fixed `getVersion` and `getMonitorData` RPC calls with
empty object arguments. A manager socket still grants more authority than these
two queries exercise; mounting it does not create a read-only RPC permission.

On Linux, including Alpine, select `unix` and explicitly enter a clean absolute
POSIX PM2 home. The derived `home/rpc.sock` must fit within 107 UTF-8 bytes.
Environment expansion, `~`, backslashes and non-clean paths are refused.
Canary verifies the connected peer's effective UID against its own effective
UID before sending any query. Socket permissions must also allow access.
A container has no automatic access to the host's PM2: explicitly provision
the intended existing socket and a compatible account/UID mapping. Other Unix
platforms are currently unsupported and report Pending.

On Windows, select `windows-pipe` and omit `home`. PM2 uses the fixed local
`\\.\pipe\rpc.sock` endpoint; PM2 home cannot select another daemon. The current
profile requires Windows 10 or later and the same user account as Canary.
Before sending queries, Canary checks the connected pipe type, present owner
and DACL, and the reported server process's token user. Unsupported descriptor
forms, access denial and unexpected thread impersonation fail closed to Pending.
The connection uses identification-level security and cancellable overlapped I/O.

**Windows also has a deployment prerequisite that the probe cannot verify:**
the pipe must have had private instance-creation and security-changing authority
from its first creation through the lifetime of every server instance. Tightening
a pipe that was once permissive does not revoke already granted handles.
Current owner/DACL checks cannot reconstruct that history. Same-account handle
delegation, SYSTEM, administrators and equivalent ownership/token privileges are
inside this trust boundary. Do not treat a passing current descriptor check as
proof of historical isolation. The source review approved this conditional
profile; actual PM2/Node pipe creation and real Windows interoperability remain
unverified. There is no option to bypass account or permission checks.

## Compatibility, privacy and resource limits

The initial compatibility profile admits PM2 **6.0.8** only. Other versions
remain Pending until their protocol behavior is separately researched and
exercised. Custom RPC endpoints and Windows account selection are not supported.
This deliberately differs from Kuma's first matching name-or-ID behavior:
Canary checks all exact name/namespace matches and keeps numeric names literal.

Connection options are private monitor configuration. Viewers and public
projections do not receive the PM2 home, connection type or namespace. The
dashboard masks the home in its default editable JSON/conflict preview and
refuses to import masked placeholders. Process names remain monitor identifiers.
Results contain fixed summaries rather than paths, process metadata, environment
values or raw manager diagnostics.

Queries execute in the existing disposable Go worker with two admission slots,
an overall deadline, bounded output, kill/reap cleanup, sanitized environment,
64 MiB soft heap target and existing platform memory controls. Linux's hard
limit covers data allocations rather than total RSS. No direct query fallback
runs in the long-lived node.

The independent AMP/RPC codec admits two correlated arguments, requests of at
most 512 bytes, version replies of at most 4 KiB and process replies of at most
4 MiB, counting the complete frame. Lists are limited to 4,096 entries and JSON
container depth to 32. UTF-8, surrogate pairs, duplicate decoded keys, correlation,
callback shape and authority fields are checked before accepting state. Additive
environment metadata, including uppercase names, cannot acquire status authority.
`getMonitorData` returns broader process metadata; Canary discards it, but these
client bounds cannot cap PM2's own collection costs.

PM2 options participate in semantic check generations. Metadata-only edits retain
the generation; changes to name selection, transport, home or namespace invalidate
old completions. Configuration and ordinary incident/receipt state use the
existing SQLite and peer machinery. Peer protocol changes from **17 to 18**;
coordinate upgrades across the cluster. No new database format is introduced.

## Source basis and validation record

The comparison baseline is Uptime Kuma 2.5.6,
[`7ca4a3d`](https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/monitor-types/pm2.js).
PM2 behavior was researched at
[`3b3b547` (6.0.8)](https://github.com/Unitech/pm2/tree/3b3b54727dab5acf6b43778314cd26b84ffa94d3),
with independent codec research against
[`pm2-axon-rpc`](https://github.com/Unitech/pm2-axon-rpc/blob/a040e0233649147894e1f5f2b0a24fd02ebb8675/lib/server.js),
[`pm2-axon`](https://github.com/Unitech/pm2-axon/blob/e012e208f82b697fd8c0fa9795602cb3a7c82f54/lib/sockets/req.js),
[`AMP`](https://github.com/tj/node-amp/blob/f48c5bd66b0ec39f03658ba3d427d0161502bb21/lib/encode.js)
and [`AMP message encoding`](https://github.com/tj/node-amp-message/blob/bc46f9855a48d1076d057f0ce12fcb49c850de3c/index.js).
Windows prerequisites follow Microsoft's
[pipe security](https://learn.microsoft.com/en-us/windows/win32/ipc/named-pipe-security-and-access-rights)
and [security object-type API](https://learn.microsoft.com/en-us/windows/win32/api/accctrl/ne-accctrl-se_object_type).

Local Windows amd64 evidence on 2026-10-24:

- Source v3 `cf506b90…`: PM2 package passed, including independent codec controls,
  private named-pipe mechanics and descriptor policy. The guarded run took
  24.157 s, peaked at 83,909,455 scratch bytes and cleaned scratch to zero.
- Source v3 `869c5dfa…`: all three type checks and 84 pure groups passed
  (16 PM2, 17 Steam, 12 system-service, 12 frontend review, 15 public
  availability, 12 capacity proof). This is static/editor evidence.
- Source v3 `e15406c8…`: PM2 package passed with the actual resource-limited
  disposable worker against a unique private Windows pipe. Controls cover
  online, stopped, unknown and malformed replies, cancellation, reaping and
  subsequent admission. The initial worker test failed because descriptor
  retrieval selected the file security provider; using `SE_KERNEL_OBJECT`
  repaired it. The original state expectations remain. The private endpoint
  test seam is rejected by the production worker decoder.
- Source v3 `dada58e7…`: the complete PM2, process and probeworker suites
  passed. The cancellation control occupies both admission slots, cancels
  one worker and requires its replacement to run while the other stays busy.
- Source v3 `ac825821…`: the full core `canary` package passed 1,028 test
  events with zero failures and two expected Windows filename skips; vet
  passed for the changed packages. This includes exact version-metadata
  admission, stale completion after a connection edit, and Pending/inversion
  through an outage. The metadata case-alias regression failed before repair.
- Source v3 `196fc73e…`: all three type checks and 86 pure groups passed
  after the editor follow-ups. A separate, disposable install of 137 locked
  fixture-package resolutions matched all 19 pinned PM2 core files to their
  raw Git bytes. Scripts were disabled; PM2 was not imported or started.
- Source v3 `26d04f9a…`: the production dashboard build and all 156 API/browser
  contract checks passed with zero browser exceptions. The collaborative
  preview twice timed out, so the documented Chrome fallback used one guarded
  disposable profile. Sampled scratch peaked at 509,368,449 bytes and was
  cleaned to zero; modules did not grow.
- Source v3 `caa3fe44…`: the production PM2 process proof passed 1/1 on Windows,
  including peer persistence/restart, metadata and semantic edits, actual
  partition/conflict/complete-head resolution, and viewer redaction. It uses
  an unsupported Unix profile on Windows and asserts Pending; it never queries
  a user's fixed pipe. Its positive manager-state cases require Linux.
- Final source v3 `8a0c50dd…`: three type checks and the same 86 pure groups
  passed after preserving a hidden home on a future transport during JSON
  import. The real private HTTP API refused an unpaired-surrogate name and
  admitted a valid paired Unicode name. This targeted check covers the added
  admission case; the 156-check browser result retains its earlier scope.

These checkpoints have different source scopes. The Go production files remain
as tested by the full core run; later proof, fixture and CI changes alter the
whole-source fingerprint. They do not establish actual PM2 daemon interoperability,
Linux, Alpine, arm64, race acceptance, hosted checks or release readiness.

Both Linux native-package jobs require the additional actual-PM2 gate against
their same extracted executable. Its separate locked development-only dependency
is installed below guarded outputs, not added to the product runtime. The
fixture uses the real 6.0.8 RPC server and real cluster workers in disposable
PID, mount and loopback-only network namespaces. Only system programs run before
the account, groups, capabilities and privilege escalation are dropped. An
unchanged-credential system supervisor retains the parent-death relationship;
the child subreaper kills and waits for all remaining private descendants.
A live supervisor-SIGKILL control must leave zero namespace processes.

The gate checks actual online, restart, stopped and removed states, Pending
during RPC loss, fresh Pending after a Canary restart, retained incident identity
and recovery at the same endpoint, and metadata redaction. Namespace prerequisites
or lifecycle cleanup failures fail the job. No actual-PM2 run has passed yet.
The existing 21 ordinary proofs are retained alongside the new PM2 proof (22
total); the full set, existing Alpine/race checks and native arm64 package tests
remain hosted gates. Actual Windows PM2 and live foreign-account rejection have
not been exercised. No tagged release or parity sign-off is claimed.
