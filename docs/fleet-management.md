# Manage monitors across registered nodes

Use matching feature-capable node and dashboard builds. The verified PR #18
published baseline predates fleet management. Check the source revision of
the image tag you deploy; see [software updates](software-updates.md).
An older node refuses
placement preflight; the dashboard does not fall back to an unsafe create.

## View every node

In the dashboard server, **Overview** and **Monitors** show the complete
registered fleet. You do not have to switch nodes or sign in again to see their
results. A fresh dashboard opens **Nodes** so you can add its first node.

A managed monitor has one row, with a result for each assigned node. Opening
one of its node views reads that actual node and monitor ID. Existing HA copies
are grouped by known peer lineage. Equal names or equal IDs on unrelated nodes
do not merge unrelated monitors.

The page distinguishes a current node snapshot, an old snapshot and a failed
read. An unreachable or stale node cannot supply a current healthy result.
Pending configuration and removal are shown as unconfirmed. The time a node
was read and the time of its measurement mean different things.

The static development dashboard still works directly with a selected node.
Fleet controls require the dashboard server and its registered-node list.

## Choose where a monitor runs

1. Create a node group and select its registered nodes. **Create and sync**
   collects existing monitors from every member and copies missing monitors
   across the group. It works in both directions.
2. Create a monitor and choose its **Node groups**. A sole group is selected
   automatically; with several groups, choose which ones should run it.
3. Choose its notification channels and save. Channels and tags are copied
   automatically from the form's source node, including their later edits.
4. Check each node's confirmation in **All nodes**. A saved plan is not
   confirmation that every node has applied it.

Changing group membership updates the monitors that follow it. Added nodes
receive them; removing a member retires the group's copies on that node.
**Sync monitors** on a group card or in its editor collects any additional
monitors created directly on member nodes. Saving an edited group starts a
sync as well. An offline member leaves collection pending until it answers.

The original monitor keeps its ID and history when first adopted. Independent
destinations receive separate physical copies and keep their own histories.
Equal names or equal IDs on unrelated nodes do not merge unrelated monitors.
Repeated sync uses the recorded ownership and does not create duplicate copies.
Existing assignments outside a newly adopted group are kept as explicit pins;
an ordinary monitor edit preserves them. Use individual-node mode to change them.
Removing a group preserves its deployed monitors and their assignments.

Configured HA peers share one configuration and retain their existing alert
coordination. Independent nodes check and alert separately. Registering nodes,
placing a monitor or adding a node group never peers them automatically.
There is no cross-cluster exactly-once alert guarantee.

Before replacing an existing assignment, register every former runner that
would be removed. The dashboard must confirm that each has stopped running
the old configuration. It does not silently forget an unregistered runner.

## Advanced settings and notification sources

**Advanced settings** contains individual-node placement, automatic channel/tag
copying and per-node reference overrides. Existing monitors without group links
keep their node assignments until a group is explicitly chosen. A saved copying
off setting remains off during group sync. Older definitions with no saved
copying preference adopt the automatic default when they join a group, including
an old missing-channel preflight that never sent a command. Per-node overrides retain their own
exact resource IDs and are not replaced by later edits to the form's selection.

Channel credentials travel only through authenticated server-to-node requests
and the encrypted deployment journal. Destinations receive managed identities
rather than matching channels or tags by name. The independent source retains
its native identities. Channel and tag updates normally follow within about
30 seconds while sources and destinations answer; unresolved writes replay the
original saved command before a new revision is prepared. Direct edits to a
managed destination channel stop replacement and require review.

The monitor form reads its saved source's channels and tags without switching
the page or discarding its draft. For new monitors it prefers a member of the
chosen group. The dashboard has no global Viewing-node button: Overview and
Monitors remain fleet-wide. Pages that operate on one node, such as notification
channels and outage dependencies, show that node with **Change node**.

**Outage dependency groups** remain a special node-local setting. They control
alert suppression and are separate from dashboard node groups. Their monitor
dependencies are not automatically copied or mapped. If a destination lacks the
selected dependency group, choose its group or None under Advanced settings;
preflight reports the mismatch instead of silently removing the dependency.
HA members sharing one monitor must use matching references.

## Pending changes and recovery

The dashboard validates all target plans before sending changes. Once prepared,
it saves the exact commands before sending them. A lost response is retried
with the same placement identity and sequence, so it cannot create a second
monitor or silently overwrite a newer revision.

After an anchor acknowledges a change, HA members must confirm it in their own
authenticated snapshots. A member being removed stays listed as pending until
it confirms the changed assignment or deletion. Its registration cannot be
removed while it still has an active or retiring fleet placement.

