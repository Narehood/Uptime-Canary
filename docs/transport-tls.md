# Encrypted node connections

Native TLS is available in the protocol-20 candidate. Use matching builds for
all peers; check the PR's validation and image publication before deploying.
These instructions do not change a running deployment automatically.

## Which connection is protected?

| Connection | Setting and trust |
| --- | --- |
| HA peer to node, or dashboard server to node | `CANARY_TLS_MODE=auto` on the node; HTTPS address and that node's `tlsFingerprint` on each caller |
| Node to the dedicated dashboard connection listener | HTTPS `CANARY_DASHBOARD_URL`; public CA trust, or `CANARY_DASHBOARD_TLS_FINGERPRINT` for native self-signed TLS |
| Browser to dashboard | Your existing HTTPS reverse proxy, including NetBird/SSO if used |

The node's API and peer exchange share one port (3100 by default). Native TLS
requires TLS 1.3 and generates an independent Ed25519 self-signed server
certificate valid for ten years. The private key stays in its retained identity
directory, outside SQLite backups. Keep the same node ID and identity volume.

An exact SHA-256 certificate fingerprint is an explicit trust anchor. Clients
check the certificate and its validity before sending a bearer credential. There
is no accept-any-certificate setting, HTTP fallback, or automatic remote trust
change. An empty fingerprint on HTTPS uses normal CA and hostname validation.
Self-signed certificates need their fingerprint even when the address changes.

## New direct node

Prefer **Nodes → Add node → Deploy a new node**, with an address such as
`https://100.79.12.4:3100`. The generated settings enable native TLS. The
dashboard server must be able to route to this address; the browser does not
connect to it. Use a host address actually assigned to the deployment machine
when binding a published port.

The node prints:

```text
Node TLS certificate fingerprint: <64 hexadecimal characters>
```

Copy this from the node's own log over a trusted administrative channel. Enter
it in the wizard before verification. Uppercase and colon-separated input are
accepted and normalized. A wrong pin cannot enroll a node. Verify is read-only;
the final Add action creates the dashboard's three role accounts.

In a container, `canary tls-info` also prints the public certificate,
fingerprint, expiry and whether it is currently valid. It prints no private
key and requires no administrator token. Run it as the container's usual user,
with that node's `CANARY_NODE_ID` and `CANARY_IDENTITY_DIRECTORY`. On a VM, use
the service's own user and retained directory; do not change their ownership.

## Migrate existing HA peers

1. Back up the node databases consistently and preserve their current identity
   directories separately. Inventory node IDs, addresses and saved credentials.
2. Set `CANARY_TLS_MODE=off` explicitly while preparing. Stop one node and run
   `canary tls-init` with its usual environment, user, database and identity
   mounts. This prepares the certificate without starting checks or a listener.
   Start that node in explicit `off` mode while preparing the others.
3. Read each fingerprint locally. Prepare each caller's full peer list with
   HTTPS URLs and the expected remote certificate, for example:

   ```json
   [{"id":"canary-office","url":"https://100.79.12.4:3100","tlsFingerprint":"<office node's complete SHA-256 fingerprint>"}]
   ```

   Replace the example fingerprint before use. Each peer pins the other node's
   certificate, not its own. Keep the cluster token and node IDs unchanged.
4. Apply `CANARY_TLS_MODE=auto` and the prepared peer lists together, then
   restart the nodes. Update direct dashboard registrations to HTTPS and the
   independently checked certificate fingerprint. Address/trust edits must
   prove the same node ID with all three existing dashboard role credentials.
5. Verify fresh checks, peer convergence and notification delivery history.
   Preserve conflicts and receipts; do not clear them to make migration look
   successful. Follow [coordinated upgrades](upgrades.md).

Empty TLS mode preserves legacy HTTP only when there is no retained certificate.
Once prepared, empty mode inherits native TLS. Use explicit `off` during staged
migration. Corrupt, missing previously recorded or expired identity fails closed;
restarting does not silently generate a replacement certificate.

The combined legacy stack still uses `http://node:3100` unless explicitly
configured otherwise. Do not enable TLS on its node and leave the dashboard's
local address on HTTP. For a fresh combined setup, start the node first, obtain
its fingerprint, then configure `CANARY_LOCAL_NODE_URL=https://node:3100` and
`CANARY_LOCAL_NODE_TLS_FINGERPRINT` before the dashboard's first enrollment.
Existing bundled registrations require an explicit verified migration; changing
environment variables alone does not replace established trust.

