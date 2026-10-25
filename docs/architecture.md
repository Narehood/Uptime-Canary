# Uptime-Canary architecture and first milestone

## Accepted product requirements

- Independent headless Docker nodes own monitoring, their local database and history, incident handling, notifications, peer coordination, and an authenticated management API.
- A separate, optional web container provides user interaction. Its absence or failure must not interrupt monitoring or notification delivery.
- Monitor configuration can be placed on this node or selected peers. Stable monitor IDs identify a shared monitor; observing-node IDs are diagnostic context.
- Coordinate notifications during connected operation. Prioritize delivery during partitions, accepting duplicate alerts and ambiguous-send retries rather than blocking on a majority.
- Reconcile incident and notification state after reconnecting, including recovery and repeat notifications. Do not replicate raw historical measurements.
- Configuration has explicit revisions and visible conflicts; do not silently overwrite concurrent edits.
- Distinguish a location-specific failure from a service failing at every observed location, and show stale or missing observations as unknown.
- Support multiple accounts, with authorization enforced by the runtime API.
- Go for the headless runtime, React + TypeScript for the optional dashboard, and SQLite for local persistence. This supersedes the initial all-TypeScript prototype choice.
- Alpine Linux for container operating systems, including build and runtime stages.
- First working milestone: HTTP/HTTPS and ICMP ping checks, webhook notifications, and evidence for standalone operation, connected deduplication, sender failover, partition delivery, and reconciliation.
- Claude Opus 5.5 with xhigh reasoning owns dashboard design and all visuals.
- Groups are defined by users, including their names and membership. Support grouping related failures into one meaningful outage notification rather than notifying for every dependent check. No sites, host names, providers, or group topology are hard-coded.
- The release goal now extends beyond that first milestone: monitoring and alert capabilities near Uptime Kuma. Discord and email (SMTP, Mailgun, Brevo and Mailchimp Transactional) are release priorities; SMS is deferred.
- MIT is the chosen project license.

## Implementation choices for the prototype

These are reversible engineering defaults, not additional user requirements.

