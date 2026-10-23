# Authentication UI validation — 2026-10-22

Independent local functional browser verification of the implemented Connect,
Your account, and Accounts surfaces. Product UI, shared contracts, Go source,
root modules, the user's database, and `.env` were not edited. No commits or
subagents were created. This does **not** certify the unfinished dashboard or
expanded monitor form as complete.

**Result: the main functional proof passed, but endpoint switching has three
reproduced defects. AUTH-UI-01 crosses a password confidentiality boundary and
should be fixed before shipping.**

## Reproduce

Keep the existing Vite server on port 5173. From the repository root:

```powershell
node scripts/browser-auth-proof.mjs
node scripts/browser-auth-proof.mjs --regressions
```

The first command exercises normal flows. The second probes state retained across
node switches and exits 1 when it reproduces findings. Both build a unique Go
executable directly, without `scripts/go.mjs serve` or reading `.env`. Each run
creates two fresh databases and generated credentials under a unique
`.canary/browser-auth-proof/run-*/` directory, uses random loopback ports, and
launches its own hidden headless Chrome with a separate profile and CDP port.
The Chrome path defaults to the installed Windows Chrome; `CHROME_PATH` and
`CANARY_GO` may override executable paths.

Both node fixtures allow exactly `http://localhost:5173` and
`http://127.0.0.1:5173` through CORS. `BROWSER_AUTH_PROOF_ORIGIN` selects one of
those Vite origins. Browser HTTP requests outside the selected Vite origin and
the two fixture nodes are blocked. In particular, the initial default probe to
the user's port 3100 is blocked. No credentials are taken from that node. Nodes
have no peers, monitors, mail channels, or Discord integrations. The old shared
CDP helper on ports 3197/9333 was inspected but was not used or stopped.

Use this headless proof after the required T3 workflow. In this validation,
`preview_status` ran first, then `preview_open` created a new incognito
automation tab. `preview_snapshot` failed twice on that tab, both times with:

```text
Preview snapshot failed: Preview automation snapshot timed out after 15000ms.
```

Only then was the separate Chrome fallback used. The fallback locates visible
controls by labels or button names, scopes repeated fields to their current
card/dialog, and types/clicks through CDP input events. Native select changes use
a change event. It does not replace app API calls or import product session
objects to simulate UI success. Supplementary API requests inspect actual
credentials/revocation and establish explicitly identified fixture preconditions.

## Findings for the UI owner

### AUTH-UI-01 — high: old node's MFA enrollment sends its password to a new node

Source: `apps/web/src/views/Account.tsx:245`, `:252`, `:277`, `:292`;
the unchanged route remains mounted at `apps/web/src/App.tsx:277`.

1. Give nodes A and B different administrator passwords. Sign into A.
2. Open **Your account**, enter A's current password, and select **Set up
   authenticator**. Leave the pending setup open without confirming it.
3. Use the top-bar endpoint switch to sign into B using B's password. Stay on
   `#/account`.
4. The setup key from A remains displayed. Enter a valid code for that key and
   select **Turn on two-factor**.

Observed: Chrome's actual `POST B/api/auth/totp/confirm` request contained A's
password in `currentPassword`. B returned HTTP 401, **Current password is
incorrect**. The harness compared the request body in memory and saved only a
boolean proving the match. Neither node enabled MFA. No real credentials were
used or disclosed in the report.

The enrollment state stores `{...started, password}`. Its cleanup runs only on
unmount, while confirmation uses the newly supplied context's API. Endpoint
switching keeps this component mounted. Clear/remount sensitive account forms
on endpoint/account identity changes, and ensure an enrollment can only be
confirmed against its issuing identity. This is a functional/security fix and
does not require a visual redesign.

### AUTH-UI-02 — medium: A's issued token is relabeled as B's token

Source: `apps/web/src/views/Accounts.tsx:43`, `:56`;
`apps/web/src/App.tsx:277`.

1. On A's **Accounts** page create an account with a generated API token.
2. Leave the one-time token panel open; do not select **I've stored it**.
3. Switch to B without changing the route.

