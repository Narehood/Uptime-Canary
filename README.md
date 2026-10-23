<h1 align="center"><img src="docs/assets/banner.svg" alt="Uptime Canary" width="100%"></h1>

<p align="center"><b>Self-hosted uptime monitoring that keeps working when parts of it fail.</b></p>

Uptime-Canary runs as a small group of independent, headless Go nodes. Each node runs your checks, keeps its own history in SQLite, opens incidents and sends alerts. Nodes that can reach each other coordinate, so you normally get one alert per outage and destination instead of one per node. Nothing central has to stay up: there is no shared database, no quorum and no required dashboard. An optional React dashboard and public status pages read from any node you choose.

> [!IMPORTANT]
> **In active development. Not release-ready.**
> - **Backend:** the working source is peer protocol **12** (development): **Globalping remote measurements** on top of protocol 11's nested group health, observed-time availability and SVG badges, and protocol 10's HTTP OAuth2 and JSONata. Older peers reject protocol 12, so upgrade all nodes together. On development snapshot `d3e2889e…` the core package's ordinary native tests passed (830 events, 0 failures, 2 expected Windows skips) and a Globalping process proof passed; that is not every package, and there is no race, Alpine or whole-release run for it. **JSONata still has seven known compatibility gaps (40 cases that wrongly report up) that block a release**; candidates based on the official JavaScript runtime remain private and are not adopted. Protocol 11 and earlier checkpoints are historical development evidence.
> - **In progress, protocol 13 (no release binary):** native **Slack, Telegram, ntfy, Gotify and Pushover** channels; peers must upgrade together. On current Windows development source, the core package tests, the delivery package and the native send leaf (26 test groups) passed, as did `go vet` on all three and Linux amd64/arm64 library builds of the leaf and delivery packages. The dashboard forms type-check, pass 8 pure checks, build, and pass a **21/21** browser proof against a freshly built protocol-13 development node with loopback fake providers. No real provider was contacted. The unchanged contract harness, Alpine, race and process proofs and a whole-release run haven't been repeated for protocol 13. See [validation](#checks-and-validation).
> - **Dashboard:** typechecks and builds. Against a private copy of the protocol-12 **development** binary `78a390ec…`: the Globalping forms pass 13 pure checks and a **16/16** browser proof, and the protocol-11 screens still pass **23/23**. The unchanged independent contract harness passes **113/114**; its one failure asserts exactly 25 check types, and Globalping is the 26th. See [validation](#checks-and-validation). None of this is a release approval.
> - **Containers:** all images are Alpine. The separate identity volume passed 11 container checks and 7 migration and restore checks on Alpine with Docker (Compose wasn't available, so equivalent `docker run` commands were used). The current integrated source still needs a full container run.

[What it does](#what-it-does) · [How it works](#how-it-works) · [Quickstart](#quickstart) · [Dashboard](#the-dashboard) · [Checks](#check-types) · [Alerts](#alerts-and-channels) · [Groups](#one-alert-for-a-shared-outage) · [Status pages](#status-pages) · [Several nodes](#run-several-nodes) · [When things fail](#when-things-fail) · [Headless](#manage-it-headless) · [Validation](#checks-and-validation) · [Not yet](#not-yet-implemented)

## What it does

| Area | What you get | Status |
| --- | --- | --- |
| **Checks** | 24 active and operator check types (HTTP, ping, TCP, TLS, DNS, WebSocket, SMTP, gRPC, PostgreSQL, MySQL, SQL Server, Oracle, MongoDB, Redis, Docker, MQTT, RabbitMQ, Kafka, SFTP, NTP, SNMP, RADIUS, SIP OPTIONS, manual) plus passive **push** heartbeats | ✅ push validated at the protocol-9 checkpoint |
| **Remote probes** | **Globalping** ping, HTTP or DNS from a third-party probe picked by a free-form location ("Germany", "AS13335", "aws-eu-west-1"), with remote latency, status, keyword, JSONata and DNS answer conditions | ⚠️ protocol 12, development; third-party service with its own quota; see below |
| **Check policy** | Per-monitor placement on nodes, retries before down, a faster retry interval, result inversion | ✅ |
| **HTTP OAuth2** | Client-credentials grant per check (Basic or form-body client auth, scope, audience, token-endpoint CA), exclusive with other HTTP auth | ⚠️ development (protocol 10+) |
| **JSONata conditions** | Expression, operator and typed expected value for HTTP, MQTT, MongoDB and SNMP, alongside the existing matchers; evaluated by the node in a resource-limited worker | ⚠️ development; **known compatibility gaps block a release**. A Go JSONata port with documented differences, not full JSONata or Kuma parity |
| **Honest states** | Up, down, pending and **unknown**. A node that can't measure (for example, ICMP without permission) reports unknown, never up or down; unmeasured latency shows as a dash | ✅ |
| **Alerts** | Reusable channels: **Discord, SMTP, Mailgun (US/EU), Brevo, Mailchimp Transactional**, JSON webhooks. Every email recipient is its own destination with retries and an outcome | ✅ acceptance only, not inbox tracking |
| **Chat and push alerts** | **Slack, Telegram, ntfy, Gotify, Pushover**: one destination per channel, plain-text title and message, credential rotation that keeps receipts | ⚠️ protocol 13, development; loopback browser proof only, no release binary |
| **High availability** | Assigned nodes take turns per destination; delivery comes first, so partitions can produce duplicates | ✅ |
| **Groups** | Your own groups and outage dependencies: one alert for a shared outage, failing open per destination | ✅ |
| **Nested groups** | Optional parent groups with read-only current health counts (down, unknown, pending, up, paused). Nesting never holds alerts or passes on maintenance | ⚠️ protocol 11, development |
| **Maintenance** | Once, every N days, daily, weekly, monthly or cron, in an explicit IANA time zone. Checks continue; alerts are held | ✅ |
| **Tags** | Reusable colored tags with optional values, filters, and opt-in public display | ✅ |
| **Status pages** | Public, credential-free JSON from every node, rendered by the dashboard. Operator-typed names and announcements only | ✅ no custom domains/CSS/history yet |
| **Badges** | SVG status badges for a published page or one of its services, with share links in the dashboard | ⚠️ protocol 11, development; current state only, no percentages |
| **History** | Local samples with time ranges, sample availability with its denominator, P50/P95 latency | ✅ not an SLA |
| **Observed time** | Time-weighted up, down, pending and unknown durations per bucket from the node's own fresh results, with availability of known time and coverage, for up to 90 days. Gaps stay unknown | ⚠️ protocol 11, development; one node's view, not an SLA |
| **Accounts** | Per-node accounts, admin/editor/viewer roles, passwords, TOTP, revocable sessions, offline recovery | ✅ |
| **Operations** | Prometheus metrics, CLI for everything, live SQLite backup and restore preparation | ✅ restore validation pending |

## How it works

![Architecture: three of about six independent headless Go nodes. Each runs 24 check types plus push, alerts, maintenance and groups, a management API, and its own SQLite database, and reaches monitored services and alert destinations (Discord, SMTP, Mailgun, Brevo, Mailchimp, webhooks) directly. Nodes sync configuration revisions, latest observations, incidents, receipts and heartbeats with every listed peer. Raw history and accounts stay on each node. Operators use the CLI or the optional dashboard, one node at a time.](docs/assets/architecture.svg)

- **Every node is complete on its own.** One Go executable with its own checks, incidents, alerts, management API and SQLite file. It keeps working when its peers or the dashboard are gone.
- **Peers share current state, not history.** Configuration revisions and tombstones (monitors, groups, channels, maintenance, status pages, tags), latest observations, incidents, receipts and push heartbeats travel in bounded, resumable pages. Raw history and accounts never leave a node.
- **Monitors run where you place them.** A monitor keeps one stable ID across its nodes, so "down everywhere" and "down from one location" are different, visible facts.
- **Delivery comes first.** Nodes take turns per destination; one that sees another's receipt stands down. If nobody's receipt arrives, the next node sends. Duplicates are possible; missed alerts are what the design avoids.
- **The dashboard is a guest.** It talks to one node at a time, keeps your sign-in in tab memory only, and stores no monitoring data.

## Quickstart

This runs one headless node on your machine. You don't need Docker.

**You need:** Go 1.27.3 or newer (the npm scripts use `CANARY_GO`, then a portable Go in `.canary/tools/go`, then `go` on your `PATH`), and Node.js 24 (24.13 or newer, below 25) with npm for the setup script, CLI wrapper and dashboard.

```sh
npm install
npm run init   # writes a private .env with random tokens; never overwrites an existing one
npm run dev    # builds the Go node and starts it at http://127.0.0.1:3100
```

Then either open the dashboard (below) or use the CLI:

```sh
npm run canary -- status                          # this node, peers, monitors, groups, incidents
npm run canary -- add examples/http-monitor.json  # check https://example.com
npm run canary -- monitors                        # current state, including the new monitor's id
npm run canary -- history <monitor-id>            # this node's latest checks, newest first
```

The CLI reads its token from `.env`. Only `/healthz`, sign-in (`/api/auth/status`, `/api/auth/login`), published status pages (`/public/status/<slug>`) and push heartbeats (`/api/push`, with a push token) work without an account; everything else under `/api/` needs one. There are no default credentials: the admin token in `.env` creates the first admin account on first start.

## The dashboard

```sh
npm run dev:web   # http://127.0.0.1:5173, while npm run dev is running
```

Sign in with the node address (`http://127.0.0.1:3100`) and a password or account token. The token or session stays in the tab's memory; only the last node address and theme are stored in the browser. Everything you see is "as known to" that node, and switching nodes is always your choice.

![Dashboard overview in the dark theme from a local two-node test: the headline reads that Payments API and Redis cache are down everywhere and Release gate is pending from 2 of 2 locations. Tiles count 2 down, 0 unknown, 4 of 8 up (1 paused, 1 pending), 1 of 1 peer in view, 1 notification, 1 of 1 maintenance window holding alerts for 1 monitor, and 0 of 4 groups holding alerts. A matrix shows each monitor's result from fixture-a and fixture-b; Payments API carries a "maintenance: Patch window" tag.](docs/assets/dashboard-overview.png)

<table>
<tr>
<td width="50%"><img src="docs/assets/dashboard-new-monitor.png" alt="New monitor form: check type cards grouped as web and network, remote probes (third party) with Globalping, databases and caches, messaging and containers, infrastructure and RPC, and passive and operator controlled."></td>
<td width="50%"><img src="docs/assets/dashboard-monitor-detail.png" alt="History of a manual monitor whose state was switched by hand during a local test: range buttons with 1 h chosen, then Observed time tiles reading 75.8% observed availability (up 2 min 26 s of 3 min 12 s known), 5.35% coverage and 46 s down. A column chart shows hatched unknown time before the last few minutes of green up columns with red down and gray pending slices. Individual checks read 108 samples (75 up, 25 down, 8 pending) and 75% sample availability. The incident list beside it still shows the newest down incident as open."></td>
</tr>
<tr>
<td><b>Forms for every check type.</b> Essentials first; credentials, queries, TLS and advanced options open on demand. Stored secrets stay masked and are never cleared by accident, and the form warns before a change would close an open incident.</td>
<td><b>History you can trust.</b> Observed time shows how long the node actually saw each state: availability of the known time, how much of the range is known at all, and gaps as unknown, never as up. Samples and latency follow with their own denominators. Neither is an SLA.</td>
</tr>
<tr>
<td><img src="docs/assets/dashboard-deliveries.png" alt="Delivery attempts table: a down alert for Payments API and a channel test through the Ops webhook channel, both sent by fixture-a and accepted with HTTP 204, and a note that acceptance isn't proof of inbox, channel or device delivery."></td>
<td><img src="docs/assets/dashboard-maintenance.png" alt="Maintenance list: a Patch window card marked Active, holding alerts, for a one-time 1 h window shown in UTC and local time, applied to and holding the Payments API monitor."></td>
</tr>
<tr>
<td><b>Every delivery attempt.</b> Per destination: accepted, retrying, held, failed or unknown, with HTTP status, next retry and a plain explanation. Test any channel (it asks first, because it sends a real message).</td>
<td><b>Planned maintenance.</b> Six schedule types in a named time zone. The node computes the windows; the dashboard shows them in UTC and your local time and marks every monitor it holds.</td>
</tr>
<tr>
<td><img src="docs/assets/dashboard-groups.png" alt="Groups page from a local test: a short explanation of grouping and nesting, then two empty top-level groups, Branch office and Lab, marked Health pending, and a Site group marked Health down with a health bar and counts (3 up, 1 down, 1 pending, 0 unknown, 1 paused, 1 in maintenance) for 5 active monitors across the group and its 1 nested group."></td>
<td><img src="docs/assets/dashboard-badges.png" alt="Share dialog for a status page: a Badges section with a served-by node, a loopback-address caveat, a whole-page badge reading status down with maintenance, and single-service badges reading up, down with maintenance, and unknown, each with Markdown and image URL copy buttons; a duplicated public name is listed without a badge."></td>
</tr>
<tr>
<td><b>Nested groups.</b> Put groups inside a parent to see the combined current health of everything below it. A missing parent or a cycle shows as unknown with the reason, never as healthy. Nesting changes no alert or maintenance behavior.</td>
<td><b>Badges and share links.</b> Copy Markdown, HTML or an image URL for a published page or one service. The dialog says which node serves the image, warns about addresses others can't reach, and explains which renames break an embed.</td>
</tr>
</table>

Screenshots show the dark theme in Chrome, signed in to one of two private local test nodes built from development commit `1a7447d`. They illustrate the interface; they aren't release evidence.

Editors and admins manage monitors, tags, groups, channels, maintenance, status pages and conflicts; admins also manage accounts. Viewers get a read-only dashboard without credentials, service settings or channel configuration. Design notes: [docs/design.md](docs/design.md).

## Check types

| Group | Types | Notes |
| --- | --- | --- |
| Web and network | HTTP(S), ping (ICMP), TCP, TLS certificate, DNS, WebSocket, SMTP | HTTP status ranges, keyword or regex, JSON value, JSONata (protocol 10), headers, Basic/Bearer/OAuth2 client credentials (protocol 10), proxy, mTLS; ping count, size and IPv4/IPv6 |
| Databases and caches | PostgreSQL, MySQL, SQL Server, Oracle, MongoDB, Redis | Read-only queries with an optional scalar condition; Oracle runs in a bounded worker process |
| Messaging and containers | Docker, MQTT, RabbitMQ (AMQP or alarms API), Kafka (metadata or acknowledged produce) | |
| Infrastructure and RPC | gRPC (health or unary), SFTP, NTP, SNMP v1/v2c/v3, RADIUS, SIP OPTIONS | SFTP requires a pinned host key unless you explicitly opt out |
| Passive and operator | Push heartbeats, manual status | Push: your job sends `POST /api/push` with `Authorization: Push <token>` before the interval ends |
| Remote probes (third party) | Globalping ping (ICMP or TCP), HTTP (GET, HEAD, OPTIONS) and DNS (16 record types) | Protocol 12, development. One remote measurement per check and node; latency is the probe's own measurement |

All 26 types share retries, a retry interval, inversion, placement, channels, repeat alerts, groups, maintenance and tags. Option details and differences from Uptime Kuma are in the [service engine contract](apps/node-go/internal/services/README.md), the [API](docs/api.md), [push monitors](docs/push-monitors.md) and the [Globalping contract](apps/node-go/internal/globalping/README.md).

**Globalping** (protocol 12, development) asks the [Globalping](https://globalping.io) network to run one measurement from a probe matching your location and reads its result.

![New-monitor form with Globalping chosen: a note that the target, location, path, query and request headers are sent to Globalping and its probe while the API token stays on the nodes, then the Ping/HTTP/DNS choice, the URL, a probe location of Frankfurt+AWS, the method and HTTP/2 options, and the response conditions with the keyword healthy.](docs/assets/dashboard-globalping.png)

- **What is shared.** The target, location and, for HTTP, the path, query and request headers (including an Authorization header) go to Globalping and the probe it picks, which may keep or publish measurement data under their own policies. The optional API token and API CA certificates stay on your nodes; viewers see neither the options nor an HTTP target's path and query.
- **What counts.** Target and resolver failures are down. Authentication, quota, API errors, offline probes and Globalping-internal failures are **pending** by default, because they say nothing about the target; an opt-in treats them as down, as Uptime Kuma does. Latency is the probe's own measurement, never the API round trip.
- **Cost and timing.** Every assigned node creates its own measurement on each check, and each one counts against your Globalping limit or credits, even when a check times out. The check's timeout covers creating and polling the measurement; the form recommends the probe timeout plus 10 s. Uptime-Canary doesn't verify where a probe is.

## Alerts and channels

A channel is a reusable destination; a monitor lists up to 16 of them. Each email recipient is a separate destination with its own retries, outcome and receipt; a chat, push or webhook channel is one destination.

| Provider | Accepted when | Configuration |
| --- | --- | --- |
| Discord | Discord saves the message (`wait=true`) | Channel webhook URL |
| SMTP | The server accepts DATA | STARTTLS (default), implicit TLS, or an unauthenticated plain relay |
| Mailgun | The API accepts the message | API key, domain, US or EU region |
| Brevo | The transactional API accepts it | API key |
| Mailchimp Transactional | Mandrill reports sent/queued/scheduled per recipient | API key (not a Marketing key) |
| Webhook | Any 2xx | URL, optional bearer token, `Idempotency-Key` header |
| Slack *(protocol 13, in progress)* | Slack answers exactly `ok` | Incoming-webhook URL (the URL is the credential) |
| Telegram *(protocol 13, in progress)* | `ok:true` with a message ID for the configured chat | Bot token, chat ID or public @channel, optional forum topic, silent, protected and no-preview switches |
| ntfy *(protocol 13, in progress)* | A message ID for the configured topic | Topic, ntfy.sh or your server, no login, access token or user login, priority 1–5, tags |
| Gotify *(protocol 13, in progress)* | The server returns the created message | Your server URL, application token, priority 0–10 |
| Pushover *(protocol 13, in progress)* | `status:1` with a request ID | Application token, user or group key, optional device and sound, priority −2 to 2; emergency repeats every 30 s–3 h for up to 3 h |

The five chat and push providers send a plain-text title and message, never HTML, with the same placeholders as email templates. Built-in wording that is too long is shortened and ends with “[truncated; see monitor details]”; your own templates are sent exactly and fail as `invalid_message` if they expand past the provider's limit. HTTPS is required unless you explicitly allow plain HTTP for a configured server. A destination follows the channel and its route, not the credential: rotating a bot token, ntfy login or Pushover application token keeps receipts, while a different chat, topic, user key or device is a new destination. Gotify tokens don't identify their application, so alert a different Gotify application from a new channel.

**Accepted means the provider acknowledged the request**, not that a message reached an inbox or a device. Delivery status never includes provider bodies, addresses or secret URLs. Partitions and crashes can produce duplicates. SMS is planned later. Details: [notifications](docs/notifications.md).

## One alert for a shared outage

![Two panels, each with a user-defined group containing an outage monitor and three dependent monitors, all down. Left: the outage monitor's alert is delivered, so the receiver gets one alert and the dependents hold theirs back while still recording. Right: the outage monitor's alert isn't delivered, so after their grace period the dependents alert on their own.](docs/assets/grouping.svg)

Groups are yours to define: names, nodes, and an optional **outage monitor** with a grace period. While the outage monitor has a fresh failure and its alert reached a destination, dependent monitors hold their alerts **to that destination**; other destinations fail open after the grace period. Checks, history and incidents continue throughout. Uptime-Canary doesn't guess whether a failure was power, internet, VPN or the host.

Protocol 11 (development) lets a group sit inside a **parent group** for an overview. A parent's health combines the current results of its own monitors and every nested group (down, then unknown, then pending, then up; an empty group is pending). Parent links don't extend alert holds or maintenance: both still follow each monitor's own group. A missing parent, a cycle or a conflicting version makes that part of the tree unknown until it's repaired. Details: [group hierarchy](docs/group-hierarchy.md).

## Status pages

![A public status page titled Service status: "All systems operational", an Identified announcement called Scheduled work about a database upgrade, and a Web section where the Website service is operational with a "production: eu" tag.](docs/assets/status-page.png)

Every node serves published pages as JSON at `/public/status/<slug>`, without an account, cookie or query secret. Pages contain only what you type: page and section titles, a public name for each selected monitor, announcements, and optional certificate dates and public tags. Private names, IDs, targets, errors and node details are never published, and unknown is never shown as up. The dashboard's status-page editor builds a shareable link such as `https://dashboard.example.com/#/status/services?node=https://canary-1.example.com`; visitors can switch to another listed node if one is unreachable. Uptime bars, custom domains, CSS and subscriptions aren't available yet. Details: [status pages](docs/status-pages.md) and [tags](docs/tags.md).

**Badges** (protocol 11, development). Every node also serves `/public/badge/<slug>.svg` for a published page, or `?group=<section ID>&monitor=<public name>` for one service, with no account or token:

<p><img src="design/badges.svg" alt="Six status badges at actual size: up, down, pending and unknown, then down and up each with an extra maintenance segment." width="640"></p>

A badge shows the current state only: unknown when results are stale, missing or paused, and an extra maintenance segment that never hides a failure. Two services with the same public name in one section get no badge, and changing the page address, a section ID or a public name breaks existing embeds. The dashboard's Share dialog and the public page copy ready-made Markdown or HTML. Details: [badges](docs/badges.md) and [badge design](design/badges.md).

## Deployment options

| Option | Start it with | Open | Status |
| --- | --- | --- | --- |
| **Local, headless** | `npm run dev` | API `:3100` | ✅ |
| **Local + dashboard** | `npm run dev`, then `npm run dev:web` | UI `:5173` | ✅ browser-proved on private fixtures |
| **Docker, headless** | `docker compose up --build -d node` | API `:3100` | ⚠️ images and the identity-volume layout verified with `docker run`; Compose itself and the current source pending |
| **Docker + dashboard** | `docker compose --profile web up --build -d` | UI `:8080` | ⚠️ earlier snapshots verified; current source pending |
| **Portainer stack** | Images built on the Docker host, then the [Portainer stack](docs/portainer.md) | API `:3100`, UI `:8080` | ⚠️ template checked with a Compose configuration check only |

Every port listens on `127.0.0.1` by default. All images are Alpine Linux, build stages included; the node runs as an unprivileged user with `no-new-privileges`, and Compose grants `NET_RAW` only for ping. Each node needs its own volumes: `canary-data` at `/data` for the database and a separate `canary-identity` at `/identity` for identity state. Never share them between nodes, and keep the identity volume when you restore a database. Containers created before the identity volume existed need a one-time, stopped-node copy: see [backups](docs/backups.md).

<details>
<summary><b>Environment variables</b></summary>

| Variable | Default | Purpose |
| --- | --- | --- |
| `CANARY_NODE_ID`, `CANARY_NODE_NAME` | from `init` | Stable, unique node identity, saved on first start |
| `CANARY_ADMIN_TOKEN` | generated by `init` | Creates the first admin account on first start |
| `CANARY_CLUSTER_TOKEN` | generated by `init` | Shared secret for peer sync; same on every node, different from account tokens |
| `CANARY_PEERS` | `[]` | JSON array of `{ "id", "url", "name"? }` for the other nodes |
| `CANARY_HOST` / `CANARY_PORT` | `127.0.0.1` / `3100` | API listener without Docker |
| `CANARY_DB` | `.canary/canary.db` | SQLite path (`/data/canary.db` in Docker) |
| `CANARY_CORS_ORIGINS` | dashboard dev and container origins | Browser origins allowed to call the management API |
| `CANARY_HISTORY_LIMIT`, `CANARY_HISTORY_RETENTION_MS` | `1000`, 7 days | Local samples kept per monitor |
| `CANARY_SYNC_INTERVAL_MS`, `CANARY_NOTIFICATION_DELAY_MS`, `CANARY_PEER_TIMEOUT_MS` | `1000`, `1500`, `2000` | Sync cadence, stagger between senders, peer timeout |
| `CANARY_IDENTITY_DIRECTORY` | `.canary-identities` beside the database; `/identity` in containers | Separately retained identity state (its own `canary-identity` volume in Compose), needed to activate a restore; see [backups](docs/backups.md) |
| `CANARY_URL`, `CANARY_TOKEN` | `http://127.0.0.1:3100`, the admin token | CLI only |
| `SSL_CERT_FILE`, `SSL_CERT_DIR` | system trust | CA certificates for private HTTPS services and peers |

</details>

## Run several nodes

Uptime-Canary is designed for a handful of nodes, about six, on different hosts. You list peers yourself; nodes don't discover each other.

1. Give every host its own `.env` with a unique `CANARY_NODE_ID` (choose it before the first start).
2. Copy the same `CANARY_CLUSTER_TOKEN` to every node.
3. List all other nodes in `CANARY_PEERS`, using private network or VPN addresses, and make the API reachable there.
4. Place monitors on nodes with `nodeIds`.
5. **Upgrade all nodes together.** Nodes reject peers that speak a different protocol version.

Peer sync and the management API use HTTP with bearer credentials and carry configuration that includes alert credentials. Keep them on a VPN or trusted private network, or put HTTPS in front of each node.

## When things fail

![Four panels, each with three nodes and an alert receiver. 1, Connected: node 1 sends one alert and its receipt makes the others stand down. 2, Sender lost: node 2 sends after its stagger, with no quorum. 3, Partitioned: each side sends, so the receiver gets two alerts. 4, Healed: receipts and incidents merge without replaying old alerts, and one recovery is sent.](docs/assets/failure-behavior.svg)

| Situation | What the nodes do | What you receive |
| --- | --- | --- |
| **Everything connected** | The first node in line sends to each destination and syncs its receipt; the others stand down | One alert per incident and destination |
| **The sender dies or can't reach the destination** | No receipt arrives, so the next node in line sends | One alert, a few seconds later |
| **Network partition** | Each side decides and sends on its own | Duplicates are expected |
| **Partition heals** | Receipts, incidents and configuration merge; delivered alerts aren't resent | One recovery per destination |
| **Planned maintenance** | Checks and incidents continue; new alerts are held | Owed alerts after the window, if the outage continues |
| **A node can't measure** | It reports unknown, which never counts as up or down | No false recovery |
| **The dashboard is closed** | Nothing changes | Alerts as usual |

An incident opens on any fresh failure and recovers only when every assigned node reports a fresh success. Editing a monitor's name, interval, destinations, tags or group keeps an open incident; changing what is checked, pausing or deleting closes it administratively, without a recovery alert. The dashboard warns before you save such a change.

## Manage it headless

Run `npm run canary -- <command>` from the repository root. Output is JSON.

| Area | Commands | Minimum role |
| --- | --- | --- |
| Overview | `status`, `monitors`, `history <id>`, `statistics <id>` | viewer |
| Monitors | `add`, `update`, `delete`, `conflicts`, `resolve` | editor |
| Groups | `groups`, `add-group`, `update-group`, `delete-group`, `group-conflicts`, `resolve-group` | editor to change |
| Channels | `channels`, `add-channel`, `update-channel`, `delete-channel`, `channel-conflicts`, `resolve-channel`, `test-channel`, `deliveries` | editor to change |
| Maintenance | `maintenance`, `add-maintenance`, `update-maintenance`, `delete-maintenance`, `maintenance-conflicts`, `resolve-maintenance` | editor to change |
| Status pages | `status-pages`, `add-status-page`, `update-status-page`, `delete-status-page`, `status-page-conflicts`, `resolve-status-page` | editor to change |
| Tags | `tags`, `add-tag`, `update-tag`, `delete-tag`, `tag-conflicts`, `resolve-tag` | editor to change |
| Accounts and data | `accounts`, `add-account` (admin); `backup-database`, `prepare-restore`, `recover-account` (local file access) | varies |

Edits carry the revision they change; a stale one is rejected with HTTP 409. Concurrent edits made while nodes were apart are kept side by side as a visible conflict until someone resolves it, in the CLI or field by field in the dashboard's Configuration page. Prometheus metrics are at `GET /api/metrics` with a viewer credential ([observability](docs/observability.md)).

## Checks and validation

```sh
npm run typecheck   # TypeScript: shared types, dashboard, proof suite
npm run vet         # go vet
npm test            # native Go tests
npm run test:proof  # builds the Go node, then runs the multi-process proof suite
npm run build       # Go node and dashboard
```

What has actually been run, and on which source. Rows marked *historical* ran on older sources and don't certify the current one.

| Evidence | Scope | Result |
| --- | --- | --- |
| Protocol 12 development snapshot (`d3e2889e…`, binary `78a390ec…`) | Ordinary native tests of the core package; a Globalping process proof (exact remote RTT, provider 429 as pending, two nodes with one down, restart without resend, one recovery) | Core package 830 test events, 0 failures, 2 expected Windows skips (158.0 s); Globalping proof 1/1 (18.4 s). Reported by the backend owner. Not all packages; the 15 process proofs, race, Alpine and whole-release runs were not repeated for protocol 12 |
| Protocol 13 development source (Windows) | Ordinary native tests of the core package, the delivery package and the native send leaf; `go vet` on those three packages; Linux amd64/arm64 library builds of the leaf and delivery packages | Core package 143.6 s, delivery 1.9 s, send leaf 26 test groups 1.4 s, all passed; vet and both builds passed. Reported by the backend owner. The first combined command exited 1, but only on a dashboard typecheck error; that failure stays on record and was fixed in the dashboard source. Not a race, Alpine, process-proof or whole-release run |
| Dashboard chat and push channel forms | Workspace typecheck, 8 pure checks of the form builders (exact round trips, Go-omitted defaults, Pushover emergency bounds, ntfy login exclusivity, template limits, destination identity, masked routes) and a production build, on the final dashboard source | All passed |
| Dashboard chat and push channels, browser | A guarded 21-check run of the final dashboard source (fingerprint `2f9b414d…`) against a freshly built Windows protocol-13 development node, using **loopback fake Slack, Telegram, ntfy, Gotify and Pushover servers** (no real providers, tokens or messages) | **21/21**, 66.97 s. For each of the five kinds: created through the form, renamed with the stored configuration kept exactly, and its saved configuration reached the local fake provider. Also: the test confirmation names the destination and its Send test gets an accepted local outcome, 390 px layout, the browser stores only the endpoint, no page or console errors. The first run passed 16 checks, then failed on its own new selector (it required an explicit `role=dialog`, but the dashboard uses a native `<dialog>`); only that selector was corrected, and the failed run stays on record. Nothing retained afterwards. The unchanged contract harness wasn't re-run on protocol 13 |
| Dashboard Globalping forms, pure | Exact round trips for every mode, kept spellings and unknown keys, the leaf's shape rules, Go-omitempty comparison for new check generations, masking, viewer facts, the 26-type catalogue | 13/13 |
| Dashboard Globalping forms, browser | Current dashboard against a private copy of `78a390ec…` and a **loopback fake Globalping API** (no real measurements, tokens or quota) | **16/16**, 0 page exceptions, console errors or blocked requests (scenarios below) |
| Independent contract harness on protocol 12 | The unchanged reviewer script (`501ab3d3…`) against `78a390ec…` | 113/114. The failing step asserts exactly 25 check types; the dashboard now offers 26 (Globalping). That expectation is out of date, not waived |
| Protocol 11 development snapshot (`17161f1…`, binary `e6666539…`, *historical*) | Compiled process proofs, including a new hierarchy proof (cycle, probing, one down, reparent, restart, recovery); ordinary native tests of the core package | 15/15 process proofs, 88.7 s; core package 815 test events, 0 failures, 2 expected Windows skips (150.4 s). Reported by the backend owner. Not all packages; no new race, Alpine or whole-release run |
| Independent hierarchy and badge reviews | Hierarchy API and projection ([report](docs/hierarchy-review.md)); badge route ([report](docs/badges-review.md)) | No findings: 16 hierarchy tests over 20,480 oracle projections, 8 private mutations detected; badge backend: no correctness or privacy blockers |
| JSONata compatibility | Go port against fresh reference cases | **Release blocker:** seven compatibility gaps remain (40 cases wrongly report up). The R6 worker passed the unchanged 1 MiB test on Alpine under the original 128 MiB limit. An official-runtime candidate is under review, not adopted |
| Separate identity volume | Alpine with Docker, `docker run` equivalents (Compose unavailable) | 11 container checks and 7 migration and restore checks passed ([shipping progress](docs/shipping-progress.md)) |
| Dashboard protocol-11 screens, pure | Observed-time planning and reading, badge addresses and escaping, hierarchy helpers, group edits, conflict "Other fields" | 16/16 |
| Dashboard protocol-11 screens, browser | Current dashboard against private copies of the **development** binaries `e6666539…` (protocol 11) and `78a390ec…` (protocol 12), two fresh peered nodes, `vite preview`, headless Chrome with a request allowlist | **23/23** on each, 0 page exceptions, console errors or blocked requests (scenarios below) |
| Badge renderer visual review | Revised `badges_svg.go` (`eb2c830a…`) | 10/10 variants meet the node's renderer test conditions; reviewed at 20 px on light and dark backgrounds. Included in `78a390ec…`, where the backend owner's badge tests and the 23/23 proof pass with it |
| Compiled multi-process suite (*historical*) | Frozen **protocol 8** snapshot: deduplication, sender loss, partitions, reconciliation, groups, metadata edits, provider acceptance and retries, RBAC, HTTPS and loopback ICMP | 11/11 passed, 88.6 s ([details](docs/protocol8-checkpoint.md)) |
| Publication and privacy proof (*historical*) | Protocol 8 snapshot: public JSON redaction, tag opt-ins, manual status continuity | Passed, 1.4 s |
| Protocol 9 checkpoint (`cde59…`, *historical*) | Push heartbeats and repairs | Full ordinary Go suite 1,670 passing events; 13/13 process proofs in 90.3 s ([details](docs/protocol9-checkpoint.md)) |
| Protocol 10 snapshots (*historical*) | HTTP OAuth2, JSONata | Development evidence only ([checkpoint](docs/protocol10-checkpoint.md)); not a release candidate |
| Dashboard typecheck and production build | Current `apps/web` source | Passed |
| Independent dashboard contract review ([report](docs/dashboard-contract-review.md), *historical*) | Unchanged reviewer harness on the dashboard source of that time, with the frozen protocol-9 artifact and, separately, the protocol-10 development build | 114/114 on each, after fixing its three findings (empty Kafka message, empty RabbitMQ password, unknown service siblings) |
| OAuth2 and JSONata form builders (*historical*) | Dashboard source of that time, no node | 17/17 builder checks: exact round trips, required endpoint and client ID, empty client secret, exclusive auth, plain-HTTP opt-in, every expected type for every operator, unknown-key preservation, viewer facts |
| OAuth2 and JSONata in the browser (*historical*) | Dashboard of that time against a private copy of the protocol-10 **development** build `2c6ca876…` (source `0cc233a7…`, predates the MQTT wait fix and the non-finite compatibility fix) | 10/10: required fields block saving; OAuth2 with an empty secret reaches up through a local issuer; JSONata true is up and false is down; an ordering-with-boolean condition saves with a warning; HTTP, MQTT, MongoDB and SNMP JSONata configurations survive form renames byte-for-byte; viewers get neither object; storage holds only the endpoint |
| Private browser proof (*historical*) | Dashboard of that time against two locally built, peered nodes with fresh databases, a local webhook sink and `vite preview`; headless Chrome with a request allowlist | **24/24 scenarios** on three successive backend builds; after the review fixes, **28/28** (adding empty-value and edit-route checks) against the frozen protocol-9 artifact. 0 page exceptions, console errors or blocked requests |

The Globalping browser proof checks that the type picker offers exactly 26 types (all 25 earlier ones plus Globalping), creates HTTP, ping and DNS monitors through the form and compares the stored objects byte for byte, sees an HTTP check come up with the probe's 17 ms timing and status 200, confirms on the fake API that the token only authorizes API calls while the path, query and target Authorization header travel in the measurement, finds the token and header values masked on the detail page and in the JSON preview, renames without changing the options or the check generation and without a warning, sees the warning before a location change starts a new generation, gets down for a target failure, pending for a quota failure and down only with the opt-in, refuses a plain-HTTP API without the opt-in and a 45 s probe timeout, offers the recommended timeout, keeps the options through pause, resume and a real concurrent-edit conflict, hides every option and the URL's path from a viewer, and has no overflow at 390 px.

The protocol-11 browser proof builds a Site › Rack › Row tree and checks the health counts against the node, creates a nested group in the form, confirms a group can't become its own descendant's child, renames a group without losing its parent, moves one to the top level, resolves a real concurrent name/parent conflict field by field, deletes a parent and sees health turn unknown with the node's reason while the form refuses to save until reparented, compares the observed-time tiles with the node's figures for the exact request, finds no up time drawn for a failing monitor, reads every bucket in the table view, checks the 90-day preset and limit, sees maintenance as a separate lane that leaves the failure counted, fetches badges without credentials (GET and HEAD, states, 404 for duplicates, drafts and stray parameters), copies exact Markdown, HTML and image URLs from the Share dialog and the public page, sees no badge links for a draft and a warning before a rename would break one, and finds no overflow at 390 and 1440 px.

The earlier browser proof signs in, creates a tag, a webhook channel (and rejects an invalid Discord URL), tests the channel and reads its accepted outcome, creates HTTP, failing-HTTP-with-retries, manual, Redis, ping and push monitors through the forms, confirms a rename keeps the stored Redis password and the manual status change recovers rather than retires its incident, sees the warning before a check change closes an open incident, sends a real push heartbeat to the local node, schedules active maintenance, reads history statistics, publishes a status page and loads it with no `Authorization` header or cookie, checks that public JSON contains no private IDs, targets or node names, finds no horizontal overflow on 17 routes at 390/1000/1440 px in light and dark themes, finds only the endpoint address in browser storage, and checks that a viewer sees redacted configuration and read-only forms. It never contacted an external service or the user's own node.

## Not yet implemented

- **Uptime on public pages and badges, rollups beyond 90 days, uptime bars and merged multi-node availability.** Observed time covers only the node you're connected to, inside the dashboard.
- **Aggregate alerts for parent groups, inherited holds or maintenance, and nested sections on public pages.**
- **Shared Globalping measurements across nodes.** Each assigned node measures, and pays, separately; there is no leader, deduplication or shared result. Probe locations aren't verified, and target POST bodies, custom target CAs, client certificates, OAuth2 and redirect controls aren't available for Globalping checks.
- **Status-page custom domains, CSS, history charts, subscriptions or automatic incident publication.**
- **SMS, providers beyond these eleven, and provider extras** such as Slack blocks, Telegram formatting, ntfy actions or attachments, and Pushover receipt polling.
- **Exactly-once alerts.** Duplicates are possible by design.
- **Replicated history or accounts, automatic discovery, dashboard failover.**
- **Unbounded scale.** Coordination records are retained; storage, memory and catch-up costs grow over time. See [runtime coordination](apps/node-go/RUNTIME.md) and [shipping progress](docs/shipping-progress.md) for the remaining release work.

## Documentation

- [Shipping progress](docs/shipping-progress.md) and [parity inventory](docs/parity-audit.md)
- [Architecture and requirements](docs/architecture.md), [management API](docs/api.md), [authentication](docs/authentication.md)
- [Notifications](docs/notifications.md), [maintenance](docs/maintenance.md), [status pages](docs/status-pages.md), [badges](docs/badges.md), [tags](docs/tags.md), [push monitors](docs/push-monitors.md), [Globalping](apps/node-go/internal/globalping/README.md), [group hierarchy](docs/group-hierarchy.md), [observed-time history](docs/availability-history.md), [observability](docs/observability.md), [backups](docs/backups.md)
- [Go runtime](apps/node-go/README.md), [runtime coordination](apps/node-go/RUNTIME.md), [dashboard design](docs/design.md)

```text
apps/node-go     headless Go runtime and CLI
apps/web         optional React + TypeScript dashboard and public status pages
apps/proof       multi-process proof suite
packages/shared  public API types shared by the dashboard and proofs
examples/        sample monitor and group definitions
scripts/         setup, Go build wrapper and validation helpers
deploy/          nginx config for the dashboard container
docs/            architecture, API, design notes, and README images
design/          the dashboard's original concept mock (sample data) and the badge design
```

Licensed under the [MIT License](LICENSE).
