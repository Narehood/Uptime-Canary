# Real browser checks

The `real-browser` monitor asks a dedicated Chromium service to load an HTTP
or HTTPS page, run its JavaScript and reach the network-idle lifecycle event.
Canary's Go node owns scheduling, policy, observations, incidents and HA. It
does not install, launch or bundle Chromium in the node or dashboard image.

## Browser service

Provision a maintained, dedicated browser service with Chromium's sandbox,
network restrictions and external CPU/memory limits. Do not connect Canary to
someone's everyday browser or profile. Its debugging endpoint grants broad
browser authority, even though Canary sends only its compiled-in check
commands. Keep the endpoint private and authenticate it where appropriate.

Every assigned node needs server-side access to that endpoint and its normal
TLS certificate chain. The dashboard user's device does not connect to it.
The browser service needs network access to the page being monitored. Page
scripts run there with ordinary browser authority; a remote query is not an
OS sandbox supplied by the Go node.

The endpoint can be either:

- A complete `ws://` or `wss://` browser CDP address, including its path and
  optional query. This address often changes when a plain Chromium process
  restarts.
- An `http://` or `https://` origin. Canary reads only `/json/version` and uses
  the advertised WebSocket address. That address must stay on the same origin
  (HTTPS maps to WSS). A reverse proxy must publish a matching address in this
  response; Canary will not relocate its credential to an internal or foreign
  host supplied by discovery.

Discovery and WebSocket handshakes refuse redirects. They send no cookies and
do not use an environment proxy. A configured bearer is sent only to the
browser endpoint, never as a monitored page header. Userinfo and fragment text
are refused. The whole endpoint is private because its query can contain a
service credential.

## Monitor settings

```json
{
  "type": "real-browser",
  "target": "https://example.com/health",
  "browser": {
    "endpoint": "https://browser.example.internal",
    "bearerToken": "<browser-service-credential>",
    "screenshot": true,
    "delayMs": 250
  },
  "intervalMs": 30000,
  "timeoutMs": 10000
}
```

The target cannot contain userinfo. This profile does not provide arbitrary
scripts, steps, selectors, executable paths, login flows or saved browser
profiles. Browser requests and page subresources retain normal within-context
cookies, but each check starts in a fresh private context. Downloads are denied
for that context. The node discards the context after the check; it never
closes the operator's whole browser service.

`delayMs` is a whole number from 0 to 30,000 and must be shorter than the
monitor timeout. It waits after network idle, before the optional screenshot.
The total deadline includes discovery, connection, context/page creation,
navigation, idle, delay, capture and cleanup. Choose a timeout that covers this
work on every assigned node; the maximum is 120 seconds.

- **Up:** the current main document loaded with a 2xx/3xx status and reached
  network idle. Later JavaScript navigation must satisfy the new document's
  lifecycle; an earlier loader cannot establish success.
- **Down:** a definite main-document network/navigation failure or rejected
  HTTP status.
- **Pending:** unavailable or incompatible browser, authentication/transport
  failure, malformed replies, capacity, cancellation, deadline, or a failed or
  oversized requested screenshot. Pending is not inverted and does not close
  an existing outage.

Reported latency is page-load time including the idle wait, not a single
network round trip. No fabricated latency is attached to Pending.

## Private screenshots

Screenshots are opt-in PNGs, 1024 by 576 pixels, at most 192 KiB. A screenshot
can contain page secrets, so it is available only to node/dashboard editors
and administrators. It is not included in peer snapshots, overviews, public
status pages, badges or notifications. The dashboard displays it as a bounded
PNG data image rather than loading an arbitrary image address from the node.

Each observer keeps at most one capture per monitor in its local database.
It records the capture's observer, check generation and time. Another
observer's restored capture is not relabelled as this node's own. A metadata
rename keeps the image; changing the check, deleting it, disabling capture or
creating a configuration conflict removes it transactionally.

`GET /api/monitors/:id/screenshot` accepts no query. It returns the PNG and its
provenance, or 404 when none is available for this local check generation.
An image older than the latest fresh observation is labelled **Older capture**.
It is not evidence that the current page or monitor is healthy. A stale image
response is discarded when the dashboard switches node, monitor or generation.

## Bounds and deployment acceptance

Each Go node admits two browser checks without an unbounded queue. CDP traffic
has a 16 KiB handshake-header limit, 384 KiB frame/message limit, 8 MiB session
budget and fixed event/frame/loader counts. The captured image has its separate
192 KiB bound. Cleanup closes the socket and joins the reader, attempts an
explicit context disposal within the overall budget, and uses the context's
dispose-on-detach behavior if the connection ends.

Local Windows acceptance used a private Chrome 156.0.8078.48 process and two
production Go nodes. It exercised actual JavaScript, document failure, Pending,
node restart, retained outage/recovery, screenshots and viewer redaction,
fresh-context cookie isolation, context disposal and zero remaining owned
browser processes. Its earlier exact source is recorded in
`.canary/completion-browser-current.json`; subsequent integration edits require
fresh combined acceptance.

The native Linux gate uses the extracted package binary and a pinned official
headless Chromium fixture inside disposable PID/network namespaces. Privilege
is dropped before repository or browser code runs. The Chromium sandbox stays
enabled, and a supervisor-SIGKILL control must leave no private processes. The
download and single profile stay inside guarded scratch and are removed.
Missing sandbox/namespace prerequisites fail the gate; they do not justify
disabling the sandbox. Linux execution, other browser versions, Alpine/arm64
deployment and complete release acceptance need their stated platform evidence.

Ubuntu's AppArmor user-namespace restriction requires a profile for downloaded
Chromium builds. The hosted fixture loads a temporary profile attached only to
the exact executable in guarded scratch, then removes it after the normal run
and supervisor-death control. It uses no profile/cache file or global sysctl
change. A refused profile load or failed removal fails the gate. This follows
[Chromium's documented per-executable approach](https://chromium.googlesource.com/chromium/src/+/main/docs/security/apparmor-userns-restrictions.md),
with a literal attachment instead of a directory wildcard. Actual Linux
interop and policy cleanup must pass on the candidate source before acceptance.

On PR #12 head `fe14b52`, the extracted Linux amd64 executable passed actual
HeadlessChrome 156.0.8078.4 interoperability, including JavaScript, fresh-context
cookies, private captures and zero remaining contexts. The AppArmor profile
loaded and was removed successfully; normal cleanup and the supervisor-SIGKILL
control left zero processes. That package job failed later in the separate
upgrade fixture, so this is browser-component evidence for that exact source,
not whole-job, arm64 or release acceptance.