- **Retry placement** resumes the saved command. It does not replace a direct
  edit made on a node.
- **Review node edits** shows the fresh revisions the dashboard read. Confirming
  replacement applies the dashboard's saved settings only if those reviewed
  revisions remain current. Resolve configuration conflicts on the node first.
  A confirmed revision refusal can be reviewed even after the command was
  prepared. The dashboard checks the previous node receipt before replacing it;
  commands not yet sent remain part of the plan. A lost response still requires
  exact replay through Retry placement before it can be superseded.
- **Load latest** in the monitor form discards its draft and loads the latest
  logical settings, destinations and reference choices together.
- **Cancel placement** is available before any configuration was sent for a new
  plan. Cancelling an update cannot forget already deployed monitors.
- **Remove everywhere** retires only this logical monitor's owned copies. An
  offline member keeps removal pending. Unrelated monitors remain intact.

An unknown write must be resolved before another edit or removal can supersede
it. If the authorizing dashboard account changes, work stops until an editor
reviews and retries with current permission. Nodes keep monitoring their last
configuration while the dashboard is stopped.

## Persistence, limits and validation

The dashboard stores encrypted logical configurations and deployment journals
with its retained identity key. Group membership and queued worker authority are
authenticated with the same key and bound to their storage identities. Legacy
inert selections can migrate; active membership cannot bypass verification by
removing its proof. Keep data and identity volumes together.
Node receipts stay local and do not enter peer snapshots. Central viewer
responses use the node's existing monitor redaction and omit destination
reference overrides. Node credentials stay on the dashboard server.

Limits are 200 retained logical definitions, 200 dashboard node groups and 64
registered destinations per definition or group. Each node's own monitor and
resource limits still apply. Network loss after preflight can leave some
placements applied and others pending; this is not a transaction across nodes.

The group-first candidate was checked on Windows amd64 with CGO disabled.
A complete Go checkpoint passed 4,527 test events with four skips and all-package
vet. Later affected-package checks cover the group-record identity guards,
channel-source reuse, rollback and legacy migration changes. All three workspace
typechecks and all 27 configured frontend fixture scripts pass. The complete
production browser harness passed 221/221 with no exceptions or blocked requests
at v3 source fingerprint
`ea8855a887dc6258a73d51d5215180e9d789deaa15d73ebe1d5a38b2d581b0cf`.
After the legacy preference migration, the affected Go checks, core vet,
typechecks, 27 frontend scripts and nine focused browser checks passed at
`bed711a6a95c97b0f1c2701f88b601de7083eda001c4a74e27a6c9028aed49c3`.
The group-first review fixes preserve original runners outside an adopted
group, retain copied-channel origins after other monitors are removed or edited,
wait for actual membership deployment before reporting a group in sync, and
keep a clone's reference source. Their regressions reproduced the original
failures. The simulated preflight refusal supplies the required node identity;
the clone fixture waits for its source overview before editing.

After those fixes, affected Go tests, core vet, all three workspace typechecks,
all 27 frontend scripts and the complete production browser harness passed:
**222/222**, zero page exceptions and zero blocked requests, on unchanged v3
source fingerprint
`d4174f5634fdcfdb9329cf04e3a9122a7ea61d7acba44263de67c74cb1b5d889`
(491.516 seconds guarded). Scratch returned to zero; the shared module cache
did not grow. These checks do not certify a release or replace hosted
Linux/image gates.

The follow-up conversion fix also retains outside runners in the form's target
profiles and save payload, preserving explicit per-node channels, tags and
outage dependency choices. Its browser regression failed before the fix; the
fixture first needed the API's omitted empty-list/false-field representation.
A direct-mode rendering guard was retained after the existing fixture caught it.
All three typechecks, 27 frontend scripts and 11 focused production browser
checks then passed at unchanged v3 fingerprint
`3fad34cc0234b76e247f344c9f9a95c11edb60959d8ba43494055c44f103a3b4`
(131.281 seconds guarded). The 222-check full browser checkpoint above precedes
this frontend-only fix and the native-adoption repair below.

Native adoption defers outside-reference membership checks until the source's
original assignments have been read and verified, before any node writes.
Regressions cover accepted original-runner overrides, unrelated-node refusal
without monitor changes, and idempotent create replay after verified pins.
The original valid-override request failed with HTTP 400 before the fix.
Affected Go tests, core vet and 11 focused browser checks passed at unchanged
v3 fingerprint
`ac3b501f7751647d7df4cc03975138d11a6eb8ce375ad4895b9290d20eb32720`
(314.454 seconds guarded). Scratch returned to zero; module cache unchanged.

