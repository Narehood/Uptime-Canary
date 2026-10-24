# Dashboard account and node enrollment contract

Status: accepted user workflow, 2026-10-24; local Windows integration passed;
hosted platform gates and matching image publication remain required.
The current uncommitted gateway work is transport groundwork, not completion of
this control-plane model. No release or migration acceptance is claimed here.

## User workflow

1. Open the dashboard URL. There is no private node address in the sign-in form.
2. On an uninitialized dashboard, create its first administrator account. On
   later visits, sign into the dashboard account. Subsequent account creation
   is administrator-controlled rather than public self-registration.
3. After sign-in, see the registered nodes and monitoring information. A fresh
   standalone dashboard has an empty state with an Add node action.
4. Register a node by supplying its label, server-reachable private URL and a
   credential authorizing enrollment. The server verifies its actual identity;
   it does not accept a claimed node ID without that verification.
5. A bundled local node is configured and registered automatically. An
   unavailable local node is shown honestly and must not prevent dashboard
   account setup or login.
6. The browser contacts only the dashboard. The dashboard server contacts all
   registered nodes, including nodes on NetBird/Tailscale networks. Operators
   do not sign into individual monitoring nodes to use the dashboard.

## Ownership and persistence

- The dashboard owns its accounts, roles, sessions and node registry in its own
  persistent SQLite state. These are not accounts stored on a selected entry
  node. Dashboard login works before any node has been enrolled.
- Monitoring nodes retain their independent databases, identity reservations,
  checks, incidents, alert delivery and peer coordination. Stopping the
  dashboard must not stop that work.
- Adding a node to the dashboard registry does not silently merge unrelated
  HA clusters or rotate a node's identity, credentials or cluster secret. Peer
  membership remains an explicit operational configuration.
- Node registration credentials stay on the server. Persisted credentials need
  authenticated encryption with a server key retained outside database backups,
  explicit restore/rotation behavior and bounded, redacted error handling.
- Existing node accounts, passwords, databases, identity volumes and tokens
  must remain intact. A dashboard account is a new identity domain; migration
  must not silently copy or reinterpret node password hashes as global accounts.

The user selected **one shared node list with roles**. Administrators manage
dashboard accounts and node enrollment/removal. Editors manage monitoring
configuration. Viewers read monitoring state with node-side secret redaction.
This is one shared self-hosted fleet, not per-user private fleets.

## Proposed server boundaries

Use a Go/SQLite dashboard service separate from the monitoring-node runtime.
It owns the HTTP API and static dashboard assets on one browser origin, with
its own persistent state and key material. Node availability is not a dependency
of dashboard authentication. Reuse the existing bounded password/session/MFA
primitives and their revocation semantics; do not introduce a weak second
password implementation or an implicit always-privileged proxy account.

The existing identity handler can operate without starting its monitor
scheduler, but its identity domain and storage must be dashboard-specific.
Whether it is factored into a shared service or wrapped behind a narrow facade
is an implementation choice; monitoring endpoints and peer synchronization must
not become reachable through that identity facade.

API groups (implementation in progress):

| Group | Authority and behavior |
| --- | --- |
| Dashboard auth status/setup | First-run status and one atomic first-owner creation; no node contact required. |
| Dashboard login/account/session | Existing password, bearer-session, revocation and optional MFA semantics, applied to dashboard accounts. |
| Registered-node collection | Authenticated listing; administrator enrollment/removal; explicit ownership policy as clarified above. |
| Registered-node operations | Server-selected stored destination and per-role node delegation; no browser-supplied proxy URL. |
| Public status | Explicit publication rules; no accidental inheritance of administrator node credentials. |

Enrollment should provision or accept dedicated least-privilege node
delegations, keep their private credentials encrypted on the server and track
partial provisioning durably. A central viewer must use a node viewer
delegation so that node-side redaction remains effective; simply proxying with
an administrator token and hiding edit buttons is insufficient. Removal must
distinguish removing a registry entry from confirmed remote credential
revocation, especially while a node is offline.

The normal post-login UI consists of a fleet overview, a Nodes page with Add
node, node detail/selection through dashboard API routes, and dashboard account
administration. Existing monitoring editors can be reused after their API
client targets server-selected registered nodes. Account administration must
not accidentally target service accounts on a selected monitoring node.

## Authorization and network boundaries

- NetBird SSO or another cookie-gated proxy can protect the entire dashboard
  origin. Dashboard authentication and role checks still apply behind it.
  Proxy identity headers alone do not grant a dashboard role.
- The server verifies the dashboard session and node authorization for every
  request. A viewer must not obtain secrets or gain mutation rights merely
  because the dashboard can contact a node with an administrator credential.
- Prefer per-role, least-privilege node credentials/delegations so the existing
  node API remains an authorization and redaction boundary. Enrollment,
  partial provisioning failure, revocation and removal need explicit cleanup.
- Registration is administrator-only. URLs are validated HTTP(S) origins,
  redirects are refused, private destinations are intentional operator choices,
  and the server never provides an arbitrary unauthenticated URL proxy.
- TLS verification remains enabled for HTTPS nodes. Requests, responses,
  concurrency and timeouts are bounded. A failed node cannot block dashboard
  login or prevent other registered nodes from loading.
- The public dashboard gateway must not expose `/internal/` peer-sync routes.
  Peers keep their existing private authenticated coordination paths.

## Required acceptance controls