Observed: the panel still contained the exact token generated for A, but now
said **auth-proof-b stores only a hash**. B's account list contained only B's
administrator. The displayed token authenticated on A (200) and was rejected
on B (401). The account name, copy action, and token survive independently of
the list reload. Clear node-bound issued-token state on a switch.

Factual screenshot, with the fixture token hidden before capture:
`.canary/browser-auth-proof/run-QxCCRK/issued-token-wrong-node.png`.

### AUTH-UI-03 — medium: account draft passwords survive switching nodes

Source: `apps/web/src/views/Accounts.tsx:144`, `:147`, `:148`, `:175`, `:186`;
`apps/web/src/App.tsx:277`.

1. Fill **Add an account** on B, including a name and matching initial/confirm
   passwords. Do not submit.
2. Switch to A while remaining on `#/accounts`.

Observed: the name and both password inputs retained B's draft while the form
now said **Created on auth-proof-a only**. The final focused proof submitted the
retained draft: the account was created on A and was absent from B. It compared
the retained credentials directly in memory. Clear credential drafts on endpoint/account
identity changes so a form prepared for one node cannot silently become a form
for another node.

These findings share the continued mounting of node-bound view state across
endpoint changes. A fix should also audit password-change drafts, pending
security operations, and administrative dialogs for the same identity boundary.
Those related cases are recommendations, not additional claimed reproductions.

## Verified behavior

Main run: `.canary/browser-auth-proof/run-idiI5q/results.json`,
2026-10-22 13:08:24–13:11:22 UTC (09:08:24–09:11:22 New York), exit 0,
39 named checks.

- A passwordless node reports setup required. The implemented **Account token**
  choice performs bootstrap and routes to **Your account**. There is currently
  no **Advanced** control; this proof used the implemented equivalent.
- **Set password** rejects empty/short input and mismatched confirmation, then
  succeeds against the real Go API. The bootstrap API token remains valid.
- Normal password sign-in rejects a wrong password and clears the input;
  correct credentials establish a usable server session.
- Reload forgets the browser's session credential. The server session remains
  valid until explicitly revoked; the proof revoked its discarded test session.
- MFA setup rejects the wrong current password. Setup alone leaves MFA off.
  The displayed manual key matches the API seed; the URI has the expected
  `otpauth://totp` scheme and matching secret. **Copy key** and **Copy setup
  link** produced exact clipboard matches, then the test cleared the clipboard.
- MFA confirmation validates six digits, accepts an independently calculated
  RFC 6238 SHA-1 code, enables MFA, and revokes the current password session.
  Reusing its confirmation code at login fails with fresh-code guidance. After
  waiting for the next actual 30-second window, the password challenge and fresh
  code establish a working session.
- Password change with MFA revokes the previous session, rejects the old
  password, and leaves the independent API token working. MFA disable with a
  fresh code also revokes the session and permits subsequent password-only login.
- Normal **Sign out** makes the captured session unusable (401). API-token
  **Disconnect** forgets the token locally and leaves it usable on the node.
  When only the owned fixture node is stopped, sign-out explicitly says the
  server did not confirm revocation instead of claiming it ended the session.
- Admin UI creates password-only, token-only, and combined accounts. The proof
  actually authenticates each issued credential. Admin name/role edit persists;
  token rotation rejects the old token and accepts the new one.
- Admin password reset with **Also turn off two-factor authentication** removes
  MFA and permits the new password without a code. MFA on that target was enabled
  through actual API calls as a fixture precondition before using the reset UI.
- Deletion removes the account and invalidates both its token and password
  session. Last-admin demotion and deletion both return 409 and display the
  server's specific reason.
- A viewer can open its own security page. Direct navigation to **Accounts**
  explains the restriction; a viewer-authenticated account-list API call is 403.
- A failed endpoint switch preserves the old visible endpoint and usable
  session. A successful password switch signs into the new node independently
  and revokes the replaced node's password session. The three retained-state
  findings above limit this otherwise successful switch behavior.
