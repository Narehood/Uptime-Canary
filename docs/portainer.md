# Portainer and separate Compose stacks

Use these Compose files with a **Linux Docker Standalone** environment in
Portainer or with Docker Compose directly:

| File | Services | Use |
| --- | --- | --- |
| [deploy/portainer-node.yml](../deploy/portainer-node.yml) | One Go node | Deploy once per monitoring host, with a unique node ID and its own two volumes. |
| [deploy/portainer-dashboard.yml](../deploy/portainer-dashboard.yml) | Dashboard account server | Create its owner, then add private nodes to its shared list. It retains its own account database and encrypted node credentials. |
| [deploy/portainer-stack.yml](../deploy/portainer-stack.yml) | One node and dashboard together | Existing all-in-one deployment. |

The separate files retain the same service settings as the combined file.
They do not require one another or a shared Docker network. These templates
are not Swarm service definitions. The root [compose.yaml](../compose.yaml)
remains the build-from-checkout option, with the dashboard behind its `web`
profile.

The project is development software, not a validated release. The templates
pull development images from GitHub Container Registry (GHCR), following the
same deployment pattern as [WebTools](https://github.com/Narehood/webtools).
No image build, Git checkout, Go or Node.js installation is required on your
Portainer host. The initial published image profile is Linux amd64.

## Images are pulled from GitHub

The combined stack pulls both images; separate stacks pull only the image
they use:

| Service | Default image |
| --- | --- |
| Node | `ghcr.io/narehood/uptime-canary-node:latest` |
| Dashboard | `ghcr.io/narehood/uptime-canary-web:latest` |

GitHub Actions builds the existing Dockerfiles. Pull requests build without
publishing. Pushes to `main`, or a manual **Container images** run on `main`,
publish `sha-<full commit>` tags and advance `latest` after both images build
successfully. A run for an older commit does not advance `latest` after `main`
has moved. These development tags do not imply a tagged release or release
approval. A commit tag identifies its source; pin an image digest when you
need its exact bytes to remain fixed across a rebuild.

Pending publishing runs queue rather than replacing one another. Before
promoting `latest`, the publisher captures both previous digests. If a write
fails, it attempts to restore every attempted tag, including a timed-out write
that might have committed. A rollback failure leaves the job failed and
requires repair. First publication has no previous tag to restore; a failed
bootstrap remains incomplete. Updating two registry packages is not atomic:
use matching commit tags or digests when a deployment requires a fixed pair.

For a matching node/dashboard source revision, set `CANARY_IMAGE_TAG` to
`sha-` followed by the full 40-character published commit. Set
`CANARY_NODE_IMAGE` and `CANARY_WEB_IMAGE` to full references to override those
defaults, including private-registry images or digests. An existing stack
override using `uptime-canary-*:development` still selects that local image;
remove the old override to use the GHCR default.

Public GHCR images require no registry login. GitHub initially creates new
packages as private: the repository owner must make each package public once
under **Packages → package → Package settings → Change visibility**. An image
is not ready for anonymous deployment until its publishing run succeeds and
an unauthenticated pull works. A `denied` or `unauthorized` error is not a
missing local build. See [GitHub's package visibility instructions](https://docs.github.com/en/packages/learn-github-packages/configuring-a-packages-access-control-and-visibility).

For optional local builds, use the root `compose.yaml` with its `web` profile,
or build the two Dockerfiles yourself and set the full image overrides. Set
`CANARY_PULL_POLICY=never` for preloaded/offline images. The default policy is
`missing`; Docker also refreshes the `latest` tag under that policy.

## Create the stack

In Portainer **Stacks > Add stack**, use repository
`https://github.com/Narehood/Uptime-Canary` and Compose path
`deploy/portainer-stack.yml`. Use a stable stack name. The web editor or file
upload works too; the host pulls the GHCR images without building them.

| Variable | Value |
| --- | --- |
| `CANARY_NODE_ID` | Unique persistent node identity, for example `canary-main`. Never reuse it for another live node. |
| `CANARY_ADMIN_TOKEN` | A unique random node management token, used to enroll the bundled node. This is not the dashboard owner's password. |
| `CANARY_CLUSTER_TOKEN` | A separate random cluster token, shared only with that cluster's peers. |
| `CANARY_CORS_ORIGINS` | Exact external dashboard origin, such as `https://canary.example.com`, without a trailing slash. |

Generate independent tokens in a password manager or with the commands in the
[setup wiki](https://github.com/Narehood/Uptime-Canary/wiki/Setup-and-Testing#generate-the-portainer-tokens).
Keep them in Portainer's environment configuration, outside Git. No `.env` is
required. Node-only and combined stacks require all four variables; a
standalone dashboard requires its browser origin and can start with no nodes.

| Optional variable | Default / use |
| --- | --- |
| `CANARY_NODE_NAME` | `Uptime Canary` |
| `CANARY_IMAGE_TAG` | `latest`; pin the same published `sha-<full commit>` on both images. |
| `CANARY_NODE_IMAGE`, `CANARY_WEB_IMAGE` | Full references override the defaults, including digests. |
| `CANARY_PULL_POLICY` | `missing`; use `never` only for preloaded/offline image overrides. |
| `CANARY_BIND_ADDRESS`, `CANARY_WEB_BIND_ADDRESS` | `127.0.0.1`; use the intended private host interface if a remote proxy/peer needs it. |
| `CANARY_PORT`, `CANARY_WEB_PORT` | Host ports 3100 and 18347. Dashboard container port remains 8080. |
| `CANARY_LOCAL_NODE_URL`, `CANARY_LOCAL_NODE_TOKEN` | Local auto-enrollment. The bundle supplies `http://node:3100` and the node admin token. A standalone dashboard can leave both empty. Corrected values are applied to unfinished enrollment after redeployment; see below for already provisioned nodes. |
| `CANARY_PEERS` | `[]`, or explicit peer IDs and node-reachable URLs. Registry enrollment does not configure these. |

Deploy and open the dashboard URL. On first use, obtain the private setup code
from the **dashboard container's log**, or `/identity/setup.code` inside it.
Create the first dashboard administrator with an account name and a password
of 12 to 128 characters. The UI signs in automatically. Once an owner exists,
that setup code stops working; administrators create later accounts on the
Accounts page. Node passwords and tokens are not copied into dashboard accounts.

All users share one saved node list. Administrators add/remove nodes and manage
accounts, editors manage monitoring configuration, and viewers read with the
node's own secret redaction. A bundled local node is registered automatically,
including a visible starting entry while it is unavailable. A standalone
installation starts with **No nodes yet**. Login needs no answering node.

If the local enrollment credential was wrong, correct it and redeploy: the
unfinished registration keeps its role credentials and retries with the new
value. Its address can also be corrected before identity discovery. Once a
node identity is proved, changing the configured address does not silently
send its credentials to another destination. The old card exposes **Remove**;
keep its old endpoint reachable until revocation completes, then the new local
destination is added automatically. Clearing the local settings exposes the
same removal action. An undiscovered placeholder can be removed without
contacting a node; provisioned role credentials still require confirmed remote
revocation. Do not delete dashboard volumes to repair these settings.

If the configured node is already registered manually, its registration and
role bundle are reused. Adopting it through a different address requires the
same node identity and successful verification of every existing role there.
Changing the enrollment token after successful enrollment does not rotate the
saved role credentials.

Use **Nodes > Add node** to enter a label, the private origin reachable from the
dashboard server and an admin enrollment credential. Verification is read-only:
check the proved node ID before confirming Add. Confirmation journals encrypted
credentials before atomically creating three role accounts on the node. An
interrupted enrollment/removal remains visible and retryable; removal is only
confirmed after the node acknowledges revocation. Selecting another node needs
no additional sign-in. In-place credential/address editing is not implemented.

The browser reaches only the dashboard server. The server generates its runtime
configuration and writes an HTML marker so a blocked config script cannot make
the UI fall back to direct node connections. It uses saved destinations and
role credentials, verifies HTTPS certificates, refuses redirects, and forwards
no proxy cookies or peer-sync routes. The Go service serves the assets and API;
nginx is not part of the current dashboard image. Health checks use `/readyz`;
`/healthz` remains a liveness endpoint.

The templates add `NET_RAW` only to nodes for ICMP. Both images run as nonroot
users. Container logs rotate at 10 MiB times three per service.

## Ports, origins and redeployment

The dashboard host port defaults to 18347 through `CANARY_WEB_PORT`. An explicit
older mapping such as 8080 remains supported. Whenever the browser origin
changes, update `CANARY_CORS_ORIGINS` and redeploy the affected dashboard.
The template passes that value to `CANARY_DASHBOARD_ORIGINS`.

Server-to-node requests send no browser Origin. Direct/static browser access
to a node still requires that node's matching CORS allowance. Changing a node's
allowance also requires redeploying its container. A plain restart retains old
environment values and port bindings; use Portainer's stack redeploy or
`docker compose up -d` with the same project, files and environment. Existing
stack variables and `.env` files are not rewritten automatically. See
[Docker restart behavior](https://docs.docker.com/reference/cli/docker/compose/restart/).

## Sign-in-protected reverse proxies

Route the entire external origin to the dashboard's published port:

```text
https://canary.example.com/ -> http://<dashboard-private-host>:18347
```

Use a matching published node/dashboard build containing the account service
before replacing an older direct-API deployment. Then remove any separate
`/api/` target to node port 3100: it would bypass dashboard authentication.
Keep paths, query strings, request bodies and Canary's Authorization header.
The browser needs no route to node APIs.

For NetBird, enable **Preserve Full Path** and **Use service authentication** on
the root target, with the intended SSO/access policy. Use its session method
without consuming Canary's own Authorization header. NetBird strips the header
matched by header authentication before forwarding; see
[NetBird authentication](https://docs.netbird.io/manage/reverse-proxy/authentication).
Proxy identity headers do not create or authorize Canary accounts.

The browser sends proxy cookies only to its own origin. Canary still requires
its separate dashboard account; the server contacts each node using a role
credential without that proxy cookie. If the proxy session expires, reload and
sign into the proxy again. API calls refuse login redirects. Canary sign-out
does not sign out of the proxy.

With NetBird or Tailscale, the private node route belongs to the **dashboard
container**. Configure its DNS and network access accordingly. A host-local
proxy can use the dashboard's loopback bind; a proxy in another container needs
an explicit route rather than that container's own loopback. HTTPS at the
browser may terminate before a trusted private HTTP server-to-node connection.

Public badge/status links on an SSO-protected origin still need proxy access;
a public Canary projection does not override the proxy's policy. Push senders
also need any proxy credentials required by that origin, or must use the node's
private push API. No broad management-API authentication bypass is needed.

The VPN wiki guide is prepared for publication with the matching images.
Provider references: [NetBird path routing](https://docs.netbird.io/manage/reverse-proxy#path-based-routing),
[NetBird authentication](https://docs.netbird.io/manage/reverse-proxy/authentication),
[Tailscale Serve](https://tailscale.com/docs/features/tailscale-serve).
Synthetic proxy fixtures do not certify a live provider deployment.

## Deploy separate dashboard and node stacks

1. Deploy `deploy/portainer-dashboard.yml`, with a stable name such as
   `canary-dashboard`, the intended image, private bind address and exact origin
   in `CANARY_CORS_ORIGINS`. It can start with both local-node variables empty.
2. Deploy `deploy/portainer-node.yml` on each monitoring host, with unique IDs,
   separate management/cluster tokens, and retained node data/identity volumes.
   Expose its API on the private interface reachable from the dashboard server.
3. Create the dashboard owner, sign in, and use **Nodes > Add node** for each
   server-reachable private node origin. Verify the returned identity first.
4. Configure HA peering separately using `CANARY_PEERS` and a shared cluster
   token. Docker DNS names do not resolve across independent networks or hosts
   automatically; use routed addresses. Adding nodes to the registry does not
   join clusters or change their identities, tokens or peer configuration.

The files also work from a checkout. Supply environment values through your
shell or a private file and keep separate stable project names:

```sh
docker compose -p canary-dashboard -f deploy/portainer-dashboard.yml up -d
docker compose -p canary-main -f deploy/portainer-node.yml up -d
```

These commands pull images rather than building on the deployment host. Use
the same matching published source tag across the dashboard and nodes.
Stopping the dashboard leaves checks and alerts running on nodes. An unavailable
node leaves dashboard login and other node selections available. Dashboard HA
and automatic management endpoint failover are not implemented.

### Splitting an existing bundle

Keep the old stack/project name for the node when switching its Compose path
to `deploy/portainer-node.yml`. Preserve `canary-data` and `canary-identity`.
Stop/remove the old web container before reusing its host port; never remove
node volumes. An older static dashboard had no accounts to migrate: create the
new dashboard owner without altering node accounts or data.

If moving an already initialized dashboard account server to another project,
retain and explicitly migrate both of its volumes. A new project name does not
move the old volumes. Update the exact allowed browser origin if it changes.
Moving a node host likewise requires the documented stopped-node data/identity
migration; never bootstrap another live node under the same ID.

## Preserve identity and data

Retain both node volumes, `canary-data` and `canary-identity`, plus the dashboard
volumes, `canary-dashboard-data` and `canary-dashboard-identity`. Actual Docker
names are project-scoped: keep the stack name stable. Do not attach a node's
volumes to two live nodes. Dashboard accounts, sessions and encrypted role
credentials live under `/data`; its encryption key, setup code and identity
material live under `/identity`. Protect that identity volume separately from
database backups. Startup refuses a database without its matching key instead
of generating a replacement. No credential rotation UI is implemented.

Node database restore remains its explicit quarantine/activation workflow;
replacing a database is not a safe way to clone a node. Do not restore an old
identity directory with an old database or delete it to bypass a startup lock.
Update/redeploy without removing volumes. Pin matching source tags or digests
for controlled upgrades, and upgrade cluster peers together on protocol changes.

## Scope and references

Earlier versions of the combined template passed actual Docker Compose runtime validation,
including readiness, CORS, private identity storage and restart persistence.
On `0c3d131`, [workflow 40013131856](https://github.com/Narehood/Uptime-Canary/actions/runs/40013131856)
also passed actual separate-project Compose runtime: independent networks, no
dashboard credentials, CORS, private identity storage, independent stop/start
and persistent node history. Configuration parsing and split-template parity
passed, and all 565 source-manifest entries matched the committed source; see
[the exact evidence](public-availability.md#split-deployment-checkpoint).
That run predates the completed public-history interface. No deployment through
a Portainer instance has been performed. Those historical checks do not cover the new dashboard-owned account service,
encrypted registry or onboarding. New exact-source container/platform checks
remain required. Neither record certifies a complete release or cross-host networking.

- [Portainer: add a stack and supply environment variables](https://docs.portainer.io/user/docker/stacks/add)
- [Portainer: build an image](https://docs.portainer.io/user/docker/images/build)
- [Docker: Compose pull policy](https://docs.docker.com/reference/compose-file/services/#pull_policy)
- [Docker: published ports and loopback binding](https://docs.docker.com/engine/network/port-publishing/)
