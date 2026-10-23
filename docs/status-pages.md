# Explicit public status data

Each independent headless node can publish JSON for an operator-selected status
page. Publication requires an administrator or editor to save a page with
`enabled: true`. There are no default pages, automatic discovery, or required web
service. The optional dashboard is a consumer of this API; its status-page
management and public presentation UI remain pending. This backend provides no
HTML renderer, styles, custom CSS, images, external fetches, or Host-based domain
routing.

The replicated resource kind is `status-page`. Pages use the existing resource
revision DAG, immutable complete edge proofs, conflict resolution, persistence,
and deletion tombstones. Local accounts and authorization remain node-local.
Status publication does not change probing, history, incidents, maintenance, or
notification delivery.

## Management API

Management requests require an account bearer token or session. Administrators
and editors can create, edit, publish, unpublish, resolve and delete. Viewers can
read the list, individual ready pages and conflict candidates, but cannot change
publication or announcements. Cluster credentials do not authorize management.

| Request | Body and response |
| --- | --- |
| `GET /api/status-pages` | `StatusPageView[]`, each containing `statusPage` and `configConflict` |
| `POST /api/status-pages` | `StatusPageInput`; returns `StatusPage` (201) |
| `GET /api/status-pages/:id` | Current ready `StatusPage`; conflict or incomplete proof is 409; missing/deleted is 404 |
| `PUT /api/status-pages/:id` | Full `StatusPageInput` plus current `revision`; returns `StatusPage` |
| `DELETE /api/status-pages/:id` | Only `{ "revision": "CURRENT" }`; writes a tombstone (204) |
| `GET /api/status-pages/:id/conflicts` | All current `StatusPage` candidates, including tombstones |
| `POST /api/status-pages/:id/resolve` | Full `StatusPageInput` plus `revisions` naming every current head exactly once; returns `StatusPage` |

An example input, with an existing monitor ID substituted by the operator:

```json
{
  "slug": "services",
  "title": "Service status",
  "description": "Updates from our operations team.",
  "enabled": true,
  "groups": [
    {
      "id": "public-services",
      "name": "Services",
      "monitors": [
        {
          "monitorId": "OPERATOR-SELECTED-MONITOR-ID",
          "name": "Public service name",
          "showCertificate": false
        }
      ]
    }
  ],
  "announcements": []
}
```

All six input fields and every nested field are required; arrays must be arrays,
including when empty. A page may have zero groups or an empty group, which
reports `unknown`. Groups and their monitor selections preserve operator order.
Public groups are page-owned selections, independent of private monitor groups;
changing a private group's membership cannot add monitors to a public page.
Selected monitor IDs must be syntactically valid, but can arrive before the
monitor or its proof, including through peer synchronization. They remain
`unknown` until ready. Public monitor names never fall back to private names.

Bounds and validation:

- Slugs contain 1–64 lowercase ASCII letters, digits and hyphens, starting and
  ending with a letter or digit. Matching is exact and case sensitive.
- Titles, public monitor names, group names and announcement titles contain
  1–128 bytes of nonblank text. Description permits up to 2,048 bytes and can be
  empty. Description and announcement messages permit newline characters;
  other control characters are rejected. Text is valid UTF-8 and treated as
  plain text; clients must render it as text rather than trusted markup.
- At most 16 public groups and 64 monitors total per page. Group IDs are unique
  within the page; a monitor ID can be selected only once across its groups.
- At most 50 manual announcements. Announcement IDs are unique within the page.
  Announcements require `id`, `title`, `message`, `state`, `createdAt`,
  `updatedAt`, and `pinned`. Messages are 1–2,048 bytes of nonblank text. State is
  exactly `investigating`, `identified`, `monitoring`, or `resolved`. Timestamps
  are integer Unix milliseconds from zero through 9,007,199,254,740,991;
  `updatedAt` cannot precede `createdAt`. They are operator-provided publication
  metadata, not evidence of an observed incident. Future dates do not schedule
  publication: all saved announcements publish immediately with the page.
- `pinned` is a boolean display hint. Announcements retain operator order;
  resolved and unpinned announcements remain public until removed by an edit.
  No incident, observation, error or automatic incident message is imported.
- Unknown fields, differently cased keys, duplicate keys, null values and wrong
  primitive types are rejected, including within nested groups, selections and
  announcements. `showCertificate` and `enabled` must be explicit booleans.
- The existing 256 KiB management body ceiling applies. Local creation admits
  at most 64 retained page IDs, counting tombstones, within the existing 512
  resource admission ceiling. Valid partition unions can exceed these local
  admission counts, subject to the shared 32-member resource union bound.
  Local admission counts do not reject already-created partition resources.

`StatusPage` adds `id`, `revision`, `updatedAt` and optional `deleted`.
Create/update/resolve accept correctly typed management metadata for response
round trips, plus the revision envelope. Only the selected route and matching
revision/head set authorize the operation. Metadata is not saved in the input
payload; sending `deleted: true` cannot replace the DELETE operation. Updates
are full replacements, including announcement edits. Every conflict candidate
must have complete revision evidence before it can be joined. Resolution joins
more than 32 current heads using the common bounded revision-edge builder.

Create, update and resolution reserve slugs against every other locally known
live candidate, including unpublished drafts, conflicts and candidates awaiting
proof. An unrelated page's slug cannot be taken locally. Tombstoned pages free
their slug while their retained IDs still count toward local admission.
Simultaneous partition-local creations can still choose the same slug. Their
union is retained as separate resources; public reads return 404 until the
operator changes or deletes the duplicate claim. No arbitrary winner is chosen.