- At sampled password-session, pending-enrollment, issued-token, and switch
  states, localStorage contained only the endpoint/theme preferences;
  sessionStorage, cookies, IndexedDB database names, and Cache Storage were empty.
  Actual request URLs contained no fixture secrets or credential parameters.
  The pending seed did not enter browser storage. No browser runtime exceptions
  or allowed external application requests were observed.
- All 42 samples passed the measured overflow inspection: sign-in, Your account,
  MFA enrollment, Accounts, Manage account, viewer security, and signed-out pages,
  each at 390/1000/1440 CSS pixels in light and dark modes. No horizontal document
  or open-dialog overflow and no sampled visible form controls outside the
  viewport were found. This is an overflow check, not visual design approval.
- Web TypeScript checking and the Vite production build passed. Build output
  went under the run directory, not product `dist`.

Focused run: `.canary/browser-auth-proof/run-QxCCRK/results.json`, exit 1 because
all three findings reproduced. Typecheck and isolated web build also passed.

Final focused run: `.canary/browser-auth-proof/run-RBl5NN/results.json`,
13:17:12–13:17:41 UTC (09:17:12–09:17:41 New York), from the alternate Vite
origin `http://127.0.0.1:5173`. All three findings reproduced, including the
actual wrong-node account creation. Seven additional named checks passed:
isolated latest-source fixtures, completed regression probes, viewer password
change revoking current and parallel sessions, viewer **Sign out everywhere**
revoking both sessions, token-authorized **Sign out everywhere** revoking a
password session while retaining token access, typecheck, and production build.
There were no browser runtime exceptions. Exit 1 records the three known
findings; there was no harness exception. This run's complete source maps were
unchanged from start through cleanup.

## Provenance, limits, and cleanup

Node v24.22.1; Go go1.27.3 windows/amd64. Main executable SHA-256:

```text
31807c1e991dbed5400b4485332cfc83c17856df5b48d274514757b1e6b6f861
```

Final focused run executable SHA-256, after the parent's maintenance source
became buildable again:

```text
d87720074ffee6ee5f7d08c73f4cd0e779d0cfa1c2faa30f2c4f768d0a4515b2
```

The JSON artifacts contain per-file SHA-256 maps before build, after build, and
after testing, plus each executable hash. The checkout has no `.git` directory,
so no commit hash is claimed. The parent changed
`apps/node-go/internal/canary/notification_review_round3_test.go` during the
successful runs; application/UI/auth sources were unchanged in those runs.
Selected UI SHA-256 values for that tested snapshot:

```text
App.tsx            77a6d21c5a885d4425dabaecff70eb1ac17efdff8831d094e76d62f802b83d91
views/Account.tsx  a2ccc0c45ba6c92db66dfba2b41630894b1e29d7d29268a602426644e534c0f4
views/Accounts.tsx 30f4f0387cc019404a2ac00d67cfc5da4fa21b5978e0a0a7c75e4484c1673474
views/Connect.tsx  8d65f06c1c908d3cff96b17863f03fe36c34f9812ef20caaf37e31977b9c38da
api.ts            7c9a374e39c3299583d3aff53f41121fd8b666c6ad06ed70c15ddd4667c3db6a
session.ts        c3ce296bb658f83d4ed88ff98036fa9fd3fb3129f07a0caf87cd418418a3061d
```

Independent QR decoding was unavailable: Chrome lacked `BarcodeDetector`, and
no installed Python QR decoder was found. QR scanability is **unverified**. No
external QR service or duplicate QR encoder/test mirror was introduced. The
manual key/URI, clipboard, and independently generated code path are verified.
No external authenticator app, mail delivery, Discord service, or user account
was used. Absolute 12-hour expiry, ten-minute enrollment expiry, login-limit
exhaustion, and recovery after a persistence fault were not time-advanced or
certified by this browser proof. Expanded HTTP/certificate/pending monitor UI
remains outside this task and was not validated as complete.

