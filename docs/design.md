# Uptime-Canary dashboard design

Status: **v3, implemented** in [`apps/web`](../apps/web) (React + TypeScript), 2026-10-22. Owner: dashboard design and visuals (Claude Opus 5.5), including the node's badge renderer. Backend context: the working backend is **protocol 14** (development, not released). It replaces the Go JSONata port with the official JSONata 2.2.2 runtime in the node's worker, with no monitor-option or form change, on top of protocol 13's native chat and push channels, protocol 12's Globalping remote measurements, protocol 11's group hierarchy health, local observed-time availability and public SVG badges, and protocol 10's HTTP OAuth2 and JSONata. Protocol 13 and earlier checkpoints are historical development evidence.
v3.3 adds protocol 15's opt-in **published 24 h history** (development): a per-service "24 h history" switch in the status-page editor, the answering node's availability, coverage and hourly strip with a table twin on the public page, and 24 h observed badge links. Accepted locally on Windows on source `a357c014…` (type-check, 15 tracked pure checks and build; contract harness with the public-history and Date-range extensions 139/139, after earlier 135/137 and 137/137 runs, see §14). No complete CI run covers it yet, so protocol 14 remains the last checkpoint validated by a complete CI run.
v3.2 adds forms for protocol 13's native **Slack, Telegram, ntfy, Gotify and Pushover** channels. They type-check, pass 8 pure builder checks, build, and pass a 21/21 browser proof against a freshly built Windows protocol-13 development node with loopback fake providers (see the README's validation table). Protocol 13 is still in development with no release binary. v3.1 adds the Globalping check type (structured ping, HTTP and DNS forms with provider, privacy, quota and timing guidance). v3 added observed-time history with a time-weighted chart, nested groups with read-only health counts, parent-preserving conflict resolution, and badge share links on the status-page editor and the public page. v2 added structured forms for every check type the node implements (24 active/operator types plus passive push), reusable notification channels with delivery outcomes, planned maintenance, time-ranged history statistics, tags, status-page publishing and a public, credential-free status route. v1 surfaces (overview, matrix, incidents, groups, nodes, conflicts, accounts, sign-in) keep their design.
Earlier concept: [`design/dashboard-concept.html`](../design/dashboard-concept.html), a self-contained v0 mock that uses **sample data only; nothing in it is live**.

This document covers the dashboard's interaction model, information architecture and visual direction. It adds no product requirements. Requirement status lives in [architecture.md](architecture.md) and [shipping-progress.md](shipping-progress.md); API contracts live in [api.md](api.md) and the linked topic documents. Statements carry one of these labels:

| Label | Meaning |
|---|---|
| **Agreed** | Restates an accepted requirement or an API contract. |
| **Proposed** | A design choice made here and shipped. Reversible; flag it if it conflicts with product intent. |
| **Open** | Needs a product or backend decision. |
| **API gap** | The design wants data the API does not expose. The dashboard leaves these out rather than inventing them. |

---

## 1. Design principles

1. **Say where every fact comes from.** Each state answers *from which node, as of when, as seen by which endpoint*. The dashboard talks to one endpoint (Agreed). It never pretends to be a global, central view.
2. **Unknown is a real state.** A stale observation, an out-of-view peer or an unresolved config conflict shows as **unknown**: a dashed ring, plus hatching when out of view. It is never painted as up or down (Agreed).
3. **Location before verdict.** Every aggregate state sits next to its per-location evidence, and the wording separates *down everywhere* from *down from 2 of 6 locations* (Agreed).
4. **Quiet when healthy, loud when not.** Healthy cells are small checks. Problems get filled shapes, banners and the top of every list. On phones, healthy rows collapse to one line.
5. **Explain the mechanism when it matters.** Partitions, duplicates, candidates, conflicts and held-back alerts get one plain sentence at the point of impact. The UI never promises exactly-once delivery (Agreed).
6. **The dashboard is a guest.** It reads nodes; nodes never depend on it. Losing the web container or an endpoint is an explainable state, not an error page (Agreed).
7. **No hidden decisions.** Outage policy, revision, sender, candidate order, conflict heads and group dependencies are visible where they affect the outcome.
8. **Nothing is claimed before the node says so.** A save, delete or resolution is reported only after the endpoint acknowledges it, with the revision it returned (Proposed).
9. **Plain-spoken and calm.** Short sentences and everyday words. Identifiers appear in monospace.

---

## 2. Mental model and vocabulary

| Term in UI | Meaning | Source |
|---|---|---|
| **Node** | An independent runtime (scheduler, probes, SQLite, incidents, notifications, API). | `NodeView` |
| **Endpoint** | The node this dashboard currently talks to. Switchable. | `Overview.node` |
| **In view / out of view** | Whether the endpoint can currently reach a peer. Out-of-view cells are hatched. | `NodeView.state`, `lastSeenAt` |
| **Location** | A node in its role as an observer of a monitor. | `Observation.nodeId` |
| **Observation** | A node's latest check result. *Fresh* means recent and taken on the current revision. | `MonitorView.observations` |
| **Monitor** | A check with a stable ID that is shared across its assigned nodes. | `Monitor.id`, `nodeIds` |
| **Revision** | An opaque config version. The UI shows the first 6 characters. | `Monitor.revision`, `Group.revision` |
| **Conflict** | Two or more revision heads after concurrent edits. Shown until someone resolves it. | `configConflict`, `/conflicts` |
| **Incident** | Records merged by monitor across nodes. Opaque IDs; no sequential numbers. | `IncidentView` |
| **Recovered / Closed after config change** | How a closed incident ended: every assigned location reported a fresh up, or the check was changed, paused or deleted. Only a recovery is green; the other closes without an up result or a recovery alert. | `resolvedAt`, `retiredBy`, `retirementReason` |
| **Receipt** | A successful notification send by one node. | `NotificationView` |
| **Candidate order** | Assigned nodes sorted by ID: the order in which nodes try to notify. | derived from `nodeIds` |
| **Duplicate** | More than one down (or recovery) receipt for the same incident. | derived |
| **Group** | A user-named set of nodes and monitors, optionally with an outage monitor. No built-in names or topology. | `GroupView` |
| **Outage monitor** | An ordinary monitor whose delivered outage alert stands in for the group's other monitors. | `Group.outageMonitorId` |
| **Grace period** | How long a dependent monitor's alert waits, from its own outage, for the outage monitor's evidence and delivery. | `Group.graceMs` |
| **Held back** | A dependent alert suppressed by its group. Checks, history and incidents continue. | `suppressedByGroupId`, `suppressionReason` |
| **Outage policy** | Milestone rule: any fresh failure opens an incident; recovery needs fresh up from every assigned node. | Agreed |
| **Channel** | A reusable notification destination set: Discord, Slack, Telegram, ntfy, Gotify, Pushover (the five chat and push providers are protocol 13, in progress), SMTP, Mailgun, Brevo, Mailchimp Transactional or a JSON webhook. | `NotificationChannelView` |
| **Destination** | One route inside a channel: one email recipient, or the whole chat, push or webhook channel. Each has its own retries, outcome and receipt. Bound to the channel ID plus the route, never the credential. | `destinationId` |
| **Delivery attempt** | A node's send effort for one destination: queued, sending, retrying, held, accepted, failed, cancelled, unknown or unavailable. | `DeliveryAttemptView` |
| **Accepted** | The provider acknowledged the request. Not proof of inbox, channel or device delivery. | Agreed |
| **Maintenance window** | Planned work that holds new alerts while checks, history and incidents continue. Never turns a result green. | `MaintenanceView` |
| **Status page** | A published page of operator-chosen public names; served by every node as JSON. | `StatusPage`, `PublicStatusPage` |
| **Tag** | A reusable label with a color, assigned to monitors with an optional value and public flag. | `TagView`, `MonitorTag` |
| **Push heartbeat** | A passive check: an external job reports up or down before a deadline. | `MonitorInput.push` |
| **Sample availability** | Up ÷ (up + down) samples in a window, from one node. Not a time-weighted uptime or SLA. A 0–1 ratio. | `LocalHistoryStatistics` |
| **Observed time** | How long the endpoint held a fresh local result in each state, per bucket. Time without one is unknown, never up. **Observed availability** = up ÷ (up + down) time; **coverage** = known time ÷ the window; both 0–100 %. Maintenance overlaps and removes nothing. Not an SLA. | `LocalAvailabilityHistory`, `ObservedTimeTotals` |
| **Parent group** | An optional organizing link between groups. It rolls up current health only; alert holds and maintenance still follow each monitor's own group. | `GroupInput.parentId` |
| **Health** | A group's current rollup of its monitors and nested groups: down, then unknown, then pending, then up; empty or all-paused is pending. Counts are current monitors, not uptime. Unknown with a reason, and no counts, when the hierarchy is missing a parent, cyclic or conflicting. | `GroupView.healthState`, `health`, `healthReason` |
| **Badge** | A public SVG image of a published page's state, or one service's, picked by public section ID and public name. | `/public/badge/<slug>.svg` |
| **Remote probe (Globalping)** | A third-party probe, picked per check by a free-form "magic" location, that measures the target for the node. Provider failures are pending unless the operator opts into down. | `MonitorInput.globalping` |

