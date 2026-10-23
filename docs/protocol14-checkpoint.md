# Protocol 14 combined-source acceptance work

This is development evidence toward the release goal, not release certification.
Production runtime code remains that tested at `77416a1`. Validation scripts and
Goja provenance metadata have additional changes recorded in their guard hashes.

## Real-process and dashboard results

All 17 real-process tests passed sequentially on Windows against a freshly
built node. TAP reported zero failures, cancellations, skips or todos; source,
fixture and binary hashes stayed unchanged. The test phase took 394.500 seconds
and the guarded command 483.219 seconds including its build. Its source-v2
fingerprint was `39b5e9e2983d8a3c2bf9f69aafb142a6579e4b48788c261a28b080d8ca12e5a0`.

The refreshed dashboard contract harness passed 124/124 against the current
node and dashboard: zero page exceptions, blocked requests or changed source
files. It includes all eleven providers and their credential-preserving edits,
viewer redaction, endpoint/MFA boundary regressions and public data checks.
The accepted Globalping addition changes only the old catalogue expectation
from 25 to 26, with uniqueness and explicit Globalping assertions. The prior
113/114 result remains historical. Five providers add ten pure/browser checks.
Its guard fingerprint was `77d5ac1039c2b1bee716a996f58eba16fc4d9745c7b6db599107127fbd4e6b67`,
elapsed 279.484 seconds. The native preview returned
`Preview automation snapshot timed out after 15000ms.` twice; the documented
Chrome/CDP fallback used one disposable profile. It was removed after testing.

Both runs produced the same Windows development binary SHA-256:
`5917b2b0ab61d572f2786616b461bf2e0e56ed7bdfcb2cab1acc5f87a730423b`.
No binary, database, source copy or browser profile is retained from either run.

## Fresh advisory gate

Linux-amd64 source-symbol scanning found no reachable Go vulnerability. npm
reported zero advisories. The existing `GO-2026-5932` OpenPGP finding is module
only, with no imported package or reachable symbol; x/crypto stays v0.58.0.
Explicit version and commit queries found no active advisory for official
JSONata 2.2.2 or the verified local Goja replacement. The gate verifies original
Goja files and its one declared helper before making advisory requests, and
rejects unrecorded code. Original source, helper mutation and identity controls
pass. No claim is made that local patches automatically fix upstream advisories.

The guarded scan passed in 22.437 seconds, with source unchanged and zero
scratch after cleanup. Its source-v2 fingerprint was
`2c0ebfba2de0f84d6875963f454df8213d02069263264ce239bf776b87399be7`.

## Upgrade proof: controlled historical fixture

The optional runner uses three historical files from Git commit
`23e4b13cecd9a2bb7d393867cfa8a257b7325d36` through a declared Go build overlay.
It requires the other shared application Go files to remain unchanged. It uses
current readonly module selection; it is a controlled compatibility fixture,
not an original protocol-13 release artifact. Production has no legacy-mode
switch or evaluator fallback. Temporary source fragments and both binaries
stay inside guarded outputs and are deleted after the command.

The first new-fixture attempt failed before exercising upgrade behavior:

```text
source fingerprint: bb1796b7782d71233c41ad621b01cf6a842348ff6ac41200b69fc8f29d7691ff
create monitor: {"error":"timeoutMs must not exceed intervalMs"}
400 !== 201
```

The fixture had intervalMs=1000 and timeoutMs=3000. The correction uses 3000 for
both, preserving the API rule and all behavior assertions. No product code or
existing independent regression was changed. The corrected upgrade proof passed in 33.439 seconds; guarded build plus proof took 81.109 seconds. It reproduced the legacy false-healthy result first, then proved mixed-version refusal in both directions, retained account roles/configuration/receipts, monotonic sequences, and exactly one down/recovery per fixture monitor. Its source-v2 fingerprint was `af4a3d12d270c9bf38b0d3cfb674b858044dea975f4f4c8ec180ad124262ff47`. The declared legacy binary SHA-256 was `a1e0e61f63089a63645ee63e2fa0dd25f81024c5339cc26630d936f5f583abb4`; the current binary matched the process/browser hash above. Both were deleted after the run.

All runs use one guarded build cache and private loopback fixtures. There are no
real provider sends, paid measurements, user databases or persistent VMs.


## Automated Alpine acceptance extension

The workflow adds a `proof` job. Its test image copies the exact executable
from the original `Dockerfile.node` image, rather than building a substitute
server. It runs the unchanged 17 normal process proofs sequentially with
network access restricted to its own loopback namespace, nonroot UID 65534,
read-only root, no-new-privileges and bounded temporary storage. Zero-match,
skipped or incomplete TAP summaries cannot pass. The controlled legacy overlay
proof remains separately scoped Windows evidence; it is not silently counted
as an original-artifact Alpine upgrade test.

That job also builds the original web Dockerfile and exercises the actual
Portainer template through an isolated Docker Compose project with random
loopback host ports and generated fixture credentials. It checks readiness,
configured management CORS, separate 0700 identity storage, and configuration
and observation persistence across restart. Only that generated project and
its fixture volumes are removed. No user's Portainer host is contacted. The
template now matches the tested read-only-root policy with bounded `/tmp`.

The new Alpine process/Compose checks are pending their first CI execution.
Existing protocol-14 node/web/race results remain those linked in
[container validation](container-validation.md). This extension does not
relabel those earlier runs as covering the new checks.