All completed proof runs stopped only their owned Chrome/node processes and
removed their databases, executables, and Chrome profiles. Their result JSON,
web build output, factual screenshots, and empty `.db.lock` files remain under
their private run directories (the final run also removes lock files). The
owned T3 preview tab was closed through `t3_preview_close`. A final process
audit found no proof node or proof Chrome instance running. An early harness deadlock run
`run-ZgaV8S` also retained its disposable database/executable/profile artifacts;
automatic approval review rejected both guarded cleanup attempts with
`blocked by policy`. No unrelated service or browser was stopped.

Follow-up builds during the parent's maintenance implementation were transiently
blocked: `run-W6dR5H` lacked Node maintenance fields; `run-qHvQXs` lacked
`maintenanceNeedsPreflight`, `prepareMaintenanceRequest`, and `maintenanceAPI`.
These runs failed before creating node/browser fixtures. They do not replace
the tested-source proof above or establish a new auth defect. The final focused
run rebuilt successfully and completed the additional viewer/password/logout-all
and alternate-origin verification. An intermediate focused rerun
`run-EuIiyV` stopped when an 8-second toast obstructed a coordinate click; the
driver now checks the hit target and waits for obstruction to clear before
dispatching the real input event. Its fixtures were also cleaned up.

## Appendix: connection-boundary functional/security fix — 2026-10-22

The three findings above are preserved as the original reproduction record.
The implementation now clears their connection-bound state on every successful
connect, including a fresh sign-in to the same URL and account. `--regressions`
now asserts the fixed behavior and is expected to exit 0. A retained enrollment,
issued token, or credential draft still fails the proof; the original checks of
the wrong-node request body, token validity, and account destination remain.

Product edits are confined to `apps/web/src/session.ts`, `App.tsx`,
`views/Account.tsx`, and `views/Accounts.tsx`. No CSS, layout, UI wording, assets,
README, monitor form, hooks, shared contracts, or Go source were edited for this
fix. Supporting edits are confined to `scripts/browser-auth-proof.mjs`,
`scripts/browser-auth-proof-regressions.mjs`, the new
`scripts/browser-auth-proof-delayed.mjs`, and this appendix. No commits or
subagents were created.

- Each successful `Session.connect` allocates an incrementing, nonsecret identity
  kept only in memory. `App` keys the whole Shell and view subtree by that
  identity. Failed connects do not change it; polls and account updates retain it.
- Sensitive async handlers check both the originating connection identity and
  API instance after awaiting. Session `end`, `setAccount`, and `adoptToken`
  additionally require and check the originating connection. Old password, MFA,
  logout-all, create, update, token, reset, and delete completions cannot change
  a replacement session or publish their results there.
- Session sign-out returns a superseded outcome when its originating connection
  or API has changed. Its completion then neither disconnects the replacement
  nor clears/replaces that connection's notices.
- Own-token adoption keeps the connection identity and one-time token panel,
  replaces the API, and invalidates polls made with the old token. Tokens are
  absent from React keys and persistent storage.

Required T3 workflow was attempted again: `preview_status`, then a new incognito
`preview_open`, followed by two `preview_snapshot` attempts on that tab. Both
snapshots failed with the exact error already recorded above:
`Preview snapshot failed: Preview automation snapshot timed out after 15000ms.`
The owned preview tab was closed. Testing then used the documented isolated
Chrome/CDP fallback. Its request allowlist blocks port 3100 and external origins;
fixture credentials, passwords, seeds, response bodies, and headers stay out of
tool output and screenshots. Each run builds its own Go executable and fresh
databases at random loopback ports without `.env`, peers, monitors, or live
notification destinations.

The normal browser proof passed all **39** named checks, exit 0:
`.canary/browser-auth-proof/run-Nfmd0Q/results.json`,
13:30:02–13:32:31 UTC. It includes the original real password/MFA, account
administration, authorization, session-revocation, failed/successful switch,
memory-only storage, unreachable sign-out, and measured overflow checks.
There were zero browser runtime exceptions or allowed external application
requests. Web typecheck and the production build into that private run
directory passed. Root `npm run typecheck` also passed for all workspaces.