- A Go service using standard-library HTTP APIs and a pure-Go SQLite driver, with React/TypeScript development and independent process proof tests in npm workspaces. The superseded TypeScript runtime is retained only as a local reference under the ignored `.canary/` directory; it is outside the application workspaces and container images.
- Explicit peer URLs over an existing private network; host VPN is recommended, and no specific VPN product is embedded.
- Authenticated HTTP peer synchronization of bounded, resumable pages containing configuration, latest observations, incidents, and notification receipts. Historical coordination records remain available to delayed peers; raw measurement history stays local. Peer transport and management authentication are separate.
- Deterministic, staggered notification candidates with successful-send receipts. This is delivery-first coordination, not consensus or a universal exactly-once guarantee.
- The initial outage policy is conservative: any fresh down observation opens an incident. Recovery requires all assigned nodes to have fresh up observations. Missing nodes remain unknown. Policy configurability is a later product decision.
- Local accounts have admin/editor/viewer roles, API tokens and optional password login with TOTP. Browser sessions stay in memory and are revocable. Accounts are not peer-replicated. These authentication mechanisms are implementation choices described in [authentication](authentication.md).
- All container stages use Alpine-based images. The headless runtime runs a compiled Go executable and installs Alpine's `iputils` package for ICMP probes; the optional static web server also uses an Alpine image.
- Monitor edits require the current revision. Concurrent distributed changes are retained as explicit conflicts requiring operator resolution. Deletions use tombstones.
- Metadata and scheduling edits retain an uninterrupted incident and its notification identity. Changes to what is checked, placement, pause/resume, and deletion end the former check generation administratively, without inventing a recovery. Fresh observations always belong to the exact current configuration revision. These lifecycle rules are implementation choices documented in [runtime coordination](../apps/node-go/RUNTIME.md).
- Container dashboards own their accounts and a shared node registry. The browser reaches only the dashboard server; it authenticates users and contacts saved private node origins with per-role delegations. Node monitoring, incidents, delivery and HA remain autonomous. Dashboard account storage and retained encryption/identity material are separate from node state. Static/development hosting retains direct-API mode; dashboard HA is not implied.
- Groups have user-defined node membership and monitors can select a group. A group can optionally name an ordinary monitor as its outage dependency. Beyond the child's grace period, suppression requires fresh dependency-down evidence and successful notification delivery for that dependency's current outage. Observations and incidents remain visible. The dependency's own notification becomes the grouped outage notification. No automatic guess distinguishes internet, power, VPN, or equipment failure.
- Protocol 11 added optional parent groups and a separate read-only current-health rollup. Parent links organize health without inheriting suppression or maintenance selection. Missing proof, concurrent conflicting placements or cycles show unknown health for the affected connected component while probes and notifications continue. See [group hierarchy](group-hierarchy.md).
- Dependent alerts wait a configurable short grace period, measured from the child's outage opening, to let dependency evidence and delivery arrive. Unknown, missing, deleted, paused, or conflicting dependencies, absent webhook destinations, and unsuccessful dependency delivery fail open after that deadline. If a dependent check remains down after the parent recovers, it notifies; if it recovered entirely while suppressed, it does not replay a stale outage/recovery pair. Cyclic dependencies are rejected. This is the initial configurable grouping mechanism, not a hard-coded site model.
- Reusable notification channels select Discord, JSON webhooks or email providers. Each named destination, including each email recipient, has independent receipt/retry state. Group suppression requires successful parent delivery to that named destination. [Notification behavior](notifications.md) describes compatibility with the original single webhook field.
- The current runtime additionally integrates TCP, TLS, DNS, WebSocket and SMTP checks, plus Docker, Redis, PostgreSQL, MySQL, MQTT, SFTP, MongoDB, SQL Server, NTP, gRPC, SNMP, RADIUS, SIP OPTIONS, RabbitMQ, Kafka and Oracle engines. Working protocol 12 adds Globalping remote HTTP, ping and DNS measurements. Their options are sensitive replicated configuration. This is implemented scope, not a claim of full Kuma parity; the remaining inventory is tracked in the [parity audit](parity-audit.md).
- Maintenance resources select explicit monitors/groups or all monitors. Checks and history continue while scheduled windows hold new notifications. Whole planned episodes avoid stale replay; previously owed alerts survive. These are implementation choices in [maintenance](maintenance.md).
- Local history accepts time windows and exposes explicitly sample-weighted statistics. Authenticated Prometheus gauges preserve viewing-node and unknown/pending context. [Observability](observability.md) distinguishes these from time-weighted uptime coverage.
- A separate local observed-time archive now records original validity intervals, unknown gaps and maintenance annotations transactionally. Its API returns known duration, coverage and availability over retained intervals, rather than treating the existing sample ratio as elapsed uptime. See [availability history](availability-history.md).

## Boundaries

Raw history remains local. Peers exchange only the latest relevant observations and operational records. Webhook credentials are sensitive configuration and should travel only over trusted private networks or HTTPS. No central database, required web service, shared storage volume, or quorum authorizes monitoring or alerts.

The first milestone proves the runtime and supplies an optional dashboard. It does not claim production-ready automatic membership, certificate issuance, account replication, arbitrary integrations, configurable voting policies, or guaranteed exactly-once delivery.

Working coordination protocol 12 pages requests and responses under 1 MiB and resumes interrupted sweeps. Revision edges and incident evidence travel as independently paged facts, with bounded commitments in mutable records. Incomplete configuration proof cannot execute that configuration; incomplete historical incident proof remains non-authoritative while fresh local monitoring and independently witnessed notification obligations continue. All nodes must be upgraded together: older protocol inputs are rejected before configuration is merged. Coordination records are retained rather than compacted, so storage, memory, and catch-up work grow with history. Individual-record and topology limits still apply; see [runtime coordination](../apps/node-go/RUNTIME.md) and [causal capacity repair](capacity-repair.md). Current protocol-12 development is not covered by historical protocol-9, protocol-10 or protocol-11 checks.

## Source context

The complete original design conversation is [Design High-Availability Monitoring](t3-thread://v1/8a8301d0-aff9-4070-8905-7aff14188436). This workspace thread carries forward those requirements and the subsequent decisions about optional UI, TypeScript, and ping checks.
