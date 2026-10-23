# Management API contract

Management requests require `Authorization: Bearer <account token or password session>`. The public authentication status/login endpoints are described in [authentication](authentication.md). Public `/healthz` returns only liveness. Peer synchronization uses a separate cluster credential and must not accept management tokens. Errors are JSON `{ "error": "message" }` with appropriate 4xx/5xx status. The management body ceiling is 256 KiB; identity endpoints have their own smaller limit.

- `GET /api/overview`: `Overview` from `packages/shared/src/index.ts`.
- `GET /api/monitors`: array of `MonitorView`.
- `POST /api/monitors`: `MonitorInput`; returns `Monitor` with generated stable ID and revision (201). Admin/editor.
- `PUT /api/monitors/:id`: `MonitorInput` plus `revision`; returns `Monitor`. Stale revision or unresolved conflict is 409. Admin/editor.
- `DELETE /api/monitors/:id`: JSON `{ "revision": "current-revision" }`; writes a tombstone (204). Admin/editor.
- `GET /api/monitors/:id/history?limit=100`: array of local `Observation`; newest first, bounded limit.
- `GET /api/monitors/:id/statistics?from=TIMESTAMP_MS&to=TIMESTAMP_MS`: local sample counts and latency statistics.
- `GET /api/metrics`: authenticated Prometheus text gauges; viewers can scrape.
- `GET /api/monitors/:id/conflicts`: candidate `Monitor` revisions.
- `POST /api/monitors/:id/resolve`: `MonitorInput` plus `revisions: string[]` naming all observed conflicting heads. Admin/editor; reject stale conflict resolutions (409).
- `GET /api/accounts`: array of `AccountView`. Admin only.
- `POST /api/accounts`: `{ "name": "operator", "role": "viewer", "token": "chosen-private-token" }`; returns public `AccountView` (201). Admin only; never returns stored token/hash.

Password accounts, account updates/deletion, token rotation, password reset and
TOTP enrollment are specified in [authentication](authentication.md). Reusable
channels, independent destination outcomes and test notifications are specified
in [notifications](notifications.md).
Planned work, schedule CRUD and alert holds are specified in [maintenance](maintenance.md).
Time filters, sample statistics and Prometheus semantics are in [observability](observability.md).
Local backup commands and restore activation with separately retained identity
state are described in [backups](backups.md). Lost identity state still requires
a fresh node identity and explicit re-enrollment.

Monitor types currently integrated are `http`, `ping`, `tcp`, `tls`, `dns`,
`websocket`, `smtp`, `docker`, `redis`, `postgres`, `mysql`, `mqtt`, `sftp`,
`mongodb`, `sqlserver`, `ntp`, `grpc`, `snmp`, `radius`, `sip-options`, `rabbitmq`,
`kafka-producer`, `oracledb`, `manual`, and `push`.
The shared TypeScript contract defines `http`, `tls`, `dns`, `websocket`, `smtp`
and `service` option objects. Service options select exactly one matching
protocol. SQL DSNs, query text, credentials and key material stay out of public
targets, probe messages and viewer configuration. Docker's starting/restarting
state is `pending`: it does not authorize down or recovery notifications.
HTTP status and certificate validity metadata are optional observation fields.
JSON response matching is a bounded selector subset, not JSONata.

Passive monitors use `push: {token, allowGet?}` and the normal `intervalMs` as
their heartbeat deadline. The required private token is 64 lowercase hex
characters with the same minimum diversity check as other credentials. Clients
submit `POST /api/push` with `Authorization: Push <token>` and JSON
`{status:"up"|"down",msg?,ping?}`; Bearer credentials do not authorize it.
Optional URL-based GET submission requires explicit opt-in. `ping` is a supplied
latency in milliseconds, not a network RTT measured by the node. Configuration
metadata edits retain the generation's `pushStartedAt` anchor; changing the
deadline, credential or acceptance policy starts a new generation and grace.
See [push-monitors.md](push-monitors.md) for causal HA behavior, limits and the
exact wire/ingress contract. Independent review and release validation remain
separate from the passing source tests and development process proof.

ICMP accepts `ping: {count, size, family}`: count 1–10 (omitted/zero means 1),
payload size 0–65500 bytes (omitted means 32), and family `auto`, `ipv4` or
`ipv6`. The whole timeout covers every packet. At least one verified reply
permits up; local execution/capability failures remain unknown and cannot recover
an outage. `latencyMs` is measured RTT, with optional `latencyAvailable:false`
when no RTT was obtained; its numeric compatibility zero is not a measurement.
Statistics report `latencyCount` and exclude unavailable RTTs from averages and
percentiles. Manual observations also have no measured latency. See the
[ICMP engine contract](../apps/node-go/internal/icmp/README.md) for platform limits.

