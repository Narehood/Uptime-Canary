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

The final guarded scan passed in 18.906 seconds, with source unchanged and zero
scratch after cleanup. Its source-v2 fingerprint was
`0b1efdf70176d6348821dea2d380dcb90879069c604092a62b8048884faa7490`.
The source/runtime fingerprint before and after was
`47214cc923227935596233069092341aea7d3e0c03f8266085096e6afa3e0298`.

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

The first execution and its fixture repair are recorded below. Existing
protocol-14 results remain linked in [container validation](container-validation.md);
an earlier run is not relabelled as covering later checks.


### First expanded CI run: fixture dependency repaired

On `9add261`, workflow 39989316293's new Alpine proof job passed all 17 tests
with zero failures/cancellations/skips/todos, exit 0 and no OOM. It also passed
the actual isolated Compose template check, including private identity storage,
management CORS and restart persistence. Its artifact is 12517177682. Web passed.

The separate node job stopped before application checks, during fixture health.
Artifact 12517212241 retains the actual fixture-container traceback:

```text
File "/fixture.py", line 17, in <module>
  from validation_workspace import tap_counts
ModuleNotFoundError: No module named 'validation_workspace'
```

The fixture image deliberately contains one standalone stdlib script. Moving
that proof-only import into `validate_proof` restores that contract; no app,
probe deadline or expected outcome changes. A new isolated-interpreter test
loads the copied fixture with `python -I -S`, without repository helpers, and
passes. The original failed CI evidence remains intact.

### Final expanded CI run

Commit `4d51a6a4ba956f03104375ad1e9e3f7056eeecbb`, workflow
[39991889659](https://github.com/Narehood/Uptime-Canary/actions/runs/39991889659),
completed successfully at 2026-10-23 17:20:00 UTC. All four jobs passed:

- Node: all 11 original container checks.
- Web: original image build, health, assets, security/cache headers, SPA fallback
  and nonroot runtime.
- Proof: all 17 real-process tests against the original node image's binary,
  under the restricted Alpine policy; zero failures, cancellations, skips,
  todos or OOM. The actual Portainer Compose template also passed readiness,
  CORS, separate private identity storage and persistence across restart.
- Race: 869 core and 1,527 worker-parent passing test/subtest events; zero
  failures, skips, unfinished tests or race warnings. Helper packages passed
  `-race` separately. Capped production children remain uninstrumented.

All four retained artifacts were inspected in memory. Their identical
560-entry source manifest matched the checkout, SHA-256
`acc6e40b05493328d7d2f326682b1386966d579c7612558e35988b0392c6cc56`.
Artifact IDs: node `12517912923`, web `12518485567`, proof `12519280491`,
race `12518542677`. The race artifact SHA-256 is
`ebcaf4fd4264a25f1744189fcb3655c62ec9ff8049116f3f009506f7931b58bf`.
Both race-parent phases used production worker SHA-256
`5b9586d0d4a36b6aefd70ff4cff50493fbed2fb01aaf2454efdc96ef50fbed04`,
identical to the earlier runtime checkpoint.

Production runtime and dashboard source are unchanged from `77416a1`.
This is a coherent development acceptance milestone on Windows and Linux
amd64, not a tagged release, arm64 execution or complete Kuma-parity claim.
The repository remains about 1.17 GiB with zero scratch, below its 1.25 GiB
idle target; no CI archive, binary or private fixture database was kept locally.