For an existing bundled registration whose origin changes, configure the new
local URL/pin in the dashboard deployment, migrate the node, then have a
dashboard administrator PATCH `/api/dashboard/nodes/<existing-registration-id>`
through the authenticated dashboard API with:

```json
{"label":"Local node","url":"https://node:3100","tlsFingerprint":"<new complete certificate fingerprint>"}
```

Use the existing label and actual destination. The server verifies the same
node ID and all three saved roles before updating the existing encrypted entry.
It must not create a second registration or fresh role accounts. The bundled
fields remain read-only in the UI because deployment manages them.

For a certificate-only change at the same origin, first temporarily disable
local auto-management by explicitly setting both local URL and token to empty
in the web service's environment mapping, then restart the dashboard. In a
combined template, setting an empty `.env` value is insufficient because its
`:-` fallback fills the default; override the mapping itself. Perform the
authenticated verified PATCH, then restore the configured local URL, token and
new fingerprint and restart the dashboard again. Keep every volume and
registration ID. This is an operator migration, not automatic trust replacement.
Private fixture checks cover retained identity/roles and refusal of a wrong
pin; real Compose/Portainer deployment remains a separate acceptance step.

## Native TLS for nodes connecting out

For a dashboard host reachable by the nodes at `172.16.99.101`, a dedicated
native TLS listener can use:

```dotenv
CANARY_DASHBOARD_CONNECTION_URL=https://172.16.99.101:18348
CANARY_DASHBOARD_CONNECTION_TLS_MODE=auto
CANARY_CONNECT_BIND_ADDRESS=172.16.99.101
CANARY_CONNECT_PORT=18348
```

The Compose/Portainer templates publish host port 18348 to container port 8081.
Use an address assigned to the host and reachable from the nodes. Keep the
browser dashboard on its existing origin and proxy; the dedicated connection
origin must not require interactive SSO. It authenticates Canary invitations
and retained node credentials itself.

The dashboard prints `Dashboard connection TLS certificate fingerprint:`. The
wizard includes that pin in `CANARY_DASHBOARD_TLS_FINGERPRINT` and displays it
for comparison with the dashboard's own log. This certificate fingerprint is
different from the node's enrollment-device fingerprint, which must still be
compared before confirming the first pairing.

If an HTTPS reverse proxy terminates the node-connection endpoint instead,
leave native connection TLS off and use its trusted public certificate. Do not
advertise the backend certificate's pin as trust for a different proxy
certificate. The listener serves connection routes, not the browser login API.

After pairing, remove only the one-time enrollment token. Keep the URL, TLS
fingerprint and identity volume. An explicit pin/origin change without updating
the retained connection is refused rather than accepting an impostor.

## Expiry and explicit renewal

Inspect the expiry with `canary tls-info`. Renew before expiry when practical.
An expired certificate can still be inspected and explicitly renewed, but
cannot start a TLS listener. Renewal replaces only the server certificate/key;
it does not replace node identity, monitoring data, accounts or cluster tokens.

Stop the owning service and run, with its retained identity directory and ID:

```sh
canary tls-renew <current-complete-certificate-fingerprint>
```

The command requires the old fingerprint and the identity lifetime lock. It
prints the new public fingerprint. If an answer is lost, inspect `tls-info`
before trying again: the old fingerprint cannot repeat the renewal. Update
every caller's pin over a trusted channel before reconnecting. Keeping an old
pin deliberately refuses the new certificate.

For the dashboard's native connection certificate, stop the dashboard and use
`CANARY_NODE_ID=dashboard-accounts` and
`CANARY_IDENTITY_DIRECTORY=/identity/auth-origin` with the same dashboard
identity volume and container user. Then explicitly update each outbound
node's retained trust, while that node is stopped:

```sh
canary trust-dashboard <old-complete-fingerprint> <new-complete-fingerprint>
```

Set `CANARY_DASHBOARD_TLS_FINGERPRINT` to the new value before restarting. This
retains the same origin, device credential and registration. The literal
`system` selects ordinary CA/hostname trust instead of a pin; use it only when
the configured HTTPS endpoint has a certificate that the node normally trusts.
The command never downloads or automatically trusts a remote certificate.

Preserve the latest identity directory across database restores: the private
retained TLS file is authoritative and is not rolled back with an older DB.
Restore guidance is in [backups](backups.md). Do not edit or delete private
identity files to bypass a failed trust check.
