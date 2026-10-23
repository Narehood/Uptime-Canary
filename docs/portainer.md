# Portainer stack

Use [deploy/portainer-stack.yml](../deploy/portainer-stack.yml) with a **Linux
Docker Standalone** environment in Portainer. It runs one headless node and
the optional dashboard. Deploy a separate stack with a unique node ID and
its own two volumes for each additional node. This template is not a Swarm
service definition.

The project is development software, not a validated release. There are no
published release images. The template deliberately uses images already on
your selected Docker host and refuses implicit registry pulls.

## Prepare images

In the selected environment, open **Images → Build a new image**. Select
the URL build method (under **Upload** in some versions), and provide the
repository URL and Dockerfile path. Build twice, using the same commit for
both images:

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
configuration, outside the Git repository. No `.env` file is required.

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

The template adds `NET_RAW` only to the node for ICMP. Both images run as their
existing nonroot users. Container logs rotate at 10 MiB × 3 per service.

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

The template is checked as Compose configuration; a deployment through your
Portainer instance has not been performed. Existing node/web Dockerfile checks
are separate evidence and do not certify this stack or a complete release.

- [Portainer: add a stack and supply environment variables](https://docs.portainer.io/user/docker/stacks/add)
- [Portainer: build an image](https://docs.portainer.io/user/docker/images/build)
- [Docker: Compose pull policy](https://docs.docker.com/reference/compose-file/services/#pull_policy)