The first complete Go attempt exceeded 512 MiB of disposable scratch; the guard
stopped it and reclaimed its scratch to zero. The same tests passed with debugger
metadata stripped, using the existing browser-build flags and the same cache
and storage limits. Original fixture failures remain explained: clone rendering
needed its optional connection guard, the channel-choice source assertion now
also recognizes remembering the reference source, and the removed global-node
picker assertion now reads the selected Nodes card's h3. Group creation's earlier
adoption adds one placement revision to the existing browser scenario. Its
history, explicit-choice, recovery and removal assertions are retained.

CodeRabbit follow-up regressions reproduced empty-group create replay failing
after reload, an unused removed source blocking edits, a removed configured
source retrying forever, and applied monitors not resuming group maintenance
after their saved author was revoked. The fixes preserve nil/empty group
semantics, allow a resource-free edit to select a registered source, distinguish
permanent source removal from temporary rotation, and let an explicit authorized
group sync rebind only idle applied rows with a new revision. Resource-bearing
monitors still require explicit source replacement; node-local IDs are not
reinterpreted automatically. Per-pass caching remains a performance follow-up.
Affected Go tests, core vet and 11 focused browser checks passed on unchanged
v3 fingerprint
`1142676a4b1143c7f74b084b9ee32dd40c1696ff25e26909e8c22f938199c10f`
(356.953 seconds guarded); scratch returned to zero and module cache stayed fixed.

The fifth source-review round resolved the source and replay fixes, but found
that replacing every member with empty nodes bypassed collection-time actor
renewal. The guarded renewal now runs over linked definitions instead. Both
addition and complete replacement regressions pass, including former-runner
retirement and the group's final applied state. The replacement case failed
before this move. Affected Go tests, core vet and all 11 focused browser checks
passed on unchanged v3 fingerprint
`7ccf71cde820c01cb95cd0b8444f3c79db476a663f9dcc5386eb9cda672be2a6`
(301.860 seconds guarded), with scratch back at zero and unchanged module cache.
The final move was approved in the sixth Sol review, explicitly authorized by
the user. All nine correctness findings are resolved; the caching nit is deferred.

The initial group-first implementation was approved after four Sol rounds with
gpt-6.1-sol on xhigh. All six correctness findings were resolved.

The earlier fleet source review approved its implementation after three Sol rounds;
the separate project-wide review approved the probe and preparation repairs
after three rounds.
Neither source review replaces the platform, image or deployment gates below.

The separate project-wide Sol review found two probe deadline bugs: GameDig's
socket deadline could discard a successful Minecraft reply, and the scheduler's
Steam deadline bypassed the optional API-failure-as-Down policy. Both new
regressions failed before their fixes. Socket waits now have margin inside the
GameDig attempt, and Steam distinguishes its scheduled measurement deadline
from earlier caller deadlines and cancellation.

After those repairs, Windows amd64 passed **4,518** Go test events across 28
tested packages, with four platform skips and no failures, all-package vet,
all three workspace typechecks and every configured pure/control fixture
(374.765 seconds guarded), at unchanged v3 fingerprint
`d27fe6458a9b82748b82c4664db17189145f54c62590b1281d13d09b1387954a`.
Only GameDig test timing diagnostics were added afterward. Five fresh Node
processes then passed **25/25** service test cases (28.062 seconds guarded),
including the original three-second control and a new 1.5-second case, at
`7e1890f78ead906c4b6bd76ac1faf299cd1e5f4d61f9a17856a537cca05bc305`.
The three-second queries completed in 1,918–1,932 ms; first TCP traffic arrived
in 245–250 ms. No deadline or original assertion was relaxed. The earlier
Windows Pending result at 3,059 ms remains recorded; its exhausted phase was
not established, and passing reruns do not prove its original cause. These
checks do not replace the separate platform, image and deployment gates below.

A subsequent PR review tightened preflight publication: failed or cancelled
preparation now keeps the previous committed placement layout and command plan.
The two new regressions failed before the fix. Fleet, node-group, receipt and
outbound-connection tests then passed (57.016 seconds guarded, package-cwd
fingerprint
`7f575469af8fb2546e23ce8383d48c49f4891c486bc7aa74073363692f2c942d`).
This follow-up changes no frontend or GameDig timeout policy.

The earlier PR head `5ce67459` failed Alpine race validation with two warnings
in the lost-response fixture. It read node internals without the mutex held by
the HTTP handler; a deliberately lost response provides no synchronization.
The fixture now locks those reads and its direct edit, preserving the original
assertions. The same targeted Windows tests passed again (48.797 seconds
guarded, package-cwd fingerprint
`98c32aaecfac32123eac47ceb3053efe670e10ea62b466898c89f2e60c26e744`).
This is a test-only repair; hosted race validation must pass on the updated head.

