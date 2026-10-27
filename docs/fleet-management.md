# Manage monitors across registered nodes

Status: fleet management is a development candidate on
`feature/fleet-monitor-management`. It is not in the published PR #18 images.
Use matching node and dashboard builds when testing it. An older node refuses
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

1. Create a monitor, or open an existing monitor and choose **Edit**.
2. Under **Run on**, select any registered nodes. These may be independent
   nodes with no HA peering between them.
3. Review the channels, dependency group and tags for each destination.
4. Save. The dashboard keeps a durable plan and reports its progress in the
   central view. A saved plan is not confirmation that every node has applied it.

The original monitor keeps its ID and history when first adopted. An independent
destination receives a separate physical copy and keeps its own history. The
dashboard records which copies belong to the same logical monitor.

Configured HA peers share one configuration and retain their existing alert
coordination. Independent nodes check and alert separately. Registering nodes,
placing a monitor or adding a node group never peers them automatically.
There is no cross-cluster exactly-once alert guarantee.

Before replacing an existing assignment, register every former runner that
would be removed. The dashboard must be able to confirm that each has stopped
running the old configuration. It does not silently forget an unregistered
former runner.

## Node groups and dependency groups

**Node groups** are saved selections of dashboard registrations. Their editor
lists the whole registry, including independent nodes. A group button under
**Run on** adds its members to the selection; saving the monitor applies it.
Changing a saved group's membership does not automatically move existing
monitors. Removing a group removes only that saved selection.

**Outage dependencies** opens the selected monitoring node's existing Groups.
Those groups control hierarchy and alert suppression on that node and its HA
peers. They are separate from dashboard node groups.

## Channels, dependency groups and tags

Resource IDs belong to their monitoring nodes. A channel named “Operations” on
one independent node is not automatically the channel with that name on another.
Fleet management neither copies resource settings nor guesses mappings by name.

By default, destinations use the monitor form's settings. Enable **Choose
references on this node** to select that destination's own channels, dependency
group and tags, including tag values and public flags. Enabling it takes a copy
of the form's selection; later form changes do not replace that explicit choice.
Unknown IDs remain visible until you explicitly remove them.

Members of one HA placement must select matching references, because they
share one monitor configuration. Independent placements can use different IDs.
Missing or conflicted references block preflight instead of being dropped.

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
with its retained identity key. Keep its data and identity volumes together.
Node receipts stay local and do not enter peer snapshots. Central viewer
responses use the node's existing monitor redaction and omit destination
reference overrides. Node credentials stay on the dashboard server.

Limits are 200 retained logical definitions, 200 dashboard node groups and 64
registered destinations per definition or group. Each node's own monitor and
resource limits still apply. Network loss after preflight can leave some
placements applied and others pending; this is not a transaction across nodes.

After the first Sol review, the recovery and destination-selection repairs
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
