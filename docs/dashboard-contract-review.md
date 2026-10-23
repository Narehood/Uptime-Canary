# Independent dashboard contract review — 2026-10-22

**Two P2 contract defects remain.** Existing Kafka checks with an empty message and RabbitMQ checks with an empty password cannot be saved through the monitor form, even for a rename. A separate P3 forward-compatibility test loses an unknown outer service field. No credential crossing or public projection leak was reproduced in the tested cases. This is not release certification.

Only the new `scripts/dashboard-contract-review.mjs` and this report were authored. Product, backend, shared contracts, module files, root documentation and Opus artifacts were read only. No commits, `.env` reads, user database access, port-3100 connections, existing dev-server use, or external notification sends were performed. Temporary builds, source copies, databases and browser profiles are under `.canary/dashboard-contract-review/`.

## Findings for the product owner

### DASH-01 — P2: an accepted empty Kafka message prevents all form saves

Trigger: create a `kafka-producer` monitor through the API with `service.kafka.mode: "produce"`, a valid topic/broker, and **`message: ""`**. Open its edit form, change only its name, and select **Save changes**.

Observed on the real Go API and built dashboard: creation returns **201**. The browser stays on the edit page with **“Enter message.”** The stored name and revision remain unchanged. An empty Kafka record is an explicit payload; it differs from omitting `message`, which Go rejects. The harness reproduces both the pure builder rejection and the browser failure.