After the first fleet Sol review, the recovery and destination-selection repairs
passed all three workspace typechecks and every configured pure/control fixture,
including 21 fleet management groups (22.922 seconds guarded), and the complete
browser suite again: **220/220**, zero exceptions and blocked requests (298.843
seconds). Both used unchanged Windows amd64 v3 fingerprint
`0836d3f7201f5d9f771a7647141bc92a3960da068f087e210da74f96502d68e1`.
The production manifest hash was
`270d7aef0a22e0778955bd6ae61790e7a8f6d66bc9c318acf2e230ed3401c53e`;
the executable hash was
`dade9bba2ca19b9280425df3053e5a3d37c6920276ff7f68e6269dd5beb46f0d`.

A subsequent Go-only change copies committed placement arrays before publishing
them to dashboard readers. Fleet, node-group and placement Go tests passed again
(38.296 seconds guarded, package-cwd fingerprint
`b783ad7b88d8f7e9e4ab67dd77b6c466dce28a6790205ff3de90fbec50f2c1f4`).
Regressions cover a refused prepared update with existing and new unsent
destinations, lost create/update acknowledgements, removed-node editor selection,
and a failed confirmation save followed by restart from its committed journal.
The first failure-injection fixture incorrectly retried a faulted runtime without
restarting; it now follows the persistence-error restart contract. Its original
failure is retained. Scratch returned to zero and the module cache was unchanged.

The second feature-review repair clears a previous refusal before every new
attempt, so a later lost acknowledgement still requires exact replay. The same
Go test selection passed again (35.203 seconds guarded, package-cwd fingerprint
`6c13b634c958f97d8f9987660d471778ab45d81ab2d3c30dba67b620dd20b4ba`).
Its regression injects a recoverable refusal through the owned loopback proxy,
then commits a real node update while losing its response. The fixture initially
omitted the required node-identity header; the header was corrected and all
assertions retained. This did not change the earlier browser/static source scope.

Before those review repairs, a Windows amd64 checkpoint passed these local checks on unchanged v3
source fingerprint
`99e4de603f4acee625223121e89eeec613c92055dd3e03b2394079725fe36450`:

- All three workspace typechecks and every configured Dockerfile pure/control
  fixture, including 10 fleet inventory groups and 19 fleet management groups
  (23.140 seconds guarded).
- The complete production-build/browser harness: **220/220**, zero page
  exceptions and zero blocked requests (317.484 seconds guarded). It preserves
  the existing 213 checks and adds seven fleet scenarios: independent inventory,
  saved node groups, adoption with destination-local tags, draft preservation,
  explicit node-edit review, owned-copy removal, and placement/retirement through
  a real node-initiated connection with no inbound URL. The group editor,
  reference controls and summary fit 390, 728 and 1280 pixels in both themes.
- All-package Go vet (63.406 seconds guarded).

The browser's production source manifest is
`a0fa56e182b72154e6936ba0313b07ce775bb7bd80bedfcb3aa39307f3a45f61`;
its executable SHA-256 is
`8654e994b0c06ba92451f89c0996a1392c738a5b0628ea724cac06c8e13e5166`.
These identify that local build, rather than a published container.

An earlier broad Go checkpoint passed **4,511 test events**, with four platform
skips, no failures and 28 tested packages, plus all-package vet (416.406 seconds
guarded), on fingerprint
`33d626b34ada445681c9d3251a1512e7e97f8cf64d9526e10be0cd0898a4da2d`.
That precedes the frontend action-clock fix and outbound route admission.
After the latter, the fleet, invitation and connection Go tests passed again
(119.391 seconds guarded, package-cwd fingerprint
`a0fe67cc1e1ce05951fb9de262a59e5fae7593c292bdbfc27a6819a4230a7e41`),
including a live TLS connection applying and retiring a placement and refusing
viewer receipt access. The final browser and vet checks cover the follow-up.

Earlier failures remain on record: the first full run exposed the missing
first-node entry action; later runs found test selector/precondition errors and
a real review-action clock bug. A newly read snapshot was compared with an older
UI timer tick. Actions now sample their own clock, with a regression for fresh,
expired, future and conflicted snapshots. Original assertions were retained.

The storage guard stopped the initial unstripped Go compile at its scratch
limit and cleaned it. Compiler debug metadata was then omitted with the same
assertions, owned cache and 512 MiB limit. All completed runs returned scratch
to zero and left the module cache unchanged. No real notifications were sent.

Hosted Linux/ARM/Alpine/race, extracted-package process proofs, image gates,
review and a real Portainer/VM/VPN deployment remain separate acceptance work.
No release or publication of fleet-capable images is claimed here.
