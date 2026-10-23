# Protocol 8 process checkpoint

Recorded 2026-10-22. This is a backend development checkpoint, not a release
approval or a claim that the changing dashboard is validated.

The immutable runtime snapshot has manifest
`c96abda45c00c817a13e8dd660b5cde3561947bbaf1862ab4af1fce0bd6885d7`.
It includes the successor-delivery and multi-hop continuity repair, origin and
restore integration, retry/inversion policy, expanded ICMP measurements, manual
checks, public status publication and tags. The Windows amd64 binary was built
from that source with `CGO_ENABLED=0`, `-mod=readonly` and `-trimpath`. Its SHA-256
is `f86cef84a0e0ca8a6bcd8cdcbc3712c10c837fe0138fcee7d4b5ade0813b4575`.

The existing compiled-process suite passed **11/11**, with no skips or failures,
in 88.606 seconds. It exercises connected deduplication, sender death/restart,
partition delivery and reconciliation, successive outages, group suppression
and fail-open behavior, metadata edits, local provider acceptance and durable
retries, credential separation/RBAC, HTTPS and real loopback ICMP.

An additional two-process publication proof passed separately in 1.386 seconds.
It verifies private monitor text and IDs stay out of public JSON, both tag
publication opt-ins are required, private tag values remain private, manual
checks do not invent measured latency, manual state changes retain generation,
tag tombstones survive peer transfer/restart, and disabled publication returns
the same response as a missing slug. Proof TypeScript checking also passed.

Run with a separately built artifact:

```sh
CANARY_BIN=/path/to/canary npm run test --workspace @uptime-canary/proof
```

Exact source fingerprints and command/result references are retained in
`.canary/proof-checkpoints/protocol8-c96a/results.json`; the runtime manifest and
source are under `.canary/alpine-validation/windows-race/snapshots/c96abda45c00c817a13e8dd660b5cde3561947bbaf1862ab4af1fce0bd6885d7/`.
Fixtures used private databases, loopback targets and generated credentials.
No external messages were sent and the user's development node was not used.
Independent review, native race testing and current integrated Alpine/UI
validation remain separate gates. Older protocol 6/7 evidence remains historical.


Native Windows race execution on derivative `816202e2...` also passed the full
canary package: 597.607 seconds, 491 passing events, zero failures/race warnings,
and two expected Unix-filename skips. Only two fixture corrections distinguish
that source from `c96a`; production bytes are identical. The earlier failed run
and targeted repetitions are preserved in [container-validation.md](container-validation.md).
The later SR5 reviewer repros and protocol 9 heartbeat integration were not part
of that frozen suite; this result does not clear them.
