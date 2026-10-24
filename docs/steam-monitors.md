# Steam directory monitoring

Protocol 17 adds `type: "steam"`. Local Windows native, process, static and browser
acceptance pass with the exact scopes below. Hosted Linux/Alpine, native arm64,
race and independent review remain pending. Nothing in this document certifies a release or a
real request to Steam.

## What the check means

Each assigned node resolves the game host, preferring IPv4 when DNS returns both
families, then requests `/IGameServersService/GetServerList/v1/` from its configured
directory API. The query contains its API key, the exact `addr\IP:port` filter,
and `limit=1`. Up requires a complete response containing the same IP and game
port. A complete empty list, including an otherwise empty `response` object,
reports Down. A nonempty list that does not match the requested address provides
neither positive membership nor authoritative absence and follows the API-failure
policy. Duplicate or case-confusable authority keys, error envelopes, invalid
addresses, truncated JSON and oversized responses also follow that policy.

This follows the directory-based monitor in the pinned
[Uptime Kuma 2.5.6 implementation](https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/monitor-types/steam.js).
It does not perform an A2S/game-protocol handshake or establish that a listed
server is currently reachable. Steam's general
[IGameServersService reference](https://partner.steamgames.com/doc/webapi/IGameServersService)
does not document this particular method; the pinned executable upstream
implementation is the compatibility reference, rather than a claimed complete
Valve method specification. Canary additionally checks the returned address,
keeps provider failure distinct from game absence, and emits fixed summaries
instead of server names or remote diagnostics.

Optional ICMP measures one actual round trip to the resolved game address after
positive directory evidence. It uses at most one quarter of the remaining check
budget, capped at one second. A missing, denied or failed ICMP measurement leaves
the positive directory result Up and marks latency unavailable. API duration and
HTTP status are never reported as game RTT/status. Disable measurement to avoid
ICMP entirely. This is separate from the GameDig catalogue, which remains a gap.

## Configuration

The target is `host:port`, or `[IPv6]:port`. The port is canonical decimal
1–65535 without leading zeroes. Host names follow the existing ICMP target
validator: no schemes, paths, zone identifiers, shell arguments or control
characters. Normal monitor timeout, placement, interval, retries and inversion
continue to apply.

```json
{
  "type": "steam",
  "target": "game.example.com:27015",
  "steam": {
    "apiKey": "replace-with-a-private-api-key",
    "skipLatency": true
  }
}
```

This is an options fragment, not a complete monitor creation request. The normal
required monitor fields, including name, timing, node IDs and enabled, are still
required. Additional monitor-type options are refused.

| Steam field | Contract |
| --- | --- |
| `apiKey` | Required, 1–256 printable ASCII bytes without whitespace |
| `apiUrl` | Optional trusted API base URL, default `https://api.steampowered.com`; a clean path prefix is allowed |
| `apiCa` | Optional additive API HTTPS trust, at most 128 KiB of complete certificate PEM blocks; system roots remain |
| `allowInsecureApi` | Default false; explicit permission for a configured plain HTTP API |
| `apiFailureIsDown` | Default false; provider/API failures report Pending, or Down with this opt-in |
| `skipLatency` | Default false; true disables the optional ICMP measurement |
| `packetSize` | Optional integer 0–65,500; omission uses the ICMP engine's 32-byte default |

Nested options reject unknown, duplicate, differently cased and null fields, and
incorrect JSON types. The URL rejects embedded credentials, queries, fragments,
escaped/dot path segments and an empty explicit port. There is no verification
bypass: HTTPS verifies the API host and chain with TLS 1.2 or newer. Custom trust
applies to the directory API, not to a game-server handshake.

## Failures, privacy and bounds

Transport/TLS errors, rejected credentials, quotas, non-200 statuses, malformed
or untrusted directory evidence, and the monitor's own API deadline are Pending
by default. `apiFailureIsDown` makes those provider failures Down, which can
trigger the ordinary retry/notification policy. A caller-canceled or superseded
check remains Pending regardless of that setting. DNS failure for the game host
is Down. Pending never proves recovery from an existing incident.

Every assigned node sends its own directory request. The published
[Steam Web API terms](https://steamcommunity.com/dev/apiterms) state a 100,000-call
daily limit; other checks and applications also consume calls, and compatible
custom APIs can have different limits. The interface's per-monitor estimate is
guidance, not a shared quota reservation or rate-limit guarantee.
The trusted API receives
the resolved game IP/port and the key in the request query; consider both before
configuring another API host or opting into HTTP. Keys and custom trust belong
to replicated private configuration, not to public status data. Viewer overview,
monitor-list and conflict projections omit the entire `steam` options object.
Probe observations contain only fixed summaries, with no remote name, key-bearing
URL, certificate PEM or provider error text.

The client does not use environment proxies, cookies, redirects or connection
reuse. There is no internal retry: only the monitor scheduler retries. DNS and
API work share the monitor deadline (maximum two minutes). API response headers
are capped at 32 KiB, decoded body at 64 KiB, JSON depth at 32, visited values at
16,384 and returned server rows at 128. The row bound and exact membership test
do not claim full validation of every optional Steam metadata field. Optional
ICMP uses the existing fixed-executable, bounded engine.

## HA and acceptance

No new storage category or mutable shared Steam key is added. Ordinary monitor
revision edges carry the options; options and target changes start a new check
generation. Renames preserve generation and the complete options object. SQL
admission rollback must leave both in-memory and durable heads/edges unchanged.
Peer protocol 17 refuses older peers; upgrade cluster members together.

Authored tests use in-memory keys, generated local TLS trust and loopback APIs.
They cover membership, failures, timeouts/cancellation, IPv4 preference, strict
decoding, redirect isolation, real-RTT attribution, peer persistence/restart,
viewer projections, generation changes and SQL rollback/retry. A production
process proof uses two private nodes and a fake directory with no real game
server or external notification destination. The process proof also creates
different option heads during a real partition and resolves the complete head
set after reconnecting. Execution evidence is limited to the checks below.

### Initial Windows checks and API-prefix repair

The first artifact-producing wrapper stopped with a Python `SyntaxError` before
starting a compiler or test (4.266 seconds, source unchanged, scratch zero).
Only the wrapper was corrected. The subsequent focused run on v3 source
`73399592c5dfbcb8acf5cb9276f3f9e9b17445cdc6d5a2349d2807ac53be36fa`
passed:

- Nine Steam leaf and four canary test functions, with two additional leaf
  subtest events: 15 passing test events, zero failures/skips, 150.828 seconds
  including compilation. This is not the complete canary/node suite.
- Workspace typecheck, 10.375 seconds; the original private Steam pure fixture,
  **15 groups**, 0.640 seconds; and all 12 system-service pure groups, 0.282
  seconds. The original fixture handoff's count of 16 was a counting error.
- Browser-harness syntax check. No browser or process acceptance ran in this
  command.

The guard completed in 179.031 seconds with unchanged source and unchanged
private fixture SHA-256
`09712b5747bc57ef74101bfdcc52fbbb087918b47a66def81c51952cf10d0724`.
Sampled scratch peaked at 415,856,476 bytes, below 512 MiB, and returned to zero;
shared modules did not grow.

Further review found that the form rejected a clean API base prefix ending in
the directory method's name, although Go admits that prefix. This could block
renaming a stored configuration. The added Go admission controls passed in
31.344 seconds. The separate desired-behavior UI regression failed, with the
other 15 groups passing:

```text
FAIL Go-admitted API prefixes remain editable without changing stored options: /IGameServersService/GetServerList/v1/
```

The reproduction guard's exit zero means **expected failure reproduced**, not
application acceptance; its UI child exited one. Source was unchanged at
`d3dd77774e7e2b99a967cec18342513dff1c1cc0422b52e7dc6f45f88dd0dc1a`.
The original private fixture had also imposed that stricter suffix rule. Its
bytes remain preserved as historical evidence. The canonical tracked
`scripts/steam-pure.mjs` records that original assumption in a comment, corrects
it to the independently executed Go contract and retains the new regression.

Opus removed only the extra form suffix refusal, without changing Go or its URL
security rules. The repaired static run on source
`9ecef5b16b0452a7ecb3d1b4e463bd163e156a33a91fa123522438354047130a`
passes workspace typecheck (15.078 seconds), all **16 tracked Steam groups**
(0.687 seconds), all 12 system-service groups (0.297 seconds), browser-script
syntax and the dashboard production build (7.093 seconds). Guard time was
27.454 seconds; source was unchanged, scratch returned to zero and modules did
not grow. The repaired `steam.tsx` SHA-256 is
`7cb839e0d1adb1eef958e89434329fbe5e6954074abd72880549d95ca470b9a6`.

The protocol-16 native/container runs on `256c7f2` predate Steam and do not
validate these new files. Post-merge workflows on `187f3ae` also cover protocol
16. Actual protocol-17 Linux/Alpine, native arm64 and race remain separate
gates; the local native/process/browser runs below are separate evidence. No real Steam API availability, quota behavior,
game-server reachability, full Kuma option parity or release publication is
certified by local fixtures.

### Full local acceptance and CA-whitespace review

On executable/test source `9ecef5b16b0452a7ecb3d1b4e463bd163e156a33a91fa123522438354047130a`,
the sequential Windows node suite passed 3,783 test events across 20 packages
with tests (plus the command package without tests), in 371.047 seconds.
Four platform skips remain: both Unix question-mark recovery cases, the origin
symlink subcase, and the Docker Unix-socket test. Node-wide vet passed in
99.109 seconds. This was CGO-disabled native testing, not a Windows race run.
Sampled scratch peaked at 446,017,207 bytes and returned to zero.

The Steam production-process proof passed 1/1 in 4.578 seconds after building
in the guard. It exercises two real private peers, restart, partition-created
heads and exact complete-head API resolution. The dashboard contract passed
148/148 with zero page exceptions or blocked requests against the identical
development binary, SHA-256
`cbfa8096bd5b2c6e557c648c76d1fcc5add20a767dddbebfe8694f58dbe9ab95`.
The browser conflict check is a projection; actual peer resolution belongs to
the separate process proof. Native preview snapshots failed twice with
`Preview snapshot failed: Preview automation snapshot timed out after 15000ms.`
The documented Chrome/CDP fallback used private loopback fixtures and en-US.
All disposable databases, profiles and binaries were removed by the guard.

A subsequent source review reproduced another metadata-edit mismatch: Go
accepts a CA consisting solely of Unicode NEL as blank, while JavaScript trim
left that value nonblank and the form refused it. The new Go positive control
passed (32.484 seconds), while the desired-behavior pure regression failed
(16 groups passed, one failed). Source was unchanged at
`385ed67c746f81a234a5c5c7397b973bd969e824849baf56013fa42c2d72f2ac`.
An expected-failure reproduction is not an acceptance pass.

Opus repaired validation to use Unicode White_Space, apply the raw 128 KiB byte
limit first and preserve an unedited stored CA byte-for-byte. BOM is not blank;
inside PEM, only Go-compatible base64 whitespace is admitted. The repaired
`steam.tsx` SHA-256 is
`67d9f938880d15cdfbdd353f72fb82569ec6ee73da10851858979991398f86d5`.
On source `0bfa6c0722c4aaba1d3e0132caaad928baaa40f98a64af486beb4c2f3708f1e8`,
workspace typecheck passed (8.297 seconds), all 17 tracked Steam pure groups
passed (0.281 seconds), and the production dashboard built (0.625 seconds).
Guard time was 12.625 seconds, source unchanged, scratch returned to zero and
shared module growth was zero. Earlier all-Go and process evidence covers the
unchanged Go production code; the CA-repaired dashboard subsequently passed 148/148 browser checks with zero
page exceptions, blocked requests or failures. The guard completed in 131.219
seconds with unchanged source, scratch peak 305,856,193 bytes returning to zero,
and no module growth. It used the same Go binary as the earlier process proof;
its JavaScript bundle is 780,698 bytes, SHA-256
`7b892dbfe83e28729140439b95af3f935e7587410e115eef4ca89ff4d223dd17`.
This continued the documented CDP fallback rather than claiming a new native
preview pass. No hosted protocol-17 result or release approval is claimed.
