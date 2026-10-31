# Guided node onboarding and one-way VPN connections

Guided onboarding was merged through PR #14 as `358ede0`. The protocol-20
candidate adds native self-signed TLS and explicit certificate pins. Use
matching dashboard and node builds containing the selected mode; an older
image does not gain it from a stack variable. This guide is not a completed
release or a live NetBird/Tailscale acceptance result.

The dashboard can manage a node in either direction:

| Connection | Who starts requests | When to use it |
| --- | --- | --- |
| Dashboard reaches the node | Dashboard server to the node's private API | Existing routed networks; keep today's Verify, then Add flow. |
| Node reaches the dashboard | Node to a dedicated HTTPS connection listener | NAT, a client/server VPN, or a cloud dashboard without a return route to the node. No inbound node port is published. |

The browser always talks to the dashboard. Nodes still probe, exchange HA facts
with their separately configured peers, and send notifications independently of
the dashboard. Registering a node does not peer it, change its cluster token, or
create a VPN route. A missing connection means the dashboard cannot read or
manage that node; it is not evidence that the node stopped monitoring.

## Prepare the dashboard's connection endpoint

**Nodes → Add node → The node reaches this dashboard** is available before the
connection endpoint is configured. It identifies the existing dashboard service
to update and shows the required environment and port settings. After updating
the stack, use **Check dashboard setup** in that menu to reload its configuration;
once the endpoint is enabled, the invitation form appears in the same menu.
With **Deploy a new node → The node reaches this dashboard**, the Docker run,
Docker Compose, Portainer stack and Linux VM tabs are visible from the start,
including before the endpoint is configured or an invitation exists. Enter a
name, a unique Node ID and the dashboard address this node can reach. The address
defaults to the configured HTTPS listener. An IP or hostname keeps its configured
port; a complete HTTPS URL can specify a different port, for example behind NAT.
The address must still route to this dashboard and use its configured TLS trust.
Choose **Generate setup** once. The first export now includes the node’s own
administrator and cluster tokens, the dashboard address, and the one-time
invitation token. A certificate pin is included when the dedicated listener
terminates native TLS; reverse proxies using ordinary CA trust need no pin.
Paste the commands or stack YAML and private
environment settings and start the node. It connects immediately without a
second configuration edit or an inbound node port. Compare the fingerprint from
its log and confirm it in this dashboard after it connects.

**Show**, **Copy** and **Download** remain visible but disabled until complete
setup has been generated; Portainer’s individual copy controls do too. A warning
explains the prerequisites. Switching between new-node connection directions
preserves its name, Node ID, deployment method and credentials. The invitation
retains the selected node-reachable address even when it differs from the default.

For a node already deployed, choose **A node that’s already running** and enter
its existing Node ID and original dashboard connection address. That path exports
only connection settings and never replaces administrator or cluster tokens.
Apply those settings while retaining its credentials, names, data and identity.
Update the existing
Compose YAML and add the enrollment token to its existing private settings file,
update the existing Portainer stack, recreate the Docker container with the extra
connection settings, or restart the native service after updating its environment.
The dialog forgets bootstrap credentials when closed. If the node is already
deployed when returning later, choose **A node that’s already running** with its
same Node ID and apply only the invitation's connection settings.
The tabs describe the new node independently of the dashboard deployment.
The node-facing hostname or IP and the Docker host's bind IP are separate:
behind NAT, publish on an address assigned to the Docker host, forward the
advertised TCP port to it, and give nodes the externally reachable address.

Keep the browser service behind your normal NetBird, Tailscale or other sign-in
proxy. Give nodes a separate HTTPS origin with no interactive login. Canary
authenticates invitations and devices itself on that origin. Do not exempt the
browser service's `/api/` path from its sign-in proxy.

For example, if nodes can reach the dashboard host at `172.16.99.101`, but the
dashboard cannot initiate requests back to them, route a trusted name such as
`canary-connect.example.net` to that address. The name and certificate must be
usable from each node. With ordinary CA trust, a literal IP needs a certificate
that explicitly covers that IP. The native TLS alternative below authenticates
an exact independently checked certificate fingerprint. Canary refuses plain
HTTP, redirects and invalid certificates; there is no skip-verification switch.