Kafka requires an explicit `service.kafka.mode`: `metadata` checks broker metadata;
`produce` submits the configured topic/message and requires acknowledgement. Each
assigned node can append a message on each check. Oracle uses `service.oracle`
with a private `oracle://user:password@host:port/service` DSN and SELECT query
(default `SELECT 1 FROM DUAL`). Oracle checks automatically run in a disposable
subprocess with deadlines, bounded output and OS resource limits. Actual Alpine
execution of that boundary remains pending; Windows process startup can exceed
the configured wall-clock budget under load. See the
[worker contract](../apps/node-go/internal/probeworker/README.md) and
[service engine contract](../apps/node-go/internal/services/README.md)
for options, fixtures, and exact differences from Kuma.

All monitor types accept `retries` (0–100, default 0) and optional
`retryIntervalMs` (100–86400000; defaults to `intervalMs`). Timeout must fit both
intervals. Inversion is applied before retry counting. Failures within the retry
budget are `pending`; a protocol-level pending result breaks the failure streak
without reporting recovery. Confirmed failures continue using the retry interval.
The node persists retry progress with its local observation/history transaction;
it is not a replicated observation from another node. Changing `retries` changes
the check generation; changing the retry interval preserves it.

Manual monitors accept optional `manual: {state, message}`. `state` is `up`,
`down` or `pending`; omission means pending with no status set. Target is
canonicalized to `manual`. Admin/editor updates use the ordinary revision-checked
monitor API and replicate to assigned nodes. Status/message edits keep the same
incident generation, so setting up can recover an existing manual outage;
pending does not report recovery. No network request is made. Shared retries,
inversion, maintenance and notification rules still apply. Dashboard controls
for this type remain pending.

User-configured groups use the same revision/conflict rules as monitors:

- `GET /api/groups`: array of `GroupView`.
- `POST /api/groups`: `GroupInput`; returns `Group` (201). Admin/editor.
- `PUT /api/groups/:id`: `GroupInput` plus `revision`; returns `Group`. Stale revision or unresolved conflict is 409.
- `DELETE /api/groups/:id`: `{ "revision": "current-revision" }`; tombstone (204). Dependent monitors remain visible; missing group policy fails open.
- `GET /api/groups/:id/conflicts`: candidate `Group` revisions.
- `POST /api/groups/:id/resolve`: `GroupInput` plus `revisions: string[]` naming all current heads; stale resolution is 409.

Groups have arbitrary names, node membership, a configurable `graceMs` (0–120000; UI default 10000), and optional `outageMonitorId` referencing an existing monitor. Monitor input optionally accepts `groupId`. A dependency monitor is never inhibited by its own group. Reject dependency cycles; unknown or conflicting dependencies fail open after the grace interval. Group and incident views expose suppression and its reason; history continues to contain actual check results while notifications are suppressed. Grouping does not infer whether a connectivity failure is caused by internet, power, VPN, or host failure.

Group suppression must retain delivery-first behavior: a dependency without a notification destination cannot indefinitely silence children that have a working destination. If dependency delivery is unavailable, dependent notifications remain eligible after the grace interval rather than hiding every alert.

Monitor configuration revisions and incident lifetimes are separate. Renaming, regrouping, changing the check interval or repeat interval, and editing a webhook preserve an uninterrupted incident and its delivery evidence. Every observation must still match the exact current configuration revision. Changing the target, check type, expected statuses, timeout, assigned nodes, enabled state, or deleting the monitor starts a new check generation. Open ancestor incidents close administratively, with `retiredBy` and `retirementReason` in `IncidentView`, without an up observation or recovery notification. `resolvedAt` therefore means the incident ended; a retirement marker distinguishes configuration closure from observed recovery. `checkGeneration` on returned monitors is runtime-owned and cannot be set by management input. See [runtime coordination](../apps/node-go/RUNTIME.md) for conflict and restart behavior.

For private HTTPS services or private peer endpoints, Go 1.27 supports `SSL_CERT_FILE` and `SSL_CERT_DIR` to select trusted CA certificates, including on Windows. These override platform trust handling; include every CA the deployment needs rather than assuming the bundle is appended to system trust. Do not disable certificate verification. Node.js-specific certificate environment variables are not the Go runtime's configuration interface.

Successful notifications POST JSON with monitor/incident identity, notification kind, sending node identity, observations, and an `Idempotency-Key` header stable across retries of a persisted attempt. Independent senders can use different keys before their incident aliases converge, so receiver idempotency cannot guarantee deduplication across every failover or partition. Receipt IDs are exposed in overview; destination URLs and account tokens must never appear in peer error responses or logs.

Monitor input validation bounds intervals, timeouts, history sizes, URL schemes, HTTP statuses, and placement. Ping accepts a hostname, IPv4, or IPv6 address, not shell fragments or command switches. A configured peer has a stable ID and explicit base URL. Public service checks follow ordinary host routing; peer traffic follows the configured private peer URLs.