The headless CLI exposes `status-pages`, `add-status-page`,
`update-status-page`, `delete-status-page`, `status-page-conflicts`, and
`resolve-status-page`. Creation/update/resolution consume the same JSON inputs
as the API. Shared TypeScript contracts live in
[`packages/shared/src/status.ts`](../packages/shared/src/status.ts) and are
exported from the shared package. The optional web API client has typed
management wrappers and `fetchPublicStatus(base, slug, signal)`, which uses the
public path without a management bearer.

## Public boundary and projection

`GET /public/status/:slug` requires no account, token, cookie or query secret.
`HEAD` has the same status and headers with no response body. There is no public
page listing or write endpoint. The node ignores the Host header for selection.
The public boundary never reads the request body and never reflects a body,
authorization value, query, or arbitrary path into its JSON response.

Every missing, unpublished, deleted, conflicted, duplicate-slug or
incomplete-proof page returns the identical 404 body:

```json
{ "error": "Not found" }
```

Invalid slugs, extra path segments, encoded alternate paths and any query
string, including a bare `?`, also return 404. Disallowed methods return the same
404 without checking page existence. A stopped or faulted node fails closed.
The public namespace does not expose the reason for a failed publication.
Public work-budget exhaustion also returns this same 404 and leaves all
replicated resources intact.
During a partition, each node can only act on its locally known configuration;
an unpublication or duplicate claim on a disconnected peer cannot be known
until synchronization resumes. Replication is not global instant revocation.

Public GET/HEAD JSON responses, including their 404s, send
`Access-Control-Allow-Origin: *`, `Cache-Control: no-store`,
`X-Content-Type-Options: nosniff`, and
`Content-Type: application/json; charset=utf-8`. Credentials are not enabled.
Other public methods receive no wildcard CORS permission. Authenticated API
routes retain their existing explicit-origin CORS and authentication rules.

A successful `PublicStatusPage` contains only:

- Operator-chosen `slug`, `title`, `description`, public group `id`/`name`, and
  selected public monitor `name`.
- Live `state` and separate `maintenance` boolean for each monitor, group and
  page; `generatedAt` is the projection's Unix millisecond time, not a probe
  timestamp.
- The page's operator-authored announcements, in their saved order.
- Optional certificate `notBefore` and `notAfter` ISO 8601 dates when that
  selection has `showCertificate: true`, every assigned node has a fresh
  observation, and all those observations contain the same valid date pair.
  These are validity dates, not a certificate verification claim. Missing,
  stale, differing or absent certificate evidence omits the object.

Private monitor IDs, revisions, names, targets, node IDs and URLs, headers,
credentials, configuration routes, private groups, observation messages,
errors, HTTP status codes, incidents, delivery state, maintenance names or
schedules, and historical measurements are absent. The publisher's chosen text
is explicitly public; the runtime cannot detect a secret intentionally pasted
into an announcement or public name.

## Status and maintenance semantics

Publication uses only selected monitors and current in-memory observations.
Missing, deleted, conflicted, disabled or incomplete-proof monitors report
`unknown`; they never fabricate a healthy state. A monitor observation must
match the exact current revision and pass the runtime freshness window:
`max(3 * probeSpacing, 2 * timeoutMs, 3 * syncIntervalMs)`. Retry-aware probe
spacing follows the runtime policy. Future-dated observations do not establish
a public known state. A fresh down observation wins over missing peers;
otherwise any missing/stale peer produces `unknown`, then `pending` wins over
`up`. Only complete fresh up evidence produces `up`.

Groups and the overall page roll up only their published selections: `down`,
then `unknown`, then `pending`, then `up`. Empty groups/pages are `unknown`.
Unselected monitors and private dependency-group state cannot change these
rollups. Announcements do not change measured state.

Maintenance is an independent boolean, true when currently applicable validated
maintenance selects that monitor; group/page flags mean any selected monitor is
under maintenance. It never changes an observed down or unknown state to up.
Missing/conflicting/incomplete maintenance is not applied. A disabled but
otherwise ready monitor can still show applicable maintenance; a missing or
conflicting monitor cannot establish its maintenance selection. No private
maintenance title, ID, reason or schedule is published.

Public projection bounds the route to a fixed prefix plus 64 slug bytes, scans
only retained current resource heads within the shared capacity bound, and
evaluates at most 64 selected monitors, 16 public groups and 50 announcements.
Each request examines at most 16,384 current resource heads and decodes at most
16 MiB of live status-page configuration while checking all slug claims. A
larger retained union fails closed for public reads rather than truncating the
duplicate check or rejecting replication. This is a public-request work limit,
separate from the local creation and replicated storage limits.
It takes the node lock for the in-memory projection, then releases it before
encoding/writing the response. It does no probe, external fetch, history query
or SQL scan under the lock. The public read does not load incident history.

## Evidence and remaining parity gaps

Private fixtures in `status_pages_test.go` cover strict input and limits,
management CRUD/RBAC, exact revision and slug checks, announcement persistence,
public redaction and CORS, GET/HEAD/no writes/no reflection, stale/missing peers,
missing monitor/page proofs, maintenance, opt-in certificate dates, restart and
tombstones. They also exercise 71-head API resolution and restart, local
admission versus partition union, and real private HTTP peer synchronization
with more than one MiB of page resources, partition conflicts and concurrent
duplicate slugs. They use temporary or in-memory databases and private fixtures;
they do not require provider credentials or contact live services.

This is the backend publication contract, not full Uptime Kuma status-page
parity. UI and public presentation remain pending with the designated visual
owner. Latency, uptime/SLA percentages, historical charts, automatic incident
publication, subscriber notifications, scheduled announcement publication,
custom CSS/themes, image fetching, domain routing/custom domains, badges and
search/discovery are absent. In particular, no stale latency is relabeled as a
current value, and no historical or global availability is inferred. The
[observability contract](observability.md) separately describes local,
authenticated sample statistics.