Add these variables to the **dashboard** stack:

```dotenv
CANARY_DASHBOARD_CONNECTION_URL=https://canary-connect.example.net
CANARY_CONNECT_BIND_ADDRESS=172.16.99.101
CANARY_CONNECT_PORT=18348
```

Use your own reachable host address and HTTPS origin. The published stack maps
host port 18348 to the dashboard container's **8081**. Point a separate reverse
proxy service at `http://172.16.99.101:18348`, preserve the full request path, and
configure its external HTTPS hostname to match the variable above. This backend
HTTP hop belongs on your trusted private host/network. The proxy must forward
`Authorization` and `X-Canary-Registration`, allow POST bodies up to 6 MiB, and
allow a long poll to wait at least 25 seconds. It must not log request bodies or
credentials. Its TLS certificate must chain to a CA trusted by the node's Go
runtime; install a private CA in the node's trust store if needed.

Alternatively, terminate TLS directly in the dedicated listener with a retained
ten-year certificate:

```dotenv
CANARY_DASHBOARD_CONNECTION_URL=https://172.16.99.101:18348
CANARY_DASHBOARD_CONNECTION_TLS_MODE=auto
CANARY_CONNECT_BIND_ADDRESS=172.16.99.101
CANARY_CONNECT_PORT=18348
```

This mode needs no HTTPS-terminating proxy for that port. The wizard displays
the dashboard's certificate hash, to compare with its own log, and includes it
as `CANARY_DASHBOARD_TLS_FINGERPRINT` in the node settings. Keep that pin after
pairing. Leave native TLS off when the reverse proxy terminates HTTPS with a
different trusted certificate. See [encrypted transport](transport-tls.md) for
trust, migration and renewal; certificate fingerprints are distinct from the
node connection fingerprint compared during first-pair confirmation.

The connection listener exposes only `/connect/v1/enroll`, `/connect/v1/open`,
`/connect/v1/poll`, `/connect/v1/result`, and a basic `/healthz`. It serves no
dashboard HTML, login, accounts, monitoring API, public status pages or HA peer
exchange. The normal dashboard listener remains container 8080 / host 18347.
Leaving `CANARY_DASHBOARD_CONNECTION_URL` empty disables the connection listener
and outbound invitations. The connection host mapping defaults to loopback.

For a native dashboard process, use `CANARY_DASHBOARD_CONNECTION_HOST` and
`CANARY_DASHBOARD_CONNECTION_PORT` for its actual bind address and port. Those
are container/server settings; `CANARY_CONNECT_*` control Compose host mappings.
The native connection listener defaults to `127.0.0.1:8081` when enabled.

## Add the node

For **Deploy a new node → This dashboard reaches the node**, enter the **Node
URL**, such as `https://100.99.98.102:3100`. This is the node host's address as
reached by the dashboard server, never the dashboard's own address. The next
verification step keeps that URL.

