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

The project is development software, not a validated release. There are no
published release images. The template deliberately uses images already on
your selected Docker host and refuses implicit registry pulls.

## Prepare images

In the selected environment, open **Images → Build a new image**. Select
the URL build method (under **Upload** in some versions), and provide the
repository URL and Dockerfile path. Build twice, using the same commit for
both images if using the combined stack. For separate stacks, build only the
image used by that stack:

| Image tag | Git repository | Dockerfile at repository root |
| --- | --- | --- |
| `uptime-canary-node:development` | `https://github.com/Narehood/Uptime-Canary.git` | `Dockerfile.node` |
| `uptime-canary-web:development` | `https://github.com/Narehood/Uptime-Canary.git` | `Dockerfile.web` |

Select the intended Git reference in your build workflow. Build on the host
where the stack will run, not just on the Portainer server. If your Portainer
version or remote agent cannot build from Git, build on a separate build host
and push to your own registry, then pull both images into the target environment
through **Images**. Set `CANARY_NODE_IMAGE` and `CANARY_WEB_IMAGE` to those exact
tags or digests. No build runs inside this stack.

With CLI access on the target host, the equivalent commands from a checked-out
commit are:

```sh
docker build -f Dockerfile.node -t uptime-canary-node:development .
docker build -f Dockerfile.web -t uptime-canary-web:development .
```

Both builds use the existing repository Dockerfiles. They download build
dependencies and need ordinary Docker build storage on that host.

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
| `CANARY_CORS_ORIGINS` | Exact dashboard browser origin, for example `https://canary.example.com` or `http://192.168.1.20:8080`. |

Generate tokens in a password manager. Keep them in Portainer's environment
configuration, outside the Git repository. No `.env` file is required. These
four required variables apply to the node-only and combined stacks. The
dashboard-only stack needs none of them and must not receive account or
cluster credentials.

Optional variables:

| Variable | Default / use |
| --- | --- |
| `CANARY_NODE_NAME` | `Uptime Canary` |
| `CANARY_NODE_IMAGE`, `CANARY_WEB_IMAGE` | The locally built image tags above. |
| `CANARY_BIND_ADDRESS`, `CANARY_WEB_BIND_ADDRESS` | `127.0.0.1`. Set to the host's LAN IP to access from another computer, or keep loopback behind a local reverse proxy. |
| `CANARY_PORT`, `CANARY_WEB_PORT` | Published host ports `3100` and `8080`. Container ports remain fixed. |
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

## Deploy separate dashboard and node stacks

1. Create the dashboard stack using Compose path
   `deploy/portainer-dashboard.yml`, for example with stack name
   `canary-dashboard`. Set its image and bind address as needed. It serves
   the UI on port 8080 by default; it has no node API.
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
   names are not shared automatically. For LAN or reverse-proxy peers, use
   an appropriate `CANARY_BIND_ADDRESS` rather than the loopback default.

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

These commands use images already present on their respective Docker hosts;
they do not build or pull an image. A dashboard-only host needs only the web
image. A node-only host needs only the node image. Build compatible versions
from the same intended source reference.

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

After rebuilding images, update/redeploy without removing volumes. Leave
automatic GitOps updates off while using development builds: a YAML update does
not rebuild these local image tags. Upgrade all cluster nodes together when the
peer protocol changes.

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
