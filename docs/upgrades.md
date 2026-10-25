# Coordinated upgrades

Upgrade a cluster as one compatible build. Protocol 19 adds monitor and
configuration contracts that protocol 18 cannot safely exchange. Mixed peers
reject snapshots; they do not silently discard unfamiliar settings. During
that interval each node continues its own checks and delivery policy. Partition
duplicates remain possible, and all-observer recovery can wait for compatible,
fresh observations. A rolling deployment is not a promise of uninterrupted
cluster convergence.

## Before deployment

1. Read the change's accepted platform evidence and operating limits. Use the
   same source version for every node and its dashboard. For containers, pin
   image digests when identical bytes matter; a `sha-<commit>` tag can be rebuilt
   by the publisher and `latest` can move.
2. Record each node ID, database and retained identity directory, peer addresses,
   and the dashboard's two persistent volumes. Keep this inventory private;
   it includes the location of credentials and recovery material.
3. Take a consistent node database backup using
   `canary backup-database <database-file> <new-backup-file>`. Retain the latest
   identity directory separately. Follow [backups](backups.md), including its
   earlier-container identity-volume migration where applicable. Copying only
   a live SQLite main file can omit committed WAL contents.
4. Back up the dashboard's persistent data and identity together while its
   server is stopped, using the operator's volume-backup procedure. Preserve
   the identity key that decrypts saved node role credentials. Node identity
   reservations and dashboard encryption identity are different state.
5. Keep the previous executable or image digest available. Do not assume an
   older executable can reopen a database after a newer schema or configuration
   has been written. Test any rollback plan against private copied state first.

## Deploy and check

Stop the old processes before replacing their executable or persistent volume
mounts. Keep the current database, node identity directory and node ID; an
ordinary same-state upgrade is not a restore and needs no activation or
re-enrollment. Never start old and new processes against the same identity.
Apply the chosen source/digest to every peer, then start the nodes and dashboard.

Check that every expected node answers, peer exchanges converge, retained
configuration has no unexpected conflict, and new observations come from each
assigned observer. An old last-known value is not proof that a restarted node
is checking. Verify notification routes with deliberate test actions, and
confirm any real outage retains its delivery history and recovery obligation.
Do not clear incidents, delete receipts or change freshness deadlines to make
an upgrade look healthy.

Dashboard accounts and registered-node IDs remain stable. Existing public
`registration` links and Push examples keep their selector. When a registered
node moves, an administrator can edit its address after the server verifies
the same identity and all saved role credentials. A bundled node's address is
managed through deployment settings.

Protocol-19 delegated-credential rotation is separate from upgrade. It updates
the three existing dashboard role accounts atomically, revokes their previous
tokens/sessions, and preserves registration ID, node identity, monitors and
cluster secret. A `rotating` row needs confirmation or Retry; a lost response
does not authorize creating a second enrollment. An older node must advertise
the rotation capability before this operation is available.
Known invalid credentials or missing role accounts are refused before starting
an update. If a credential is revoked while the update is in flight, supply
a current administrator credential through its recovery action; the server
keeps the original desired bundle and registration. A pending result is not
confirmation that the old credentials still work or that nothing changed.

## Recovery

An unsuccessful process start is not permission to overwrite state with a
backup. Preserve diagnostics and stop the writer. If restoring a node database
is necessary, use `prepare-restore`, then `activate-restore` with the **latest**
retained identity directory and the original process stopped. Restore activation
revokes password sessions and creates fresh check generations; it is not an
ordinary restart or a way to carry old notification continuity into a new
measurement. See [the complete restore procedure](backups.md).

Do not roll the identity reservation back alongside a database snapshot. If
that current state is lost, a new identity and explicit cluster re-enrollment
are required. A dashboard backup without its encryption identity cannot recover
the saved private role credentials merely by changing the database address.

## Acceptance fixture

`scripts/coordinated-upgrade-validation.py` uses the published protocol-18
Linux amd64 node image from source
`103f5c45f64d788f02df0a6393ac2b3fdbd42f0b`, pinned by image digest. It checks the
image's source/platform labels, copies only `/usr/local/bin/canary` from a
container that is never started, and records the executable hash and reported
protocol. It creates no historical source snapshot, rebuilt legacy overlay or
retained binary archive. The ordinary Docker daemon cache is used for the
public baseline pull; the unique extraction container and its anonymous volumes
are removed, and all private fixture state belongs to the storage guard.

The proof uses two loopback nodes and private SQLite/identity state to require
old down acceptance, mixed-protocol rejection in both directions, fresh
observations and increasing sequence reservations, account/role persistence,
new-profile convergence after the second upgrade, receipt retention through
another restart, and the owed recovery. The current executable is the same
one extracted from the candidate release package. The baseline image is amd64
only, so this is not an arm64 or Windows upgrade claim. Separate native arm64,
Windows, Alpine, restore and ordinary process gates remain required.

A second case upgrades the published dashboard against retained private
accounts, encryption identity and its automatically enrolled node. It requires
the original owner password and unexpired session to work, retains the
registration ID and encrypted delegation, refuses unsupported rotation on the
old node without leaving a pending journal, then upgrades that node and
completes rotation without signing the dashboard out or losing its monitor.

The metadata and unsupported-host controls run without Docker. They do not
establish that the actual published-baseline upgrade has passed; that result
must be recorded from the native Linux gate on the exact candidate source.

The first hosted attempt on PR #12 head `fe14b52` passed the dashboard/account
upgrade case, but the cluster case stopped before its first upgrade. Its
`quiet` call confused the expected webhook count with the two observer nodes:
the published baseline correctly delivered one outage alert. The corrected
fixture requires one outage and one recovery, as the existing connected-HA
proof does, keeps the same quiet duration, and additionally verifies the
original receipt ID before and after both upgrades and another restart. The
original failed result remains recorded; both complete cases must pass on the
new candidate head before upgrade acceptance.