Sources: [`serviceSpecs.tsx:210`](../apps/web/src/serviceSpecs.tsx#L210) marks the message required; [`serviceSpecs.tsx:399`](../apps/web/src/serviceSpecs.tsx#L399) deletes empty textarea values and reports missing input. [`MonitorForm.tsx:497`](../apps/web/src/views/MonitorForm.tsx#L497) rejects the save even after restoring unchanged option objects. [`kafka.go:56`](../apps/node-go/internal/services/kafka.go#L56) rejects a nil message, while allowing an explicit zero-length message.

Requested fix: represent an explicit empty Kafka payload and retain its property in the request. Simply making the field optional while deleting empty values would still produce a backend rejection. Add create and rename coverage for `message: ""` versus omitted `message`.

### DASH-02 — P2: an accepted empty RabbitMQ password prevents all form saves

Trigger: create a RabbitMQ AMQP check through the API with a nonempty username, `password: ""`, loopback address and `tlsMode: "plain"`. Change only the name through the dashboard.

Observed: Go returns **201** for the configuration. The edit form displays **“Enter password.”** and leaves the stored name unchanged. The same builder governs new monitors, so this accepted configuration also cannot be entered through that form. This was reproduced independently of the Kafka case, in both pure and browser checks.

Sources: [`serviceSpecs.tsx:202`](../apps/web/src/serviceSpecs.tsx#L202) declares the RabbitMQ password required; the common empty-value handling is at [`serviceSpecs.tsx:402`](../apps/web/src/serviceSpecs.tsx#L402). [`rabbitmq.go:56`](../apps/node-go/internal/services/rabbitmq.go#L56) permits a zero-length password; it bounds its size and encoding and requires the username.

Requested fix: match the RabbitMQ validator's empty-password semantics without weakening unrelated required credentials. Verify both a new check and a name-only edit.

### DASH-03 — P3: editing service fields drops unknown outer service siblings

This is a **synthetic forward-compatibility finding**, not observed loss of currently persisted Go configuration. Start from a returned Redis monitor; add `service.futureEnvelope = { enabled: true }` and `service.redis.futureOption = { nullable: null }` in the isolated builder test. Change only the Redis address.

Observed: `service.redis.futureOption` survives, but `service.futureEnvelope` disappears from the outbound input. A name-only edit preserves both because the unchanged-object restoration applies. [`MonitorForm.tsx:406`](../apps/web/src/views/MonitorForm.tsx#L406) rebuilds the outer service object with only the selected protocol. The restoration at [`MonitorForm.tsx:492`](../apps/web/src/views/MonitorForm.tsx#L492) cannot preserve it once that protocol changes.

The current Go `ServiceMonitorOptions` DTO does not persist such an unknown sibling, so this is not evidence of current server-side data loss. It does contradict the stated preservation contract for a newer node's options. Preserve unknown outer fields when editing the same service type, in addition to cloning its inner protocol object.

## Reproduction and evidence

From the workspace root, after the required browser-tool attempt:

```powershell
node --check scripts/dashboard-contract-review.mjs
node scripts/dashboard-contract-review.mjs
```

The harness copies and hashes the source before building, byte-checks each copy, builds an owned Go executable and Vite bundle, typechecks the web workspace, and starts two independent nodes with fresh databases, a local HTTP/webhook fixture and a private Vite preview on random loopback ports. Service checks and notification channels are disabled except explicitly identified loopback/manual scenarios. Discord configuration is saved for preservation checks and never tested or sent. Browser requests are allowlisted to these fixtures. Requests record credential **presence only**, never their values.

The supported inspection mode is `node scripts/dashboard-contract-review.mjs --fixture`, followed by `node scripts/dashboard-contract-review.mjs --run <printed-run-directory>`. Creating `STOP` in that directory stops its owner. Normal runs stop their owned processes automatically. Fixture tokens are scrubbed from the private state file at shutdown. All owned processes were confirmed stopped after this review.

Final execution: **2026-10-22 18:27:45–18:28:39 UTC**, Node **v24.22.1**, Go **go1.27.3 windows/amd64**, exit **1**. Artifacts: `.canary/dashboard-contract-review/run-pPe8u2/`, particularly `results.json`, `source-manifest.json`, `build.log`, and the built `dist/`.

**109 passed, 5 failed, 0 browser exceptions, 0 blocked requests.** The five failures are two assertions each for DASH-01 and DASH-02, plus one for DASH-03; they are not five independent product bugs. Web typecheck and private Go/Vite builds passed. Vite emitted its existing bundle-size warning.

The preparatory `run-m4q9Fn` had one unclassified first-request `fetch failed` in its Docker case. The final fresh run passed all Docker checks; this transport failure is not presented as a product finding. Earlier harness development attempts are superseded by the final run.

T3 sequence: `preview_status` reported no tab; `preview_open` succeeded after correcting an unsupported `about:blank` argument. Both following `preview_snapshot` attempts returned the identical raw error:

```text
Preview snapshot failed: Preview automation snapshot timed out after 15000ms.
```

The documented headless Chrome/CDP fallback in `docs/auth-ui-validation.md` and `.canary/opus-web/proof.mjs` was then used. UI operations use real DOM input/change/click events; private exports of builder functions provide supplementary contract assertions. The shipped bundle is built before adding exports to separate `.review.tsx` copies.

## Verified behavior and boundaries

- The catalogue contains all 25 check types. The 16 service forms cover their Go DTO field names, including nested TLS and Kafka SASL fields. Oracle intentionally has no certificate-verification override. Every service protocol passed a real API rename, exact stored-option comparison, applicable one-field builder edit, and browser rename. This establishes schema coverage and the tested round-trips, not every possible validation boundary or protocol exchange.
- HTTP, ping, TCP, TLS, DNS, WebSocket, SMTP, manual and push options survived API/form/browser renames. HTTP credentials, assertions, explicit zero redirects, retries, inversion and the legacy destination survived an actual browser pause. Top-level optional nulls were omitted; nested unknown values/nulls survived `inputOf`, without sharing references. Explicit ping payload zero and fractional TLS validity days survived.
- All 16 service conflict-row projections retained options/credentials and produced server-accepted management inputs. This used ordinary revision-checked PUT validation, **not a real multi-head conflict**. Full partition/merge resolution was not exercised.
- Six channel providers preserved their stored credentials and applicable provider options through API and browser renames. Go's canonical empty inactive-provider structs were compared after saving. Saving channels sent no message. Channel test/fanout/retry delivery semantics were not re-proved by this suite; its only active destination was the local fixture.
- All six maintenance strategies preserved schedule fields through API and browser renames, including IANA timezone, weekly/monthly selectors and cron. The node compiled them. This was not an independent exhaustive DST-engine test.
- Push tokens generated by rotation were distinct 64-character lowercase hex strings. Rotation and GET logging warnings appeared; keeping the current token retained the token and generation anchor. A push metadata rename retained its anchor. Saved rotation/heartbeat HA behavior was not certified.
- Endpoint switching cleared an actual monitor credential draft and all three prior authentication regressions: account password drafts, a real one-time issued token, and an actual pending MFA setup. The issued token authenticated only on its issuing node. After switching, the old MFA seed/password and confirm control were absent. Nonsecret `connection.identity` remounts and stale-async guards must be retained.
- A real create response from A was held through CDP until after connecting to B. The write existed only on A; releasing it neither replaced B's draft nor changed its route or published A's success notice there.
- Public responses omitted private monitor/node IDs, names, diagnostics, credentials and nonpublic tags. Only explicitly selected public tags appeared. Public browser fetches carried neither Authorization nor cookies while signed in. Published HTML-like text rendered as text; script URLs, userinfo, query-bearing and path-bearing node addresses caused no public fetch. Viewer responses redacted service/channel/push secrets and viewer forms prevented writes.
- Browser storage contained only endpoint/theme preferences, with no session storage or cookies; reload discarded authentication. Unmeasured manual latency rendered as a dash, history stated its sample denominator and SLA limitation, and a real administrative closure displayed **Closed after config change**, with a retirement marker from Go.

## Pinned versions and release limits

SHA-256 values for the final execution:

```text
review harness
501ab3d3cc31820f548568eda40d286fc02a7873850f1b2e61636598be080eb1
source-manifest aggregate (SHA-256 of JSON.stringify(files))
c6fa9cfafca270728b6026860164e4f858fcfd2e18bc4a31fc8f05ee27c3cac6
review-node.exe
d5f1840268269a64be95f257551d32f1c6410dc5fb68ba7b39846fa2aa7889ba
dist/assets/index-D7QyyEXt.js
e5e74d071cc058c5648c74835161198650618c1d9e4c6f0f70d63f80189792b9
dist/assets/index-B9cjoy9V.css
c0817d7e81bbdff2e387708f3171ac8c833507f9202e74715435e2447cd7f469
apps/web/src/serviceSpecs.tsx
6fb3141f589b2994b2d8a8b2e071fa0f505fe0ba2fc1fe79eb3ff5b0f1293a3f
apps/web/src/views/MonitorForm.tsx
dccca7f5201ea08c0b13e7c1a212de4f190b34e2df12a5b76c42f7d04ae5005d
deploy/web.conf
31bfd4684aebd47ad153f093588461255afcae862fd06d53ae591b35eb87d836
```

Every copied source file still matched its manifest after execution. In the live workspace, `go.mod` changed during the run: the unused `github.com/jsonata-go/jsonata` requirement was removed. The reviewed copy stayed unchanged (`go.mod` SHA-256 `2c95b02cd9f1c89cb56b40eccbe3feeb35bc772fd5bd0f7153ad821ea099e77a`). Results apply to the pinned build; they do not certify a subsequent dependency graph or source change.

Read `docs/design.md`, the Opus fixture/proof, its run results, management/shared contracts and the relevant Go validators. The three recent successful Opus runs (`run-mRK67E`, `run-huw7GC`, `run-SVOlmy`) each report 24/24; older failed attempts were also inspected. Those passes and this review do not establish release readiness for HA push/coordination, container deployment or untested external protocols.

The nginx source repeats `X-Content-Type-Options` and `Referrer-Policy` in both locations that define `Cache-Control`, addressing `add_header` inheritance there; `/healthz` inherits the server headers. **No fresh container build or nginx response-header test was performed.** Docker and nginx executables were unavailable on PATH. Validate the built web image's `/`, an existing `/assets/` file, missing assets and `/healthz` before approving that deployment fix. Opus owns the product fixes above; this reviewer changed no product code or visuals.