### 2.1 State vocabulary

Status shapes carry meaning and color reinforces it, so meaning never depends on color alone:

| Glyph | Shape | Color token | Used for |
|---|---|---|---|
| Up | filled circle + check | `good #0ca30c` | fresh up, connected peer, recovery receipt |
| Down | rounded square + ✕ | `critical #d03b3b` | fresh down, form errors |
| Warning | triangle + ! | `warning #fab219`, edge `#a86f00` | peers out of view, groups holding alerts, a mixed history bucket, endpoint not answering |
| Conflict | diamond + ! | `serious #ec835a`, edge `#b5532c` | config conflict heads |
| Unknown | dashed ring + ? | `neutral #898781` | stale, earlier revision, out of view, conflict-affected |
| Paused | ring + bars | neutral | `enabled: false` |
| Pending | ring + clock hands | neutral | a retry in progress, a container starting, a manual monitor without status; neither up nor down |
| Maintenance | wrench in a warning-wash tag | `--warning-wash` | `maintenanceIds` on a monitor or incident; shown beside the measured state, never instead of it |
| Not assigned | short dash | neutral | node does not run the monitor; an incident closed after a config change |
| Out of view | 45° hatch behind the glyph | `--hatch` | node not reachable from the endpoint |
| Alerts held | layers icon in a warning-wash tag | `--warning-wash` | `suppressedByGroupId` on a monitor or incident |

**Monitor aggregate chip** (Agreed semantics, Proposed wording; computed in `model.ts`):

| Chip | Rule | `MonitorView.state` |
|---|---|---|
| **Down · everywhere** | every assigned location has a fresh down (single-location monitors say *from node-id*) | `down` |
| **Down · n of m locations [· k unknown / not in view]** | at least one fresh down, not all | `down` |
| **Unknown · config conflict** | conflicting heads | any |
| **Unknown · k of m not in view / without fresh data** | no fresh down, some locations lack fresh data ("not in view" only when every such node is out of view) | `unknown` |
| **Up** | every assigned location fresh up | `up` |
| **Paused** | disabled | `paused` |

Sort order everywhere: Down everywhere → Down partial → Conflict → Unknown → Up → Paused. Within a level, monitors whose alerts are held back follow the others, so a group's root cause leads.

---

## 3. Information architecture

```
Rail (primary nav)                    Top bar (global context)
├─ Overview                           ├─ Endpoint button: "Viewing from <id>" · peers in view · switch
├─ Monitors ─ detail ─ edit ─ Tags    ├─ Search (/ or Ctrl/Cmd-K): names, targets, node IDs, IDs, tags, groups
├─ Groups ─ new ─ edit                ├─ Freshness: "Updated 3 s ago" / "Not answering" / "Signed out"
├─ Incidents                          ├─ Theme: system → light → dark
├─ Notifications                      ├─ You: account name · role · "on <endpoint>"
│   ├─ Channels ─ new ─ edit          └─ Sign out / Disconnect
│   ├─ Delivery attempts
│   └─ Receipts                       Public (no sign-in, outside the shell)
├─ Maintenance ─ new ─ edit           └─ #/status/<slug>?node=<address>[&node=…]
├─ Status pages ─ new ─ edit
├─ Nodes
├─ Configuration (conflicts)
└─ Accounts (admins only)
```

Hash routes keep the static web container simple and never carry tokens or API data. The only route with a query string is the public status page, whose `node` parameters are public node addresses chosen by whoever shares the link (Proposed).

| Route | Purpose | API sources |
|---|---|---|
| `#/overview` | What needs attention now, from this endpoint's view | `GET /api/overview` |
| `#/monitors`, `/new`, `/<id>`, `/<id>/edit` | List with state, type and tag filters; create; detail; edit; pause; delete | `overview`, monitor CRUD, `/history?from&to&limit`, `/statistics`, `/availability?from&to&stepMs`, `/notification-deliveries?monitorId` |
| `#/tags` | Tag catalog: name, color, usage | `/api/tags` CRUD |
| `#/groups`, `/new`, `/<id>/edit` | Groups as a tree: health counts, parent, dependency and what they hold back | `overview.groups` (with `healthState`, `health`, `healthReason`), group CRUD |
| `#/incidents` | Open and closed incidents with delivery, maintenance and suppression lines | `overview.incidents`, `notifications` |
| `#/notifications` | Channels: create, edit, test, delete | `/api/notification-channels` |
| `#/notifications/deliveries` | Every delivery attempt known to the endpoint, per destination | `/api/notification-deliveries` |
| `#/notifications/receipts` | Successful sends with duplicate marking | `overview.notifications` |
| `#/maintenance`, `/new`, `/<id>/edit` | Planned windows with the node's computed current and next window | `overview.maintenance`, maintenance CRUD |
| `#/status-pages`, `/new`, `/<id>/edit` | Publication editor, share links and badge links | `/api/status-pages`; badge previews from `/public/badge/<slug>.svg` |
| `#/status/<slug>?node=…` | The public page itself, with an embed-a-badge panel | `GET /public/status/<slug>` and badge images, without a credential |
| `#/nodes` | Endpoint path, peer state from the endpoint, endpoint switching | `overview.nodes` |
| `#/config` | Conflict resolution for monitors, groups, channels, maintenance, status pages and tags | each kind's `/conflicts` and `/resolve` |
| `#/accounts`, `#/account` | Accounts on this endpoint (admin); your own password, 2FA and sessions | account and auth routes |

Rail badges: open incidents (critical red); active maintenance windows, out-of-view nodes and conflicts (neutral). No other navigation chrome uses status color.

---

## 4. Screens

### 4.1 Connect and switch endpoint
Unchanged from v1 (see the authentication contract and [browser evidence](auth-ui-validation.md)): password, authenticator code or account token; one node at a time; the credential lives only in the tab's memory; switching remounts every node-bound view so drafts, issued tokens and pending enrollments can't cross nodes.