The four edited product files were unchanged throughout that proof, with these
SHA-256 values:

```text
session.ts        bd8467df3bca082e1ff3f1561d803b2c9ab45834d5c4f6a8bf4205a1297c5762
App.tsx            7521667686bbf6a0312b3bbefdfcc4dac337469c67282eeec4d196059c3a889e
views/Account.tsx  e6160ab05c92b2d31598ade5d70037468f55b1a7a0ec014ded1d4264f94be864
views/Accounts.tsx 1710f1ebd51809fc5b908c0e6611cfd5ad1e63067628624b2d6f95519cdc7c9c
```

The normal proof's executable SHA-256 is
`e352d2cfcfa45d4dc58f4a10b59942738fea09ff3f2688d859ce0acdfcd7847c`.
Parent maintenance work changed Go/core documentation and reconciliation/delivery
sources during the run; those changes are listed in its JSON. The UI hashes
above stayed stable, and the artifact identifies the exact executable exercised.

Final fixed-behavior regression proof passed all **23** named checks, exit 0:
`.canary/browser-auth-proof/run-qYdTfl/results.json`,
13:35:20–13:37:37 UTC. There were zero findings, harness failures, or browser
runtime exceptions. Its production source hashes stayed unchanged throughout.
It passed web typecheck and the isolated production build as well. Executable
SHA-256:
`89f50c55038e3d47e5c41dd264e7a8eb2e737361ac604f51cb10a624cea14de6`.

The fixed-behavior checks prove that pending MFA enrollment, issued tokens, and
account drafts clear on successful switching; MFA enrollment survives a poll
and a failed switch; account drafts survive a poll; and fresh credentials for
the same URL/account still clear drafts. An attempted submit after clearing
does not create the old draft on either node. The viewer password-change,
parallel-session revocation, and token-authorized logout-all assertions remain.
Own-token rotation invalidates the old token, authenticates with its replacement,
and keeps the same mounted Shell and one-time token panel through the next poll.

Nine delayed-completion cases use CDP only to hold and release **actual Go API
responses**, without fabricating bodies or mutating/importing the product session:

- Token-authorized password setup completes on A without overwriting B's account
  or publishing A's success on B.
- Password-session password change and MFA confirmation cannot disconnect B.
- Delayed MFA setup cannot display A's seed/QR on B.
- Logout-all and sign-out cannot end B's connection; delayed sign-out also leaves
  B's new sign-in notice intact.
- Account creation completes on A without publishing its token/success on B or
  creating the account there.
- A self-admin edit cannot end B or publish the old account result there.
- A delayed self-token rotation cannot adopt A's new token into B's token
  connection. The next real GET overview still sends B's own credential.

The harness retains the real login limiter and waits one real 60-second window
before these additional cases. It counts authenticated GET polls separately
from CORS OPTIONS preflights. Memory-only storage, URL hygiene, external-request
blocking, and zero-runtime-exception assertions pass at the end of the suite.
Real delayed responses cover the cases above; MFA disable, admin password reset,
and deletion completion guards were audited in source and their ordinary UI
flows passed the normal proof, but their delayed variants are not separately
claimed.

Intermediate fix-validation runs are retained as failed harness attempts:
`run-NPumnf` used an incorrect dialog-close locator; `run-SnFtEt` had an escaped
selector expression error; `run-2B1mSF` reached the fixture's real HTTP 429 login
limit; and `run-Au4MBJ` inspected a preflight instead of the authenticated GET in
the final credential assertion. These were corrected without weakening the
security assertions. All of these runs and both completed proofs stopped their
owned Chrome/node processes and removed only their own databases, executables,
and profiles. Result JSON, isolated builds, and the normal proof's nonsecret
screenshots remain. The earlier `run-ZgaV8S` artifacts remain untouched:
automatic approval review previously rejected their cleanup with `blocked by
policy`; no retry or alternate cleanup path was used for that directory.
