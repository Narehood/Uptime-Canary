# Software update notices

The protocol-20 candidate dashboard compares its running build and registered
monitoring nodes with the official public node and dashboard images. It shows
an update notice when the latest image is from later source, or is a newer
official build of the same source. Nothing is pulled, installed or restarted.

## Schedule and network use

One dashboard server owns the cache. Opening tabs and reading the status do
not trigger GitHub or registry requests. The first automatic check is scheduled
about a minute after startup once the owner is configured, then every six
hours. Its cache and scheduling state survive dashboard restarts.

Checks use bounded anonymous GHCR manifest/config metadata reads for the two
fixed official repositories. Image layers are never downloaded. ETags and
manifest digests avoid rereading unchanged configuration. Source-history
comparisons use GitHub only for distinct known commit pairs, cache resolved
results, and make at most sixteen comparisons per check. Throttling responses
delay the next attempt. There is no Docker socket, registry password or PAT.

Every signed-in dashboard role can read **Software updates**. Administrators
can choose **Check now**, at most once every fifteen minutes and only one
check at a time. The browser reads cached results at least a minute apart,
usually every five minutes, and waits when its tab is hidden or sign-in has
been refused. Public and signed-out pages do not check.

Set this on the dashboard to disable both automatic and manual checks:

```dotenv
CANARY_UPDATE_CHECKS=false
```

With checks disabled, the UI makes no freshness claim from earlier results.
Nodes themselves do not poll GitHub or GHCR for this feature. Multiple
independent dashboards each have their own cache.

## Dashboard API

`GET /api/dashboard/updates` returns the cached `UpdateStatus` to any signed-in
dashboard role. It never triggers an external request. An administrator can
POST `/api/dashboard/updates/check` with no body to queue one check (202), then
read the cached status. Both target the dashboard itself; the browser sends no
registered-node selector header. Neither accepts query parameters. The server
returns 401 without a dashboard credential, 403 for a non-admin POST, 409 when
disabled/already running, and 429 for the persisted manual cooldown or upstream
backoff. Older servers may return 404; the frontend then hides the feature.

## What the result means

| Result | Meaning |
| --- | --- |
| Update available | Latest official source is later, or the same source has a later authenticated publisher build time |
| Current | Same source and reported publisher build time as the latest official image |
| Newer than latest | Running source/build is later; an older image is not suggested |
| Unknown | Running identity, build time or source relation cannot support a comparison |
| Unavailable | Registry/history metadata could not be read; existing monitoring keeps working |

Official publisher builds stamp the full source revision and build time into
both the executable and OCI labels. Older images and custom builds may lack
that information. A dirty or unknown source identity is not assumed to be an
official image. A node that has not answered since dashboard startup remains
unknown until its build identity is read.

Same-source rebuilds are detected only when both images report usable build
times. This cannot identify what changed inside a rebuild, prove a security
fix, or discover the exact digest currently running. The optional GameDig
service and arbitrary containers on a host are not inventoried.

For a native or source build, select a compatible build for its operating
system and architecture. An advisory source comparison does not establish
container compatibility: the published container images currently target
Linux amd64. Check the chosen tag/platform before deployment.

The notice is advisory even when you pin a tag or digest. **Not now** dismisses
the current notice for this sign-in; a different update or later rebuild can
show it again. Upgrade explicitly using [coordinated upgrades](upgrades.md),
the same chosen source for nodes and dashboard, and retained data/identity
volumes. A successful CI build alone does not mean a readable matching image
has already been published.
