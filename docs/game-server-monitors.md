# Game server checks

`gamedig` is one optional integration with GameDig 5.3.3. The independent Go
node keeps scheduling, retries, inversion, observations, incidents, SQLite and
peer reconciliation. A separate Node query service runs the game protocol.
The base node and dashboard images do not contain that service or GameDig.

The selector contains 358 IDs from upstream commit
`aff6afd252dc1c4853aae7522423623b7b5adec7`. This is a catalogue, not evidence
that 358 protocols have each been exercised. Literal metadata was extracted
without executing upstream source. The installed npm `lib/games.js` matches
the recorded raw Git SHA-256; this does not establish whole-tree Git/npm
equivalence. The separate package lock pins the complete runtime dependency
tree. Its original notices are retained beside the service.

## Optional service

For a source checkout, generate a separate token, set it in your deployment
environment and enable the `game-query` Compose profile. For example:

```sh
openssl rand -hex 32
docker compose --profile web --profile game-query up -d --build
```

Put the generated value in `CANARY_GAME_QUERY_TOKEN` before deploying. Do not
reuse an administrator, cluster, dashboard or push credential. This command
uses your normal deployment settings; agent validation never reads `.env`.
With that profile, nodes in the same Compose network can use
`http://game-query:3121`, the separate service token and explicit permission
for plain HTTP. No query-service port is published on the host.

`deploy/portainer-game-query.yml` is an optional standalone stack pulling
`ghcr.io/narehood/uptime-canary-game-query`. Check that a matching tag exists
and allows anonymous pulls before deploying. A publishing workflow in source
does not establish package availability. The default host binding is
`127.0.0.1:3121`; another container cannot reach that through its own loopback
address. Provision a private shared Docker network or a private reachable
interface for the node. Do not put this API on the public internet. Across
untrusted networks, use HTTPS with ordinary certificate validation.

The service runs without root, with a read-only filesystem, no added
capabilities, no privilege escalation, a 256 MiB container limit, one CPU and
64 PIDs. It needs outbound access only to the game servers you intend to
monitor and their DNS. Its bearer grants query authority to those reachable
addresses. It is an administrator-trusted network service, not a sandbox for
hostile code or a public game-query API.

Node's permission mode reduces accidental parent-process filesystem access;
it is not a security sandbox, and its permissions do not propagate to worker
threads in the supported Node 24 profile. Worker V8 heap limits do not bound
Buffer/ArrayBuffer/native allocations. Keep the external container limits.

## Monitor contract

The target is one literal IP or ASCII hostname, without a scheme, port,
credentials or command syntax. GameDig options are:

```json
{
  "game": "minecraft",
  "port": 25565,
  "givenPortOnly": true,
  "endpoint": "https://game-query.example.internal",
  "bearerToken": "<separate-query-service-token>"
}
```

`port` may be omitted when the selector lists an explicit upstream default.
Otherwise this supported profile requires a port, before attempting a query;
it does not turn a missing local setting into an outage. `givenPortOnly` requires an
explicit port and prevents GameDig's usual query-port derivation. Optional
`token` is a game-specific credential; it is separate from `bearerToken`.
`allowInsecureHTTP:true` is required for an HTTP service endpoint. The endpoint
must be an origin, without a path, userinfo, query or fragment. Unknown options,
ambiguous JSON keys, null values and unknown game IDs are refused.

Each assigned node makes its own query. It sends only a fixed authenticated
`POST /v1/query`, with a random correlation ID and the `gamedig/5.3.3` profile.
The game-server network measurement originates at that service, so nodes
sharing one endpoint also share its network vantage and availability. This
does not create an independently deployed or highly available query service.
The service never receives a Canary management credential. The service bearer
never goes to a game server. Endpoint, game token and service bearer are private
monitor settings and are omitted for viewers. Raw server names, players,
environment data and protocol errors do not enter observations.

- **Up:** a matching, supported query reply, with GameDig's measured latency.
- **Down:** a completed GameDig game-query failure.
- **Pending:** service unavailable, authentication/profile refusal, malformed
  reply, cancellation, deadline, memory failure or no free query slot. Pending
  is not inverted and cannot close an existing outage.

Both the Go adapter and service admit at most two concurrent queries, without
an unbounded queue. The service receives 75% of the monitor timeout. A worker
is terminated and joined before its slot is released. Requests are at most
8 KiB, service replies 2 KiB, worker diagnostics 4 KiB and HTTP headers 8 KiB
at the service. Scripts, arbitrary modules, executables and raw GameDig options
cannot be configured. A game adapter's own protocol activity is still subject
to its upstream implementation; the service does not make every game equally
cheap or remove the need for deployment resource limits.

The service passes GameDig a socket timeout capped at two seconds and at 75%
of its attempt budget. This leaves time for sibling protocol failures to
settle. A reply that misses the shorter socket bound can fail while the overall
query still has time left; a longer monitor timeout does not remove the
two-second socket cap.

## Evidence boundaries

Local Windows tests exercised the actual locked library against a private
Minecraft fixture, plus primitive/Unicode admission, authentication, Origin
refusal and cancellation while both service slots were occupied. A production
two-node process proof also passed against a private Terraria-TShock API,
including Pending, restart, retained outage, recovery and viewer redaction.
The exact-source records are `.canary/completion-gamedig-helper-current.json`
and `.canary/completion-gamedig-process-current.json`; these are local developer
records, not distributed runtime data.

Other game protocols, native Linux/Alpine/arm64 execution, the published service
image and complete release acceptance require their own evidence. No existing
user game server or real notification destination was used by these fixtures.
