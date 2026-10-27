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

Local validation is recorded separately from hosted/platform acceptance. The
candidate has focused Windows Go and static checks; the first browser run
exposed a first-node navigation regression, and the next passed all 213 existing
checks but found selector errors in the six new fleet steps. Those original
failures remain recorded. A corrected full run, complete Go/vet checks, hosted
Linux/ARM/Alpine/race gates and real deployment remain to be confirmed.
Nothing here claims a release or publication of fleet-capable images.