- First-owner creation is atomic under concurrent requests, unavailable after
  initialization, and independent of node availability. Later accounts require
  administrator authorization. Sessions, password changes, logout and role
  revocation remain durable across restart.
- A fresh standalone dashboard can log in with no nodes. A bundled dashboard
  discovers its configured local node without manual browser-side registration.
- Node enrollment verifies identity/credentials, rejects duplicate or conflicting
  identities, persists across restart and never exposes saved node credentials
  in browser responses or logs.
- Viewers, editors and administrators have enforced permissions at both the
  dashboard and node boundaries, including secret redaction and account/node
  management. Failed or interrupted delegation provisioning cannot silently
  leave untracked administrator access.
- A real browser reaches only the dashboard origin when adding, selecting or
  operating nodes. The server can reach a private node the browser cannot.
  Stored old browser endpoint preferences cannot bypass this behavior.
- Proxy-cookie authentication works on the shared dashboard origin; expiry,
  redirects, cancellation and sign-out report their actual effects. No ambient
  credentials are sent to unrelated browser origins.
- Loss/restart/removal of one node leaves dashboard login and other node views
  available. Existing monitoring/notification/HA regressions retain their
  assertions and workload limits.
- Upgrade and rollback use private fixture data only. The existing node data
  and retained identity state are never treated as disposable build artifacts.

Implementation should reuse the existing Go/SQLite identity primitives where
their semantics fit, without pretending that a node-local login is already a
dashboard-owned account. The documentation and wiki must distinguish this
complete model from the gateway-only transport checkpoint.

## Implementation API contract

The production server writes `data-canary-dashboard="control-plane"` on the
HTML element and generates a runtime script setting the control-plane and
gateway flags to true. The marker keeps the client in dashboard mode if that
script is blocked. Static/development hosting supplies neither server marker
nor control-plane flag.
The same origin owns all routes below. Existing `/api/auth/*` and
`/api/accounts*` routes always refer to dashboard accounts, even after selecting
a monitoring node. Auth status additionally returns `dashboard: true`.

| Request | Result |
| --- | --- |
| `POST /api/dashboard/setup` `{name,password,code}` | 201 owner account; then use ordinary password login. Setup code comes from the operator's private server setup file/log, never a public endpoint. |
| `GET /api/dashboard/nodes` | `RegisteredNode[]`, shared list; addresses visible only to admins. No node credentials. |
| `POST /api/dashboard/verify` `{label,url,token}` | `NodeVerification`; read-only verification, 2-minute actor-bound ticket. No node accounts created yet. |
| `DELETE /api/dashboard/verify/{ticket}` | Discard a verification. Expiration also discards it. |
| `POST /api/dashboard/nodes` `{ticket}` | 201 registered node after durable enrollment. An ambiguous or interrupted enrollment remains visible as `enrolling`, with retry/removal actions. |
| `POST /api/dashboard/nodes/{id}/check` | Recheck node identity and reachability; return registered node. Any dashboard role may read/check. |
| `POST /api/dashboard/nodes/{id}/retry` | Admin retries unfinished enrollment or removal. |
| `DELETE /api/dashboard/nodes/{id}` | 204 after confirmed remote revocation, otherwise 202 `removing`; local bundled node cannot be removed in UI. |

Monitoring requests use the ordinary `/api/overview`, monitors, channels, groups,
maintenance, history, status-page and tag routes, with header
`X-Canary-Registered-Node: <registration id>`. The server resolves only saved
destinations and supplies the user's role delegation. Without this header,
`GET /api/overview` returns an empty dashboard overview so login and account
administration work without nodes. Other monitoring requests need a selection.
The header is ignored for dashboard authentication and account routes.
Unknown node registrations are 404. Upstream credential rejection is 502 with
a fixed node error, never 401 (which would falsely log out the dashboard).
Public status needs an explicit saved registration selector and receives no
management bearer; peer/internal and node account APIs are not forwarded.

Enrollment and removal are administrator-only. Provisioning uses one atomic
node-side role bundle, journaled in the dashboard before network I/O, so retries
cannot create untracked service accounts. The three role tokens are encrypted
at rest, never sent to the browser. Verification does not modify the node.
The registry is bounded to 64 nodes and 16 outstanding verification tickets.

Heartbeat capabilities use `POST /api/push?registration=<id>` with their
existing `Authorization: Push <monitor token>`, or opt-in
`GET /api/push/<monitor token>?registration=<id>&status=up`. The dashboard strips
the selector before node admission and forwards no dashboard bearer, role
delegation or proxy cookie. Nodes retain token, body/query, rate, generation
and durability checks. Machine senders still need any front proxy access or a
direct private route to the node. This does not bypass management authentication.

Windows-local identity controls passed on source `f728ae73…`: setup without a
node, private proof, owner races, restart, roles, logout, last-admin protection
and transaction rollback. This is component evidence, not full dashboard,
container, platform or release acceptance.

On source `2f69dbac…`, the full Windows amd64 `canary` package passed 1,088
test events with two expected Windows filename skips and no failures, plus
vet, three workspace type-checks and 139 pure groups. The production build and
browser harness then passed 170 checks: all 156 existing checks, six proxy
checks and eight control-plane checks, including actual generated POST/GET
heartbeats. There were no page exceptions or unexpected requests. The native
preview snapshot timed out twice, so the documented Chrome/CDP fallback used
one disposable profile. Each guarded run kept source unchanged, cleaned its
scratch to zero and left the module cache unchanged. This is local evidence;
it does not establish hosted platform, live-provider or release acceptance.