### 4.2 Overview
v1 headline, KPI tiles and matrix, plus: a **Maintenance** tile (active windows and how many monitors they hold), a maintenance tag beside held monitors, and **pending** results in the headline. Latency in cell tooltips says "no measured latency" when a node reports `latencyAvailable: false`; it never shows a measured-looking `0 ms`.

### 4.3 Monitor form
- **Check type** is a grid of native radio cards in five groups: web and network (HTTP(S), ping, TCP, TLS, DNS, WebSocket, SMTP), databases and caches (PostgreSQL, MySQL, SQL Server, Oracle, MongoDB, Redis), messaging and containers (Docker, MQTT, RabbitMQ, Kafka), infrastructure and RPC (gRPC, SFTP, NTP, SNMP, RADIUS, SIP OPTIONS), and passive/operator (push, manual). Editing collapses the grid to the current type with "Change type".
- **Progressive sections.** Essentials appear inline; credentials, query/assertion, TLS and advanced options sit in disclosures whose closed summary says what's configured. A failed save opens every section with an error. The 16 service protocols are rendered from one declarative field list (`serviceSpecs.tsx`), so labels, hints, bounds and conditional visibility (for example SNMP v3 fields, gRPC unary fields, SFTP host-key pinning) follow the node's contract.
- **Edits preserve what the form doesn't model.** Every option object is edited from a copy of the stored one, so unknown keys survive at every level (including siblings of the protocol object inside `service`, keys inside a stored condition, and unknown channel config and template keys), and an option object whose fields didn't change is sent byte-for-byte as stored (Agreed: unchanged options must not start a new check generation). Changing the type or provider starts from a clean object.
- **Empty can be a value.** Where the node accepts an empty string as meaningful (Kafka's zero-length produce record, RabbitMQ's empty password), the field may be left empty and is sent as `""` rather than omitted; other required credentials stay required.
- **Edit forms load the stored item.** An edit route opened before the overview includes the item shows "not found" and mounts the form only when the item arrives, so its draft and base revision always come from the stored version.
- **Credentials are never blanked by accident.** Stored secrets are prefilled as masked fields with show/hide. PEM "Replace" keeps the stored value until a replacement is pasted ("Keep the stored key" undoes it); only "Remove" clears it. Clearing a stored secret shows "Saving removes the stored value" before save.
- **Schedule and retries:** interval, timeout, retries before down, an optional faster interval while failing, inversion, and active.
- **Ping:** packets per check, payload size, address family; a hint that a node unable to send ICMP reports unknown.
- **Manual:** a segmented status (not set / up / down / pending) and message, explaining that status edits keep the incident.
- **HTTP OAuth2 (protocol 10):** a fourth choice under Authentication, exclusive with Basic, Bearer and an Authorization header. The token endpoint and client ID are required; the client secret key is always sent, and an empty secret is valid. Client authentication is a Basic header (default, explicit `client_secret_basic` is kept if stored) or the form body; scope, audience and a token-endpoint CA are optional. Plain HTTP for the token endpoint or the monitored URL needs the explicit "Allow plain HTTP" opt-in. The secret is masked, previews hide it, and viewers never receive the object.
- **JSONata (protocol 10):** HTTP (under Response content) and MQTT, MongoDB and SNMP (under Query and assertion) take an optional expression, an operator (`==`, `!=`, `>`, `>=`, `<`, `<=`, `contains`) and a typed expected value (text, number, true or false, all available for every operator because the node accepts them; changing the operator never changes the stored type). Pairs that can't pass, such as ordering against true/false, get a warning rather than a save error. It must pass together with every existing matcher. The form never compiles or runs the expression; it states what `$` is for each check, that the node evaluates it in a separate resource-limited worker (a syntax or evaluation error reads exactly like a false result, because the node's diagnostic doesn't distinguish them; MQTT keeps waiting for the next matching message until the deadline; only an unavailable worker reports pending), the comparison rules, and the known differences of the Go port from the JavaScript JSONata Uptime Kuma uses. Unknown keys in a stored condition survive edits. Viewers don't receive HTTP JSONata.
- **Push:** a 64-hex token generated in the browser from 32 CSPRNG bytes, show/copy, **Rotate token** with a warning that it starts a new check, opt-in GET acceptance with a caution about URL logging, and a `curl` example (token masked unless shown).
- **Alerts:** a channel picker (provider, recipient count, disabled/conflict flags, unknown IDs kept visible), the legacy webhook in a disclosure, and repeat while down.
- **Organize:** tags with optional value and a per-assignment "Public" flag; group.
- **Check-generation warning.** The form mirrors the node's `sameCheck`. If the edit changes what is checked and the monitor has an open incident, a warning above Save names the changed fields and says the incident closes without a recovery alert (resolves v1 open question 11).
- **JSON (advanced)** previews the exact request body (credentials masked unless shown), copies it, and can fill the form from a CLI/API object. It complements the fields; it doesn't replace them.

#### 4.3.1 Globalping (protocol 12)
The type picker gains a **Remote probes (third party)** category with one entry. The section opens with a note on what leaves the nodes (target, location and, for HTTP, path, query and headers including Authorization go to Globalping and its probe; the token and API CA stay), then **Measurement** (Ping, HTTP, DNS), the mode's own target (URL, host or name, each kept while switching modes) and **Probe location** with magic examples and the reminder that the dashboard doesn't verify where a probe is.
- **Ping:** packets (1–16, empty 3), ICMP or TCP, and a TCP port (empty 80) shown only for TCP.
- **HTTP:** GET, HEAD or OPTIONS (no body), HTTP/2 (https only), skip TLS verification (danger, https only), a **Request headers** disclosure (32 headers, 32 KiB, no Host/User-Agent, Authorization as the only target auth) and **Response conditions** (status ranges, a literal case-sensitive keyword with "Fail when found", and JSONata on the strict-JSON remote body, with the 10 kB/truncation caveat). HEAD with a body condition saves with a warning that it can never pass.
- **DNS:** the 16 record types the schema allows, UDP or TCP, a resolver host or IP without a port, port (empty 53), an RE2 answer pattern matched per answer value, and "Accept an empty answer".
- **Globalping API and failures** (disclosure): masked token, API address (empty = public API; path prefix without `/v1/measurements`), "Allow plain HTTP for the API" (danger), extra API CA certificates (API only, not the target) and "Treat Globalping failures as down" with the pending-by-default explanation.
- **Probe timeout** (5–30 s) and **IP version** (refused for IP targets), then a timing and quota note computed from the form: the check timeout covers create, probe and polling; when it's shorter than probe + 10 s a one-click "Use N s" is offered; it states measurements per hour across the chosen nodes and warns below a one-minute interval.
Edits start from the stored object: unchanged spellings (`icmp`, `head`), unknown keys and the token survive, and exactly one mode object is sent (`{}` for defaults). The change warning compares `globalping` the way the node does (Go `omitempty` JSON), so a rename never warns and a location change does. Viewers get "Hidden from viewers" facts and the node's `scheme://host/` target. The JSON preview masks the token and header values.

### 4.4 Monitor detail
v1 location table and incidents, plus: certificate verification and validity per location, consecutive failures, a **Manual status** card for editors, and **History** with a range control (1 h, 6 h, 24 h, 7 d, 30 d, 90 d, custom from/to) and a check limit (100/300/1,000).

