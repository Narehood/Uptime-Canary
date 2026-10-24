# Portainer and separate Compose stacks

Use these Compose files with a **Linux Docker Standalone** environment in
Portainer or with Docker Compose directly:

| File | Services | Use |
| --- | --- | --- |
| [deploy/portainer-node.yml](../deploy/portainer-node.yml) | One Go node | Deploy once per monitoring host, with a unique node ID and its own two volumes. |
| [deploy/portainer-dashboard.yml](../deploy/portainer-dashboard.yml) | Static dashboard only | Deploy once wherever browsers can reach it; it needs no node credentials, database, peer network or node container. |
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

In **Stacks → Add stack**, select **Git Repository**, use
`https://github.com/Narehood/Uptime-Canary`, and set the Compose path to
`deploy/portainer-stack.yml`. Choose a stable stack name and the intended
repository reference. You can also paste the YAML into the web editor or
upload the file. No relative bind mounts or Business Edition path feature
are needed.

Enter these variables in Portainer's stack environment section:

| Variable | Value |
| --- | --- |
| `CANARY_NODE_ID` | Unique persistent identity, for example `canary-home-1`. Never reuse for another live node. |
| `CANARY_ADMIN_TOKEN` | A fresh random token, at least 32 characters. Used only for management. |
| `CANARY_CLUSTER_TOKEN` | A different fresh random token, at least 32 characters. Share only among members of this cluster. |
| `CANARY_CORS_ORIGINS` | Exact dashboard browser origin, for example `https://canary.example.com` or `http://192.168.1.20:18347`. |

Generate tokens in a password manager. Keep them in Portainer's environment
configuration, outside the Git repository. No `.env` file is required. These
four required variables apply to the node-only and combined stacks. The
dashboard-only stack needs none of them and must not receive account or
cluster credentials.

Optional variables:

| Variable | Default / use |
| --- | --- |
| `CANARY_NODE_NAME` | `Uptime Canary` |
| `CANARY_IMAGE_TAG` | `latest`; use the same published `sha-<full commit>` tag for a pinned pair. |
| `CANARY_NODE_IMAGE`, `CANARY_WEB_IMAGE` | Optional full image references; override each GHCR default. Remove old local-development overrides to switch. |
| `CANARY_PULL_POLICY` | `missing`; use `never` only for preloaded/offline image overrides. |
| `CANARY_BIND_ADDRESS`, `CANARY_WEB_BIND_ADDRESS` | `127.0.0.1`. Set to the host's LAN IP to access from another computer, or keep loopback behind a local reverse proxy. |
| `CANARY_PORT`, `CANARY_WEB_PORT` | Published host ports `3100` and `18347`. The dashboard's container port remains `8080`. |
| `CANARY_PEERS` | `[]`, or a JSON array such as `[{"id":"canary-home-2","url":"http://192.168.1.21:3100"}]`. URLs must be reachable from the node container. |
| History and timing settings | Defaults match `compose.yaml`; edit the explicit variables in the template as needed. |

Click **Deploy the stack**. The Dockerfiles supply health checks for both
containers. Open the dashboard at its published URL, select the node's
browser-reachable URL, and sign in with the admin token. The browser connects
directly to the node; the dashboard container does not proxy API calls.
An HTTPS dashboard needs an HTTPS node endpoint too. Terminate TLS at your
reverse proxy and use matching browser origins. Plain HTTP transports tokens
without encryption; use it only on a trusted network.

The templates add `NET_RAW` only to the node for ICMP. Both images run as their
existing nonroot users. Container logs rotate at 10 MiB × 3 per service.

The dashboard's default host port is `18347`; `CANARY_WEB_PORT` can override it.
Existing explicit port settings are preserved. When changing ports, update
`CANARY_CORS_ORIGINS` on each node to the exact origin you open in your browser,
then redeploy the affected stack(s) to recreate their containers. Redeploy the
combined stack once; with separate stacks, redeploy the node for CORS changes
and the dashboard for host-port changes. Existing stack variables and `.env`
files are not rewritten automatically. Keep `CANARY_WEB_PORT=8080` if you intentionally
want the earlier host mapping. The listener and health check inside the
dashboard container still use port `8080`.