**Publish or listen address on the node host** is a separate setting. Its
default, `0.0.0.0`, publishes the port on all IPv4 interfaces. Docker-published
traffic can bypass ordinary host INPUT/UFW rules. Choose an explicit host IP
to limit exposed interfaces, and use Docker-aware filtering to restrict who
can connect (for example, `DOCKER-USER` with Docker's iptables backend; use
the corresponding filtering for its nftables backend). Native VM listeners
use the host's normal firewall. See [Docker's firewall guidance](https://docs.docker.com/engine/network/packet-filtering-firewalls/).
To bind one interface, enter an IP actually owned
by the Docker or VM host. A VPN or NAT destination address may belong to another
machine and must not be used as the local binding. Inside a container,
`CANARY_HOST` remains `0.0.0.0`; the separate setting controls Docker's host-side
port mapping. On a native VM it controls `CANARY_HOST` directly. The URL's port
is used as the host port (the container still listens on 3100); if a forwarding
rule translates ports too, adjust the host mapping to match that rule.

On **Portainer stack**, add one Environment variables entry per displayed
card. **Copy name** copies only its Name; **Copy value** copies only its raw
Value, without `NAME=` or a trailing line break. Values remain masked on the
page after copying. The separate **Copy .env** and Download options retain all
`NAME=value` lines for Advanced mode or **Load variables from .env file**.
Portainer stores these values with the stack; these are environment variables,
not Docker secrets. Keep stack-edit access restricted.

Sign in as a dashboard administrator, open **Nodes → Add node**, and choose the
node-initiated connection. Enter a unique, persistent Node ID and a display
label. Generate an invitation, then choose Docker, Compose, Portainer, or the VM
instructions. The wizard obtains the connection origin from the server setting;
it does not infer it from the browser's SSO-protected address.

Invitations last 15 minutes. The secret is returned once and the dashboard
stores only its hash. Treat exported commands and environment files as private.
While waiting, **Check for connection** requests the invitation's latest status
immediately; automatic polling continues too. Adding a node still requires
comparing its fingerprint and confirming the first connection.
The wizard keeps generated values only in its memory; losing an unconfirmed
invitation requires replacing it. Existing nodes keep their existing admin and
cluster tokens. Generated bootstrap tokens are for fresh nodes, not replacements
for an existing installation's credentials.

Before changing an existing node, ensure `CANARY_ADMIN_TOKEN` is a working local
administrator account token; an old, removed or rotated bootstrap token is not
recreated from the environment. An unusable token or a saved identity pinned to
another dashboard makes startup fail, so monitoring stops until corrected. To
roll back the connection change, remove both dashboard connection variables and
restart with the prior node settings, retaining its data and identity volumes.

New-node commands use a readable deployment name with a public suffix derived
from the complete, case-sensitive Node ID. This avoids the common case,
punctuation and truncation collisions of the earlier naming rule; the suffix is
not a credential or an identity proof. Keep an existing node's current container,
project, volumes, unit and state directory when adding its connection settings.
The wizard does not rename or migrate them. Save each new Compose deployment in
its own directory so its `compose.yaml` and private environment file remain separate.

The node needs its normal unique Node ID, administrator token, cluster token and
retained data/identity volumes. Outbound mode also needs:

```dotenv
CANARY_DASHBOARD_URL=https://canary-connect.example.net
CANARY_DASHBOARD_ENROLL_TOKEN=<the invitation shown once by the dashboard>
```

Use `deploy/portainer-node-outbound.yml` for a GHCR node without an inbound host
port. The matching connector image must actually be published first. Containers
are currently published for Linux amd64 only. There are no published native
release downloads: the VM instructions require a matching independently built Go
executable or Docker Engine. Do not invent a release URL or use an older binary
that silently lacks this runtime feature.

Start the node, compare its log's **node connection fingerprint** with the
dashboard's waiting invitation, check the expected Node ID, and confirm pairing.
The invitation becomes ready only after the node durably saves its registration
ID and opens an authenticated session. A claim whose answer was lost, or whose
local state publication failed, remains unconfirmed and can be replaced safely.
The reported name/version is descriptive; the fingerprint comparison binds the
connection you approve to the node you started. Before confirmation there is no
registered monitoring entry and no delegated role account on the node. Pairing
then creates the same separate admin/editor/viewer delegation used by directly
connected nodes. Dashboard accounts retain their own roles and never become
node administrator credentials merely because a transport is connected.

The node saves its random device secret, pinned dashboard origin and registration
ID in `/identity`, outside database backups. Keep both node volumes. After a
successful pairing, remove `CANARY_DASHBOARD_ENROLL_TOKEN` from the stack and
redeploy while retaining `/identity`; keep `CANARY_DASHBOARD_URL`. Restarting
does not require another invitation. A changed dashboard origin or invalid
retained identity fails closed instead of silently enrolling elsewhere.

## Maintenance and failures

Admins can rename an outbound entry without contacting the node. Its address
cannot be edited: the node initiates the connection to its pinned dashboard.
Check now, monitoring edits and role rotation use that connection. Rotate access
changes the three delegated role credentials, not the device transport secret.
Recover access and durable operation journals retain their existing meanings.

Removing an entry immediately blocks normal dashboard management access. If the
node is disconnected, its removal remains pending until it reconnects and
confirms revocation of the role accounts. Preserve the node's saved connection
identity until that cleanup finishes. A request dispatched before disconnection
may have committed; a timeout is not a rollback. The transport does not replay
unknown writes when a session reconnects. Only the existing explicitly
idempotent enrollment/rotation/removal journals retry their own operations.

To replace an expired or cancelled **unconfirmed** invitation, create a new one
for the same Node ID and configure its token on the node, keeping `/identity`
and the same dashboard origin. The node retains its device secret and publishes
the new registration only after a successful claim and durable save. An already
registered node identity cannot be claimed by another invitation. Moving to a
different dashboard requires deliberate operator handling of retained identity
and old delegation, not an address edit or deleting application data.

The transport uses HTTPS long polling with fixed application routes, two
in-flight requests per node and the dashboard's existing eight-call global
limit. It carries no arbitrary TCP forwarding, browser cookies, peer exchange,
or remote executable commands. Request/response bodies and session counts are
bounded. Transport last-connected time describes contact with the dashboard;
it does not replace the timestamp or health result of any monitor.

## Validation status

The setup usability follow-up passed the three workspace type checks and all
configured frontend pure/control fixtures (including eight new setup groups)
on v3 source `dad28ba1f28dd6cd0f82fd50834ca0489cc619178eccd05a4f0fa51ae4b06b36`
on Windows amd64. After adding the narrow-layout browser assertions, the full
production-build browser suite passed **213/213**, with no page exceptions or
blocked requests, on unchanged v3 source
`4e464779b53bf3b1bd38bb157ac2123ecbf344894a2b85b3d278561386218a9c`
(215.203 s guarded, 2026-10-26). It exercised actual per-field clipboard
handlers through an in-page fixture, masked values after copy, independent
local binding and unchanged verification URL, and Portainer controls at 390
and 728 px. The copied commands were not deployed to Docker, Portainer or a VM.
Scratch returned to zero; hosted checks and matching published images remain
separate gates for this follow-up. The earlier 171/171 browser pass omitted
the optional control-plane fixtures and does not establish this new coverage.

The local Windows amd64 checkpoint is v3 source fingerprint
`e163a607106c074e196c798350f9f70bbf5a7a363e2720dc7ad8df186dbb2a03`
(2026-10-25). Its production dashboard build and browser suite passed **210/210**,
with no page exceptions or blocked requests, including real TLS pairing,
fingerprint confirmation, delayed invitation cancellation and credential
separation between node IDs. The selected production-process onboarding proof
passed **1/1**: dashboard and node restarts, retained pairing, monitoring and
down/recovery notifications while the dashboard was stopped, and revocation on
removal. Three workspace type checks and **288** pure/control checks also pass.

The core suite and vet passed on the unchanged **553 Go/runtime/module inputs**
fingerprinted as
`78d1a5581ce1756278ac043b8b37305d5adc5e3e40065ac599623641c7d20297`:
1,149 core, 87 identity and seven transport passing test events, with two
expected core skips and one platform-specific identity skip. This input scope
excludes the frontend, which was being repaired during that run. All guarded
scratch returned to zero; the shared module cache did not grow.

These are component results, not release acceptance. Native Linux amd64/arm64,
Alpine, race, the complete process-proof set and deployment gates must pass on
the reviewed PR head. Docker Compose, Portainer, VM installation and live
NetBird/Tailscale deployment have not been exercised locally. Synthetic proxy
fixtures do not establish compatibility with every live VPN or SSO setup, and
the feature needs matching published images before registry-based deployment.