**Observed time** (protocol 11) comes first in the card: three tiles (observed availability with "up X of Y known", coverage with known, unknown and pending durations, and down time with any maintenance overlap), a 100 % column chart per bucket and a note. Presets end at the node's clock from its latest overview, because the node rejects a future end; a custom end in the future is clipped and says so. Buckets default to the smallest of 1 min … 1 d that keeps about 120 columns, and the **Buckets** menu offers every size that stays within the node's 4,096 buckets; ranges longer than 90 days are explained instead of sent. Percentages are the node's own, truncated (99.9996 shows 99.999 %, never 100 %), a tiny share shows "<0.01 %", and no known time shows a dash. The note names the node, the exact window, `retainedFrom` (or that nothing is retained yet and old checks aren't converted), the 90-day and 100,000-span limits, and that it isn't an SLA. Nodes before protocol 11 get a one-line note instead.

Under **Individual checks**, statistics tiles show samples by state, **sample availability** with its numerator and denominator (up ÷ (up + down), pending/unknown excluded) and P50/P95 latency with `latencyCount` as its denominator. A note says it is a sample ratio from one node, not a time-weighted uptime or SLA, and states the node's retention. The latency line skips unmeasured samples. Details list every type's options (credentials masked, "Reveal credentials" for editors, "Hidden from viewers" for viewers), channels, tags and, for push, when the heartbeat deadline started counting. A **Delivery attempts** card shows this monitor's per-destination outcomes.

### 4.5 Groups
v1, with wording updated for channels: holding is per destination; destinations the outage monitor doesn't reach fail open after the grace period.

Protocol 11 adds **nesting**. Without a search, groups render as a tree (children indented under a rule); a search shows a flat list so no match hides inside a parent. Each card shows the path ("In Site › Rack"), a **Health** chip beside the dependency chip, a health bar and counts (up, down, pending, unknown, plus paused and in-maintenance when nonzero) with "n active monitors across this group and m nested groups. Current results, not uptime.", the parent, and nested groups with their health glyphs. An empty or all-paused group says it is pending rather than up. When the node can't evaluate the hierarchy the card shows Unknown with the node's reason and no counts; a missing parent or cycle is named on the card.

The form adds **Parent group** (optional): every group by path, minus the group itself and its nested groups; conflicted groups and groups whose own parent chain is broken are listed but disabled with the reason. The hint says nesting organizes health only. A stored parent that has gone missing or into a cycle blocks saving with a field error until another parent or the top level is chosen, because the node validates the whole chain on every save. Edits start from the stored group, so unknown fields survive; "None · top level" omits `parentId`. Monitor and maintenance pickers label groups by path, and the maintenance hint says nested groups aren't included.

### 4.6 Incidents, notifications and deliveries
- **Incidents** add a maintenance line ("Overlapped planned maintenance" or "Entirely inside planned maintenance", naming the windows) and count duplicates per destination ("to 3 destinations · 1 duplicate").
- **Channels** list provider, masked route, recipients (email only), usage and revision with Test, Edit and Delete. Chat and push routes show the API origin and non-secret options only, never a token, topic, chat ID or user key. Three cards set expectations: delivery comes first, one destination per recipient (a chat, push or webhook channel is one), accepted isn't delivered.
- **Channel form:** provider cards grouped as Chat and push (Discord, Slack, Telegram, ntfy, Gotify, Pushover), Email and Your endpoint (Webhook); provider-specific fields (Discord route check; webhook bearer and opt-in plain HTTP; SMTP STARTTLS/implicit/plain relay with the "plain can't log in" rule; Mailgun US/EU; API base URL overrides behind a disclosure); sender and up to 50 recipients; optional literal templates with the six documented placeholders.
- **Chat and push fields (v3.2; loopback browser proof, development node):** Slack webhook URL; Telegram bot token, chat ID or @channel, forum topic, silent, protected and no-preview switches; ntfy topic, optional server, no login / access token / user and password, priority 1–5, comma-separated tags; Gotify server (required), application token, priority 0–10 (new channels start at 8, as in Kuma); Pushover application token, user or group key, device, sound and priority −2…2, where emergency asks for repeat and expiry seconds (30–10,800, repeat ≤ expiry) and inactive values are omitted. Telegram, ntfy and Pushover take an optional API base URL with a path prefix; every server needs HTTPS unless plain HTTP is explicitly allowed. Secrets use masked fields and are kept on rename; only the active provider object is sent, copied from the stored one so unknown keys survive, and Go-omitted zero values stay omitted. Each provider shows what picks its destination; editing explains whether a change is a credential rotation (receipts stay) or a new destination, and that a different Gotify application needs a new channel. Templates offer Title and Text only, state the provider's limit, flag fixed text that can never fit, and explain that built-in wording is shortened with “[truncated; see monitor details]” while custom text is never cut. Exact token and route formats stay the node's to validate; its errors surface in the form.
- **Test** opens a confirmation that a real message will be sent ("this channel's destination" or "all N recipients"; a Pushover emergency channel also warns that Pushover repeats it until acknowledged), then links to the outcomes. Saving a channel never sends anything.
- **Delivery attempts** table: last attempt, event (kind, monitor, incident), destination (channel and destination ID), outcome chip with HTTP status, next retry and hashed acknowledgement, node and failures. A "why" line explains fixed error codes in words, maintenance holds, group holds with the node's reason, and the per-destination fail-open rule. Filters: needs attention, accepted, held, tests, and channel.
- **Receipts** keep the v1 ledger; duplicates are now per destination.

### 4.7 Maintenance
Cards sorted conflict → active → scheduled → ended, each with a status chip ("Active · holding alerts", "Scheduled", "Disabled", "Conflict · not applied"), a schedule sentence in the schedule's own named zone, the node's current window and next start **in explicit UTC plus the viewer's local time and a relative time**, the selection, the node's reason, and which monitors it holds now. The form covers all six strategies (once, every N days, daily, weekly with ISO weekdays, monthly with days and "last day", five-field cron), an IANA time-zone field with suggestions, dates, start time, exact duration (minutes/hours/days) and the selection (all monitors, or chosen monitors and groups). The node computes windows and DST; the form only describes the schedule in words and says the list will show the computed result.

