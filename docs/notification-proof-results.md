# Notification process proof results

## Current protocol-13 development checkpoint, 2026-10-23

Root ran all 17 current process scenarios sequentially with one temporary Go
binary through `scripts/bounded_validation.py`. All 16 existing scenarios passed.
The new `messaging.test.ts` scenario failed only because its helper expected an
UP sample from both peers for a monitor intentionally assigned to one peer.
The failing run remains **16 PASS / 1 FAIL / 0 SKIP**, 411.645 seconds of Node
test execution (438.781 seconds including the guard and build).

Only that new fixture was corrected to check the assigned node. Its standalone
rerun passed in 30.696 seconds (63.406 seconds including build/guard). No
production source or older proof assertion changed. This is separate passing
evidence for all scenarios, not a claim of a single clean 17/17 invocation.

Both runs used binary SHA256
`47a10d19bfce7d1e1591d72c4bd1321be3244da767f16dec9e8d7559d300a8e6`.
The corrected run rechecked that hash afterward. The new proof covers all five
native Slack, Telegram, ntfy, Gotify and Pushover wire formats, connected
per-destination receipt exchange, genuine sender SIGKILL/failover, restart
without replay, five recoveries, and an ntfy 429 eight-second retry deadline
surviving another process death and SQLite reopen. Other accepted destinations
do not resend. All servers are loopback fixtures; no real provider is contacted.

The compact record `.canary/protocol13-process-result.json` preserves both logs,
the first failure, exact guarded outcomes, binary hash and final new-proof hash.
All binaries, temporary databases and runtime directories were reclaimed. Module
cache growth was zero and `pauseRequired` was false. The historical v1 guard's
input fingerprint covered Go/JS/JSON/module files, not TypeScript; its hash alone
does not prove proof-source immutability. Guard v2 now covers TypeScript,
frontend styles/HTML, Python and deployment configuration, with regression tests.

The production JSONata port still has its independent semantic blockers. These
process proofs do not clear the private replacement's failed Alpine fit gate,
certify a current race/container run, or approve a release.

## Historical notification checkpoint

Verified on Windows amd64, 2026-10-22, with Node v24.22.1 and Go go1.27.3.
The tests execute the compiled Go program as separate processes, each with a
private temporary SQLite database. Peer authentication tokens are generated
per fixture. HTTP, provider, SMTP, and partition relays listen on loopback.

## Coverage

Four focused proofs were added in `apps/proof/test/notifications.test.ts` with
support in `apps/proof/fixtures/notifications.ts`:

1. Local Mailgun US/EU configurations reach their respective prefixed multipart
   fixture paths with Basic authentication. Brevo and Mandrill use their expected
   paths, authentication, payloads, and provider acknowledgements. A real SMTP
   envelope and message DATA exchange reaches its final 250 acknowledgement.
   Brevo's 429 and eight-second Retry-After survive process SIGKILL and SQLite
   reopening with the same attempt ID and deadline. A rejected Mandrill recipient
   retries independently; already accepted recipients and the legacy webhook
   are not resent. Each destination receives its recovery independently.
   Viewer delivery outcomes retain safe status/error fields while excluding
   addresses, credentials, provider response bodies, URLs, and raw provider IDs.
2. Two connected processes send once per destination to a legacy webhook, a
   reusable webhook, and two email recipients. Successful destination receipts
   replicate. Killing the preferred sender permits the survivor to notify every
   destination; restarting the sender does not replay accepted deliveries.
   During a peer partition, both isolated nodes deliver all destinations.
   Reconnection converges receipt IDs and stops additional sends. Intentional
   partition duplicates are asserted explicitly.
3. A dependency's successful shared webhook holds only that same child
   destination. Failed dependency email cannot silence the child's email.
   Delivery status shows held and accepted destinations independently. Dependency
   recovery releases the held route once across connected nodes without resending
   accepted child email. Both child destinations later recover.
4. Channel API authentication, viewer/editor roles, secret redaction, versioned
   concurrent updates, conflict heads and complete resolution, stale revisions,
   replicated tombstones, and disabled/deleted channel testing are exercised.
   One test batch queues one attempt per recipient, respects the per-node
   five-second batch limit across channels, and creates no monitors, incidents,
   observations, or incident notification receipts. Outcomes remain local to
   the sending node. Protocol 2 peer input is rejected.

The existing seven process proofs remain unchanged: scheduled HTTP timeout,
dependency with no destination, dependency with a failing destination,
user-defined groups, three-node HA, monitor edits/restart, and standalone
HTTPS/ICMP.

## Commands

Run from the repository root in PowerShell. This deliberately uses a separate
binary and does not rebuild or lock the default/development executable:

```powershell
$env:CGO_ENABLED='0'
$env:GOTOOLCHAIN='local'
& .canary/tools/go/bin/go.exe build -trimpath -o .canary/bin/canary-proof-notifications-final-20261022.exe ./apps/node-go/cmd/canary
npm run typecheck --workspace @uptime-canary/proof
$env:CANARY_BIN='.canary/bin/canary-proof-notifications-final-20261022.exe'
node --import tsx --test apps/proof/test/notifications.test.ts
npm run test --workspace @uptime-canary/proof
```

Initial focused runs passed all four new scenarios. The initial full suite
passed 11/11 with no skipped or cancelled tests in 88.1 seconds. On the final
build, proof typecheck passed, and the full suite passed 11/11 in 87.5 seconds:
zero failures, cancellations, or skips. This final build includes the later
coherent runtime integration and the additional viewer check against a pending
429 outcome containing a deliberately private provider response body.
No runtime contract failures were observed.

Final binary SHA-256:
`08010e95cc09bce6d989af415522423d48c34c404161821f5867a7fb2895596b`.

## Limits

These results demonstrate local provider/SMTP acceptance and runtime scheduling,
not inbox delivery, live provider credentials, bounce handling, or live Discord.
Mailgun region configurations use explicit local base URL overrides; they do not
verify public US/EU DNS routing. SMTP uses explicit unauthenticated plaintext on
loopback; SMTP TLS/authentication behavior belongs to the provider package tests.

The group process proof uses a named webhook/email pair. Exact Discord host,
route, payload, confirmed HTTP 200 message ID, and 429 behavior are covered by
the runtime's injected-Doer tests in `notification_provider_test.go`; the parent
also checks per-destination grouping in `channels_test.go`. No host DNS, hosts
file, machine trust store, or production test routing is changed for these
process proofs. No external email or Discord message was sent.

Tests register cleanup for their Go children, temporary databases, peer relays,
and provider sockets. No default port 3100 application or database is touched.
After the final run, a process query found no notification-proof executables
running, and the system temporary directory contained no
`uptime-canary-proof-*` directories. Both isolated generated executables were
removed after recording the final binary digest. The default/dev executables
were not modified. No commits, module changes, or root package changes were made.