A plain container restart keeps the previous environment and port bindings.
Use Portainer's stack redeploy, or `docker compose up -d` with the same Compose
file, project name and environment values used for that deployment. See
[Docker's restart documentation](https://docs.docker.com/reference/cli/docker/compose/restart/).

## Deploy separate dashboard and node stacks

1. Create the dashboard stack using Compose path
   `deploy/portainer-dashboard.yml`, for example with stack name
   `canary-dashboard`. Set its image and bind address as needed. It serves
   the UI on host port 18347 by default; it has no node API.
2. On each monitoring host, create a node stack using Compose path
   `deploy/portainer-node.yml`, for example `canary-home-1` and
   `canary-office-1`. Set the node's unique ID, management token, cluster
   token and the exact external dashboard origin in `CANARY_CORS_ORIGINS`.
   Expose the API using the host's LAN address or a reverse proxy.
3. Open the dashboard, enter a browser-reachable node URL, then authenticate
   to that node. Docker service names such as `node` are not browser URLs.
   If the dashboard uses HTTPS, the node's browser URL must also use HTTPS.
4. To form an HA cluster, configure `CANARY_PEERS` on the nodes with each
   other's persistent IDs and container-reachable API URLs, sharing the
   cluster token. Being selected in one dashboard does not peer the nodes.
   Different Docker hosts need routed addresses; their private Docker DNS
   names are not shared automatically. Keep the loopback bind when a reverse
   proxy process runs directly on the Docker host. Change `CANARY_BIND_ADDRESS`
   only when a peer or proxy on another host must reach the API directly, using
   the intended host interface. A proxy in another container needs an explicit
   network route to the node; that container's own loopback is not the Docker
   host's loopback. Do not expose the API on the LAN just to use a host-local
   reverse proxy.

The dashboard sends no management token from its container. Authentication
and API requests go from the browser to the selected node. Stopping or
updating the dashboard does not stop probing or alerting; stopping a node
does not stop the static dashboard, although that node's data is unavailable.
Switching nodes is explicit; the dashboard does not automatically fail over.

The same files work from a checkout with Docker Compose. Supply the node's
required environment values through your shell or a private environment file,
then use stable, separate project names:

```sh
docker compose -p canary-dashboard -f deploy/portainer-dashboard.yml up -d
docker compose -p canary-home-1 -f deploy/portainer-node.yml up -d
```

These commands pull images from GHCR without building locally. A dashboard-only
host pulls only the web image. A node-only host pulls only the node image.
Use the same published `CANARY_IMAGE_TAG` for compatible source versions.

### Splitting an existing combined stack

Keep the existing stack/project name for the node when changing its Compose
path to `deploy/portainer-node.yml`. This preserves its existing project-scoped
`canary-data` and `canary-identity` volumes. Stop/remove the old web service
before reusing its host port for a separate dashboard; do not remove the node
or its volumes. Deploy the dashboard under a new stack name, and update
`CANARY_CORS_ORIGINS` if its browser origin changes.

Renaming the node stack or moving it to another Docker host does not move
the volumes. Use the documented stopped-node data/identity migration process
for that operation; do not bootstrap another live node with the same ID.

## Preserve identity and data

Keep both `canary-data` and `canary-identity` volumes when updating or removing
containers. Docker scopes their actual names to the stack name. Keep that name
stable; renaming a stack creates different volumes. Do not attach either volume
to two live nodes. The image initializes `/identity` with private permissions.

Retain identity reservations independently of database backups. Database restore
is an explicit quarantine/activation workflow; replacing a database file is not
a safe way to clone a node. Follow the existing backup/restore instructions before
using historical data. Do not restore an old identity directory alongside an old
database, or delete it to bypass a lock or startup refusal.

Update/redeploy without removing volumes. Development `latest` images can
advance; pin the common commit tag or full image digests for controlled
upgrades. Automatic GitOps updates are optional, not required to pull an image
when deploying a stack. Upgrade all cluster nodes together when the peer
protocol changes.

## Scope and references

The combined template has passed actual Docker Compose runtime validation,
including readiness, CORS, private identity storage and restart persistence.
On `0c3d131`, [workflow 40013131856](https://github.com/Narehood/Uptime-Canary/actions/runs/40013131856)
also passed actual separate-project Compose runtime: independent networks, no
dashboard credentials, CORS, private identity storage, independent stop/start
and persistent node history. Configuration parsing and split-template parity
passed, and all 565 source-manifest entries matched the committed source; see
[the exact evidence](public-availability.md#split-deployment-checkpoint).
That run predates the completed public-history interface. No deployment through
a Portainer instance has been performed. These checks do not certify a complete
release or cross-host networking.

- [Portainer: add a stack and supply environment variables](https://docs.portainer.io/user/docker/stacks/add)
- [Portainer: build an image](https://docs.portainer.io/user/docker/images/build)
- [Docker: Compose pull policy](https://docs.docker.com/reference/compose-file/services/#pull_policy)
- [Docker: published ports and loopback binding](https://docs.docker.com/engine/network/port-publishing/)