### 4.8 Status pages and the public route
- **Editor:** title (slug follows it until edited), slug, plain-text description, Published, ordered sections with a public section ID, ordered monitor selections each with a **typed public name** (private names are never prefilled; "Publish the private name" is an explicit button), certificate-date, public-tag and **24 h history** opt-ins (history is off by default; when ticked, a note explains that visitors see the answering node's last 24 hours including outage times, which nodes run the check, and that a paused or unknown monitor shows unavailable), and ordered announcements (title, message, state, pinned). Bounds mirror the API. Saves write `showTags`/`showAvailability` only when on, so a page without opt-ins keeps its pre-protocol-15 bytes, and fields this editor doesn't know (selection, section, announcement or page) are sent back as stored. A **Share** card builds the public link from the endpoint and chosen peer addresses.
- **Badges:** the Share dialog and the editor's Share card list badge addresses for the saved page when it is published (drafts say "Publish the page to get badge links"): the whole page, then each service whose public section ID and public name match exactly one entry, each with a live 20 px preview and Markdown, HTML (whole page) and image-URL copy buttons. **Served by** chooses the node address; loopback, private and plain-HTTP addresses get a caveat. Duplicate names are listed with the reason instead of a link. A service whose saved selection publishes history also gets a paired **24 h observed** row (`&metric=uptime`); duplicates get neither. The copy says which edits break embeds and that renaming a section doesn't; the editor warns when unsaved changes would. Visual rules: [design/badges.md](../design/badges.md).
- **Public page** (`#/status/<slug>?node=…`): page title and description, an overall state panel ("All systems operational", "n of m services down", "Checks in progress", "Status partly unknown"), maintenance stated beside results, pinned announcements first, sections with state chips, monitor rows with tags and certificate validity, then, only when the node returned a published `availability` field, a separate **Last 24 h** line under the current state: availability and coverage (truncated basis points from the millisecond totals, like the badge), "observed by this node, as of" the window end, a compact hourly strip (up, down and pending fill from the bottom; the rest is hatched unknown; a top mark for maintenance; tooltips) and an **Hourly figures** table. Unavailable and unreadable fields get their own sentence and draw no figures; the field is read strictly (window, frame, every bucket boundary, accounting identity and percentage must match) and never falls back to a healthy value. A footer naming the node that answered and when the projection was generated. An **Embed a status badge** panel offers the same badge rows, served by the node that answered. A data-source panel lets visitors pick another listed node or type one. Not found, unreachable and stale data each have their own message; the last good page stays visible, marked, when a refresh fails for the same node and page, while "not found" (an unpublished or withdrawn page) clears it. A late answer from a node or page no longer selected is discarded. Nothing switches nodes automatically.

### 4.9 Tags
Catalog table with color swatch, hex, usage (and how many assignments are public), revision and conflict link. Create/edit in a dialog with eight preset colors, a color picker and a hex field. Color is decoration: the name always sits beside it, and only strict `#rrggbb` values reach a style attribute.

### 4.10 Nodes, Configuration, Accounts
Nodes and Accounts are unchanged. Configuration resolves conflicts for all six resource kinds; the monitor resolver binds type, target and every option object into one "Check" row so an invalid combination can't be picked, and shows option JSON masked unless credentials are revealed.

---

## 5. Primary workflows

**W1 · Triage: the service or the path?** Headline → matrix row (which columns are red) → chip (*everywhere* or *n of m*) → monitor detail sentence → history on a node that runs it.

**W2 · Operating through a partition.** The banner names the out-of-view peers and explains independent monitoring and possible duplicates. Out-of-view cells are hatched; chips say "n not in view". "View from <peer>" opens the switch dialog with that peer's address.

**W3 · Explaining a duplicate.** Receipts → Duplicates filter, or the incident's "1 duplicate" tag (counted per destination); the Notifications cards explain why.

**W4 · Resolving a conflict.** Rail badge or "conflict" link → Configuration → pick per field or a whole version → preview → Save resolution (names all heads) → 409 means review again. Works for monitors, groups, channels, maintenance, status pages and tags.

**W5 · Adding a monitor.** Pick a type; fill the essentials; open sections only as needed; watch the notification order; create; land on the detail page with the acknowledged revision.

**W6 · Losing the endpoint.** The top bar switches to "Not answering · data from 1 min ago", a banner explains the last data is shown, the views dim, and polling backs off (5, 10, 20, 30 s). "Try now" and "Switch endpoint" are offered; nothing switches automatically. A 401 stops polling and offers sign-in.

**W7 · Accounts across nodes.** Create the account with a generated token on one node, copy it, switch to another node and create the same account with "Use a token I already have".

**W8 · Read-only wall display.** A viewer sees every view except Accounts; actions become lock tags; credentials and channel configuration show "Hidden from viewers". Phones collapse healthy rows.

**W9 · One alert for a shared outage.** Create a group for the place or dependency; pick its outage monitor (with the same channels as its dependents) and grace period; add dependent monitors to the group from their forms. During an outage the dependency's alert is sent, dependents show "alerts held" with the reason for destinations it reached, and incidents keep recording.


**W10 · Email the on-call team.** Notifications → New channel → SMTP/Mailgun/Brevo/Mailchimp → sender and recipients → Create → Test → Delivery attempts shows each recipient's outcome → pick the channel in monitors. *Chat or push (protocol 13, in progress):* the same flow with Slack, Telegram, ntfy, Gotify or Pushover; the channel is one destination.

**W11 · Plan maintenance.** Maintenance → New window → choose monitors or groups → strategy and zone → Schedule → the card shows the node's next window in UTC and local time → during the window, monitors show the maintenance tag and attempts show "Held".

**W12 · Publish a status page.** Status pages → New → title → sections → pick monitors and type public names → opt into certificate dates or public tags → Published → Create → Public link → copy for visitors.

**W13 · Monitor a cron job.** New monitor → Push → copy the generated token or the `curl` example into the job → Create. A missed heartbeat fails like any other check.

**W14 · Read history honestly.** Monitor → History → pick a range → read observed time (availability of known time, coverage, down time; unknown gaps hatched) → then samples by state, sample availability with its denominator and latency with its sample count; each note says what it isn't.

**W15 · Organize groups.** Groups → New group (or Edit) → Parent group → Save → the tree shows the group under its parent, and the parent's health counts include it. Moving it to "None · top level" removes the link. Nothing about alerts or maintenance changes.

**W16 · Share a badge.** Status pages → Share (or the editor's Share card) → choose the serving node → copy Markdown for the whole page or one service → paste into a README.

---

## 6. Visual direction

### 6.1 Identity
- **Mark:** a geometric canary perched on a wire that turns into a heartbeat blip, drawn in code (`ui/icons.tsx`, favicon in `index.html`): canary `#ffd23f`, wing `#e9a800`, beak and feet `#f28c28`, on ink `#151514`.
- **Wordmark:** "Uptime **Canary**" in two words for display; the project and repo name stays `Uptime-Canary`.
- **The yellow rule:** canary yellow appears only on ink (rail, brand panel, README art) and in illustrations, never as a UI state, so it's never mistaken for the amber *warning* status.
- **README art** (`docs/assets/*.svg`) uses the same ink, canary and status tokens so the repository and the product read as one system.

### 6.2 Color tokens
CSS custom properties on `:root`, with `[data-mode="dark"]` overrides (`styles.css`). Contrast was measured with the dataviz skill's validator in the concept phase.

| Role | Light | Dark | Notes |
|---|---|---|---|
| Page | `#f3f2ee` | `#0d0d0d` | warm neutral |
| Surface / card | `#fcfcfb` | `#1a1a19` | |
| Surface 2 (tiles, hover) | `#f1f0ec` | `#222220` | |
| Text 1 | `#0b0b0b` | `#f0efec` | |
| Text 2 | `#52514e` (7.7:1) | `#c3c2b7` (9.7:1) | |
| Text 3 (small labels) | `#6f6d68` (5.0:1) | `#a3a198` (6.7:1) | |
| Axis ticks only | `#898781` (3.5:1) | `#898781` (4.9:1) | non-essential; values are in tables and tooltips |
| Gridline / baseline | `#e1e0d9` / `#c3c2b7` | `#2c2c2a` / `#383835` | 1 px, solid |
| Accent marks (line, focus) | `#2a78d6` | `#3987e5` (4.8:1) | the only data hue |
| Accent text (links) | `#1c5cab` (6.5:1) | `#6da7ec` (7.0:1) | |
| Primary button | `#256abf` + white (5.4:1) | same | |
| Form error text | `#a32a2a` | `#f08a8a` | always with a down glyph; the one place text takes a red |
| Ink rail | `#151514`; text `#c3c2b7` (10.2:1) | `#111110` | canary on ink 12.8:1 |

**Status** (fixed, never themed): good `#0ca30c`, warning `#fab219` (light edge `#a86f00`), serious `#ec835a` (light edge `#b5532c`), critical `#d03b3b`. Warning and serious fills sit below 3:1 on light surfaces, so their glyphs get the darker edge (WCAG 1.4.11). Chips use a 10–20 % wash with primary-ink text.

### 6.3 Typography
`system-ui` everywhere; monospace (`ui-monospace`, Cascadia Mono, Consolas) for node IDs, monitor IDs, targets, revisions and addresses. Scale 24 / 20 / 15 / 14 / 13 / 12–12.5 / 11. Weights 450–750. Tabular numerals in tables, ages and axes.

### 6.4 Shape, space, elevation
4 px grid; cards 16 px padding, 14 px radius; controls 9–11 px radius; chips are pills. 1 px hairlines. Shadows only on dialogs, tooltips and toasts. Touch targets ≥ 44 px on phones.

### 6.5 Iconography
24 px outline icons, 1.8 stroke, round caps, drawn in code. Status glyphs are a separate 16 px filled set (§2.1). The endpoint button reuses the bird head on an ink tile.

### 6.6 Charts (dataviz method)
| Data job | Form | Why |
|---|---|---|
| Monitors × locations | matrix of labelled glyphs in a real table | identity plus state at a glance |
| Local history over checks | uptime strip, 2 px gaps | status over time; up is muted so failures pop |
| Response time | single-series 2 px line, 10 % wash, failing bands, end label, crosshair + keyboard, table twin | one series, so no legend box; no dual axis |
| Observed time per bucket | 100 % columns by duration: up (muted green) at the baseline, then down (red), pending (neutral), unknown (hatched); a separate maintenance lane above; per-bucket tooltip, arrow keys, table twin | composition of time, so gaps stay visible and nothing is interpolated; status colors with a legend and texture, never color alone; maintenance doesn't take space from any state |

Thin marks, recessive solid grids, tooltips that never gate (every value is in a table), text in text tokens only, no categorical palette (nodes are identified by label, never by hue).

### 6.7 Motion
150 ms on toggles and stale dimming only; no pulsing "live" indicators. The only ticking text is the freshness age, which counts up honestly. `prefers-reduced-motion` removes transitions.

### 6.8 Copy rules
- Say: "usually one", "duplicates are possible", "out of view", "last known", "unknown", "held back", "as known to <node>".
- Never say: "exactly once", "guaranteed delivery", "global status", "offline" (for a node that may be fine), "the cluster decided", or a diagnosis of power, internet or VPN.

---

## 7. Responsive behavior
Layout responds to the app's container width (container queries), so it works full-page and embedded.

| Width | Layout |
|---|---|
| ≥ 1180 px | Full rail with labels; two-column card grids; matrix with a separate State column. |
| 1060–1179 | Same, stacked bottom cards. |
| 700–1059 | Icon-only rail (labels become tooltips and stay accessible names; badges as dots). The matrix folds the state chip under the monitor name. |
| < 700 | Rail becomes a top bar with a scrolling labelled nav. Search goes full width. KPI tiles in 2 columns. Matrix rows become cards; **healthy rows collapse to one line**. Tables stack into label/value pairs. |

Charts redraw to their card width (ResizeObserver) and never scroll inside cards.

---

## 8. Accessibility (target WCAG 2.2 AA)
- **Never color alone:** distinct status shapes, text in chips, hatching plus "not in view" text, `aria-label` on every matrix glyph.
- **Real tables** with captions and headers for the matrix, locations, history, receipts, accounts and conflict heads. The chart has a table twin and arrow-key reading announced through a polite live region.
- **Keyboard:** skip link; visible 2 px focus rings; `/` and Ctrl/Cmd-K focus search, Escape clears it; native `<dialog>` for switch and delete (focus trap, Escape, focus returns to the opener); segmented controls are native radios; toggle buttons use `aria-pressed`.
- **Forms:** labels on every control, hints linked with `aria-describedby`, `aria-invalid` on errors, an error summary that receives focus after a failed submit.
- **Screen readers:** focus moves to the page `h1` on navigation; confirmations are toasts inside a polite status region; banners use `role="status"` (stale) or `role="alert"` (token rejected).
- **Reduced motion and forced colors** handled; glyphs keep their colors with `forced-color-adjust: none`.
- **Time:** times show in the viewer's locale and time zone; every `<time>` carries the ISO instant and a UTC tooltip. Ages are relative to the endpoint's `now`.

---

## 9. Data honesty and security
- **One endpoint, named everywhere.** Views say "as known to <id>"; nothing aggregates across nodes.
- **Polling:** `GET /api/overview` every 5 s, one request at a time (a refresh during a request is queued, never overlapped), paused while the tab is hidden, aborted on switch or disconnect. Writes trigger an immediate refresh. History refreshes every 15 s while its card is open.
- **Stale and rejected:** on failure the last overview stays visible, dimmed, with its age; retries back off to 30 s. A 401 stops polling. No placeholder or sample data is ever shown as live.
- **Acknowledged writes only:** buttons read "Saving to canary-a…" until the node answers; confirmations quote the returned revision. 400/403/404/409 keep the user's input and show the node's reason.
- **Tokens:** kept only inside the in-memory API client. Never in `localStorage`, `sessionStorage`, cookies or URLs; disconnect and reload drop it. Only the last endpoint address and the theme preference are stored. Requests use `Authorization: Bearer`, `redirect: 'error'`, `credentials: 'omit'`, `no-referrer` and a 15 s timeout.
- **Untrusted strings** (names, targets, messages, reasons) render as React text; tooltips use text only.
- **Webhooks** are masked to their origin by default (paths and queries often hold secrets); viewers never receive them.
- **Stored credentials** (service passwords, DSNs, PEM keys, channel API keys, push tokens) reach editors and admins only, masked until shown. Viewers receive configuration with these removed by the node and see "Hidden from viewers".
- **Async completions are bound to their connection.** Every write checks that the tab is still on the same connection before publishing its result, so a slow answer from one node can't appear on another.
- **The public route** reads `GET /public/status/<slug>` with no `Authorization` header and no cookies, even while signed in, and renders the response as text after keeping only expected fields. Tag colors must be strict `#rrggbb`. The link carries public node addresses only.
- **Channel tests** send real messages, so they always ask first; saving a channel sends nothing.

---

---

## 10. API mapping and gaps

| View element | Source | Status |
|---|---|---|
| Matrix, chips, unknown counts, conflict flag | `overview.monitors[].observations`, `unknownNodes`, `configConflict` | ✅ |
| Unmeasured latency | `Observation.latencyAvailable === false` → dash; statistics `latencyCount` | ✅ |
| Incidents, suppression, config-change closure, maintenance | `overview.incidents` (+ `retiredBy`, `maintenanceIds`, `maintenanceComplete`) | ✅ |
| Delivery outcomes per destination (was G4) | `/api/notification-deliveries` | ✅ failed, retrying, held and accepted attempts with fixed error codes |
| Channels | `/api/notification-channels` CRUD, conflicts, resolve, test | ✅ viewers get names and providers only |
| Time-window history and statistics (was G5) | `/history?from&to&limit`, `/statistics` | ✅ sample ratios |
| Observed-time availability | `/availability?from&to&stepMs` → `LocalAvailabilityHistory` | ✅ protocol 11 (development); the endpoint's own results only, read strictly (a malformed response is an error, not a guess) |
| Group hierarchy and health | `GroupInput.parentId`; `GroupView.healthState`, `health`, `healthReason` | ✅ protocol 11 (development); no inherited holds, maintenance, aggregate alerts or public hierarchy |
| Maintenance | `overview.maintenance`, `/api/maintenance` | ✅ windows computed by the node |
| Status pages and public projection | `/api/status-pages`, `GET /public/status/<slug>` | ✅ |
| Tags | `/api/tags`, `MonitorInput.tags`, `showTags` | ✅ |
| Push monitors | `MonitorInput.push`, `Monitor.pushStartedAt` | ✅ form and detail; validated in the protocol-9 checkpoint |
| HTTP OAuth2 | `HTTPMonitorOptions.oauth2` | ✅ form, facts and masking; backend protocol 10 in progress |
| JSONata conditions | `jsonata` on HTTP, MQTT, MongoDB, SNMP | ✅ form and facts; evaluation is the node's (protocol 10 in progress); no browser evaluation |
| Receipt slot (G6) | — | Repeat receipts still don't say which repeat they were |
| Per-location state changes (G1), peer views (G2), per-peer heads (G3), who saved a revision (G8) | — | API gap, unchanged |
| Published observed availability | `StatusPageMonitor.showAvailability`; `PublicStatusMonitor.availability`; `/public/badge/<slug>.svg?group&monitor&metric=uptime` | ✅ protocol 15 (development); serving node only, read strictly, projection times within the Date range; local Windows acceptance, complete CI pending |
| Rollups beyond 90 days, merged multi-node availability, public history beyond 24 h, whole-page uptime | — | API gap: not shown and not estimated |
| Badges | `/public/badge/<slug>.svg[?group&monitor]` | ✅ protocol 11 (development); renderer owned here, in the protocol-12 development binary |
| Globalping remote measurements | `MonitorInput.globalping` (`GlobalpingMonitorOptions`), type `globalping` | ✅ protocol 12 (development); forms, facts, masking, conflicts and generation warnings; no measurement is ever started by the browser |
| Status-page custom domains, CSS, history beyond 24 h, subscriptions, incident publication | — | Not implemented by the backend; the UI doesn't offer them |

---

## 11. Choices made here (reversible)
v1 choices 1–14 still hold. New in v2:

15. Service protocols are described declaratively; edits start from a copy of the stored object and unchanged objects are resent byte-for-byte.
16. Secrets are prefilled masked for editors (the API returns them to editors), with explicit removal warnings instead of write-only fields.
17. Push tokens are generated in the browser (32 CSPRNG bytes) and never leave the form except in the save request and on explicit copy.
18. Duplicate receipts are counted per destination (`incident · kind · destination`).
19. Maintenance windows are shown as the node computed them, in UTC and local time; the dashboard never re-implements the DST engine.
20. Public pages require a typed public name per monitor; private names are published only by an explicit button.
21. The public route renders outside the signed-in shell and never sends the management credential, even while signed in.
22. Tag colors are validated to `#rrggbb` before styling and always paired with the tag name.

New in v3:

23. Observed-time presets end at the node's last reported clock rather than the browser's, so a request never names a future instant.
24. Observed and sample availability stay separate figures with different scales; neither is averaged into the other.
25. The conflict resolver owns its fields per row and carries any field no row knows (from a newer node) as an "Other fields" row, so a resolution never drops data.
26. Badge links come only from the saved, published page; the dashboard never advertises a link the node would answer with 404.
27. A badge image names one serving node and never a credential; there's no cache-busting parameter, because the node accepts exactly `group` and `monitor`.
28. The public page renders badge previews only while a visitor opens the embed panel, so nobody's browser fetches images they didn't ask for.

New in v3.1:

29. Globalping is its own "Remote probes (third party)" category, and its privacy note is the first thing in the section, before any field.
30. Provider failures default to pending in the copy and the facts; "down" for them is an explicit, explained opt-in.
31. The timeout recommendation (probe + 10 s) is offered, never applied silently; shorter timeouts save with a warning.
32. URL and API checks follow the node's raw parsing, not the browser's: international host names must be punycode, and `..` path segments are refused rather than resolved.

New in v3.3 (protocol 15, development):

33. Published history sits under a service's current state as its own line, never in the state chip, so past up time can't read as present health.
34. The public figures are truncated basis points from the node's millisecond totals, matching the badge; the node's floats are only checked for consistency.
35. A published field that is unavailable, or in any way inconsistent, draws no figures at all; there is no partial or healthy fallback.
36. The strip legend appears once per page, under the first strip; every strip's exact values are in its hourly table.

---

## 12. Open questions
1. **Outage policy.** Keep "any location", or add threshold/all policies per monitor?
2. **Merged history.** Fetch history from every node for multi-location charts (needs cross-node auth and CORS), or keep endpoint-only history with switching?
3. **Public status for several nodes.** Should the public page query two listed nodes and show disagreement, or keep one answering node with manual switching (current)?
4. **Status page history.** Decided for protocol 15: opt-in per service, the answering node only, a fixed 24 h window, coverage always stated beside availability. Open: longer windows, and whether a page should compare nodes.
5. **Badges.** Decided for protocol 15: a separate per-service 24 h observed badge with availability and coverage, no whole-page figure. Open: whether an embed should name its serving node.
6. **Channel templates.** Is a live preview with sample substitutions wanted, given templates are literal text?
7. **Write target during a partition.** Should editing a monitor assigned to out-of-view nodes ask for confirmation?
8. **Node selection at scale.** Checkboxes suit about six nodes; are regions or tags for nodes expected?
9. **Hierarchy on public pages.** Should a status page ever publish nested sections, given parent links are private configuration today?

---

## 13. Implementation map (`apps/web/src`)
- `api.ts`: `CanaryApi` (bearer, 15 s timeout, `redirect: 'error'`) with typed wrappers for monitors, groups, channels, deliveries, maintenance, status pages, tags, history/statistics and accounts; `fetchPublicStatus(base, slug)` without a credential.
- `session.ts`: in-memory connection, non-overlapping poll loop with backoff, connection identity for node-bound state.
- `checks.tsx`: the check-type catalogue, `inputOf`, `checkChanges` (mirrors the node's `sameCheck`) and plain-language facts. `serviceSpecs.tsx`: declarative fields for the 16 service protocols, build/mask/facts.
- `globalping.tsx`: the Globalping draft, representation-preserving builder mirroring the leaf's shape rules, Go-omitempty comparison, masking and facts. `views/GlobalpingFields.tsx`: the form section.
- `publicAvailability.ts`: strict reading of a published `availability` field (protocol 15), the shared `validProjectionTime` Date-range check, truncated basis-point figures, words for tooltips and accessible names, and the editor's selection read/write that keeps unknown fields and omits off flags.
- `availability.ts`: observed-time query planning (node limits mirrored), strict response reading, truncating percentages and durations. `badges.ts`: badge addresses, ambiguity counting, address caveats, escaped Markdown/HTML snippets.
- `model.ts` (including the group forest, paths and parent checks), `format.ts`, `router.ts` (including `publicHref`), `theme.tsx`, `notices.ts`, `hooks.ts`.
- `ui/`: icons and status glyphs, bits, forms (durations, secrets, PEM, segmented controls, disclosures, node picker), charts (strip, latency, observed time, the public 24 h strip), tags, badges.
- `views/`: `Connect`, `Overview`, `Monitors`, `MonitorForm`, `ServiceFields`, `MonitorDetail`, `Tags`, `Groups`, `Incidents` (+ receipts), `Channels` (+ deliveries), `Maintenance`, `StatusPages`, `PublicStatus`, `Nodes`, `Config`, `Accounts`, `Account`. `App.tsx`: shell, banners, catalogs, tooltip layer.
- `styles.css`: tokens and components; container queries for layout.

## 14. Validation record
2026-10-22, against private fixtures only: an owned Go build (SHA-256 recorded with the run), two peered nodes with fresh databases on random loopback ports, a local webhook sink, and `vite preview` of the current bundle. The user's node, database and `.env` were not used, and no external service was contacted. The T3 preview snapshot timed out twice (`Preview automation snapshot timed out after 15000ms`), so the documented headless-Chrome/CDP fallback drove the UI with real DOM input events and a request allowlist. All 24 scenarios passed with no page exceptions, console errors or blocked requests on three successive backend builds (node binaries `5a276da3…`, `b31d43b0…`, `cf9c1c5e…`); the final run used the final dashboard source. After the independent contract review (three findings fixed, plus an edit-route race found while testing them) the browser proof grew to 28 scenarios and passes 28/28 against the frozen protocol-9 artifact (`d5f18402…`); the unchanged reviewer harness passes 114/114 against the same artifact, including after the protocol-10 OAuth2/JSONata forms were added. Those forms are covered by 17 builder checks and a focused 10/10 browser proof against a private copy of the protocol-10 development build `2c6ca876…` (source `0cc233a7…`, before the MQTT wait and non-finite fixes); the harness also passes 114/114 with that build. A development build is not release evidence. The scenario list is in the README's validation section; results and screenshots stay in the private run directories under `.canary/opus-web/`. This is functional evidence for the dashboard on that snapshot, not a release approval for the backend protocol under review.

**Protocol 11 (development), 2026-10-22.** The observed-time, hierarchy, resolver and badge work adds 16 pure checks (query planning against the node's limits, truncation, strict reading, badge addresses and escaping, ambiguity, address caveats, forest/paths/parent checks, group edit preservation, the "Other fields" row) and a 23-scenario browser proof against a private copy of the protocol-11 **development** binary `e6666539…` (source `17161f1…`): nested tree with counts equal to the node's, parent creation, self/descendant exclusion, rename keeping every field, move to top level, path labels, a real concurrent parent/name conflict resolved field by field, a deleted parent turning health unknown and blocking the form until reparented, observed-time tiles equal to the node's figures for the exact request, no up columns for a failing monitor, a table twin with every bucket, pending with no availability, the 90-day preset and the 90-day limit, a maintenance lane that leaves the failure counted, unauthenticated badge states with HEAD and 404 rules, share-dialog previews and exact copied snippets, no badge links for drafts, the editor's rename warning, the public embed panel without credentials or the signed-in shell, no overflow at 390 and 1440 px, and endpoint-only storage. 23/23 passed with no page exceptions, console errors or blocked requests. That binary still contains the earlier badge renderer; the revised renderer was reviewed in a scratch copy only (see [design/badges.md](../design/badges.md)). The T3 preview snapshot timed out again (15 s), so the CDP fallback drove the browser. Development evidence, not a release approval.

**Protocol 12 (development), 2026-10-22.** The Globalping form adds 13 pure checks (exact round trips per mode, kept spellings and unknown keys, the leaf's rules including raw host and path parsing, Go-omitempty generation comparison, masking, viewer facts, the 26-type catalogue) and a 16-scenario browser proof against a private copy of the protocol-12 **development** binary `78a390ec…` (source `d3e2889e…`) with a loopback fake Globalping API built from the leaf's engine-test shapes: no real measurement, token or quota was used. All 16 passed with no page exceptions, console errors or blocked requests; the protocol-11 proof also passes 23/23 on that binary, now with the revised badge renderer. The unchanged independent contract harness (`501ab3d3…`) passes 113/114 against it; its failing step asserts exactly 25 check types, and Globalping is the 26th. That is an out-of-date expectation in the harness, not a waived failure; the focused proof asserts all 25 earlier types plus Globalping instead.

**Protocol 14 (development), 2026-10-23.** No dashboard source change. The refreshed contract harness passes 124/124 against the protocol-14 Windows development binary `5917b2b0…` with no page exceptions, blocked requests or changed source files. It keeps the original 114 assertions except the accepted catalogue expectation (25 → 26 types, now with uniqueness and explicit Globalping checks), covers all eleven providers, and adds 10 pure and browser rename checks for the five chat and push providers. The T3 preview snapshot timed out twice (15 s), so the documented CDP fallback drove the browser with one disposable profile, removed afterwards. The 113/114 protocol-12 result above stays historical. On head `4d51a6a` the web image also passed its CI runtime, asset, header, SPA-fallback and nonroot checks ([run 39991889659](https://github.com/Narehood/Uptime-Canary/actions/runs/39991889659)). Development evidence, not a release approval.

**Protocol 15 (development), 2026-10-23, local Windows acceptance.** The published-history editor switch, public strip and table, observed badge links, strict reader and node/page state clearing were written source-only and run by the backend owner through the bounded runner. The first static run (type-check, 13 pure checks, production build) passed on source `cc6e0890…`. The first browser run of the contract harness with its public-history extension was **135/137**: both failures timed out at one preparation step, because the public page kept a visitor's node *position* after a new link listed fewer nodes, leaving no node to read; the same-node page-switch and 404-withdrawal assertions weren't reached. The repair keys the visitor's choice to the node list it was made in (a new list starts at its first node; polls keep the choice), and the reader now refuses an available metric without a positive safe-integer projection time; the first fixture's `generatedAt = 0 → available` expectation is quoted in the fixture and replaced by an invalid-time regression. On the repaired source `076899d5…` the type-check, all **14** pure checks (tracked as `scripts/public-availability-pure.mjs`) and the build passed, and the same 137 unchanged assertions passed **137/137** with 0 page exceptions or blocked requests, against a node built from the current Go source (`0617c558…`), in headless Chrome through the documented CDP fallback with an en-US locale (native preview snapshots timed out at 15 s). A controlled protocol 14 → 15 upgrade proof passed 1/1 on rebuilt binaries. No screenshots were taken and none of the README images show this feature. Integrated CI for this source runs after commit and hasn't passed yet; this is not a release approval. Details and the preserved failure: [published availability](public-availability.md).

**Protocol 15 Date-range repair (development), 2026-10-23, local Windows acceptance.** CodeRabbit review `5634028561` on `cb7de6e` found that a safe-integer page time beyond JavaScript's Date range passed the reader, so a coherent metric could be accepted while the footer's `toISOString` would throw. The backend owner's coherent-boundary pure check (a full 24 h metric ending exactly at the Date limit as the positive control; the limit + 1 must fail) **failed** before the repair (14 passed, 1 failed, 4.141 s, source `0f5a4406…`: `available` instead of `unreadable`). The repair adds one shared check, `validProjectionTime` (a positive safe integer no later than `MAX_DATE_MS`), used by the reader, the page-time normalizer and, after source inspection found the same path, announcement creation and update times. An unknown page time now reads "generated at an unknown time"; a note with unknown times keeps its title, message and state and omits its time line. On source `a357c014…` the type-check, all **15** tracked pure checks (`scripts/public-availability-pure.mjs`), the browser-script syntax check and the build passed in 12.546 guarded seconds (the main-bundle large-chunk notice remains at 756.89 kB, 221.18 kB gzipped, with no threshold raised). The browser run passed **139/139**: the 137 earlier assertions unchanged, a page time beyond the Date range with and without history, and an announcement with out-of-range times beside valid history; 0 page exceptions or blocked requests, 129.422 guarded seconds, using the earlier CDP fallback (en-US) without a new native preview attempt. The announcement group is post-repair coverage; no separate failing browser run is claimed for it. The 137/137 pass above covers the earlier source only. The 14 → 15 upgrade proof stays Windows-only evidence, and these repairs changed no Go code. CI for `cb7de6e` had passed node, web and proof with race still running when last checked; the repairs need a fresh complete run. Details: [completed review](public-availability.md#completed-review-date-range-and-host-proxy-guidance).

## 15. Concept file (historical)
`design/dashboard-concept.html` (v0) is a single self-contained file with sample data and reviewer controls. None of its data, controls or annotations ship in the product.
