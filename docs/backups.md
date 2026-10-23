# Database backups and restore preparation

The node can take a consistent SQLite backup while monitoring continues:

```sh
canary backup-database /data/canary.db /backups/canary-2026-10-22.db
```

The source must be an existing initialized Canary database. The destination
directory must already exist and the destination file must be new. These local
filesystem commands do not need a management bearer or contact any service.

## What is saved

The backup contains the node's identity, monitoring configuration and revision
proofs, incident/run evidence, notification intents and receipts, local history,
accounts, password hashes, MFA seeds and password sessions. It also includes
provider credentials in monitoring configuration. Backups are not encrypted;
store them in a private directory or encrypted storage. POSIX output permissions
are `0600`; on Windows, access also depends on the containing directory's ACL.

The implementation uses a separate read-only connection and bound `VACUUM INTO`
output, rather than copying only the main database file. Committed WAL data is
included in a consistent transactional snapshot. See SQLite's
[VACUUM INTO contract](https://www.sqlite.org/lang_vacuum.html#vacuuminto).

The command creates a private temporary file in the destination directory,
checks database integrity and identity, closes SQLite, synchronizes file data,
and publishes through a hard link. It never replaces an existing destination.
The destination filesystem must support hard links; otherwise the command
fails. A destination ownership lock prevents another node from opening the
new path while publication is in progress. Failed operations remove their
temporary snapshot; the persistent `.lock` file can remain.

The JSON result gives `nodeId`, byte count, and SHA-256 of the completed file.
Keep these with the backup. File synchronization and consistent SQL contents
do not constitute power-loss certification of every filesystem's directory
metadata. Backups should be copied off the monitored host and checked by the
operator's normal storage process.

## Prepare and activate a restore

```sh
canary prepare-restore /backups/canary-2026-10-22.db /data/restored-canary.db
```

This creates a checked, self-contained copy, removes every password session,
and atomically marks the restored identity as quarantined. It preserves account
credentials/roles, configuration, coordination evidence and local history.
Its result sets `sessionsRevoked:true` and `requiresReenrollment:true`.

The staged database cannot start monitoring. Stop the original node before
activating the restored copy, and supply its **latest retained identity
directory**, kept separately from database snapshots:

```sh
canary activate-restore /data/restored-canary.db \
  --identity-directory /identity/canary
```

Configure the running node with `CANARY_IDENTITY_DIRECTORY=/identity/canary`
and retain that directory across restores. Outside containers, the default is
`.canary-identities` beside the database. The container default is `/identity`;
Compose mounts `canary-data` at `/data` and a separate `canary-identity` volume
at `/identity`. Use `--identity-directory /identity` when activating a restore
in that deployment. Retain the latest identity volume when restoring a database
or its data volume. Rolling both back together defeats sequence reservation.

Database backup commands do not copy the identity directory. Keep the latest
identity state through machine replacement as well, with the original node
stopped. Never run two copies using the same node identity or restore an older
identity snapshot alongside an older database.

Activation requires existing identity state for the same node ID and acquires
its exclusive lock. It reserves a sequence above the retained high-water mark,
resets every live monitor's checking generation, and closes old continuity
administratively without sending recovery alerts. Monitor IDs, placement and
settings remain; conflicting candidates remain conflicts. Accounts retain their
passwords, roles, API tokens and enrolled MFA, while password sessions and pending
MFA setups are revoked. Enrolled MFA may require waiting for the next unused
30-second step. Only the final activation transaction removes quarantine.

After activation, point `CANARY_DB` at the restored file and use the same
`CANARY_IDENTITY_DIRECTORY`. A failed activation keeps the file quarantined;
correct its cause and retry. The JSON result reports the node ID, count of
monitors reset, reserved sequence floor and activation time.

If the latest identity directory is lost, do not activate the old node ID from
an older copy of that directory. A fresh node identity and explicit cluster
re-enrollment are required; automated re-enrollment is not implemented. Never
remove quarantine manually, start concurrent copies of a node, or replace a
running database with a backup. Ordinary restarts on the latest retained
database remain supported.

## Upgrade an earlier Compose deployment

Earlier images kept identity state in `/data/.canary-identities`. Before starting
the new image against that database, copy its latest state into the new identity
volume while the old node is stopped. Run these commands from the same Compose
project directory so the existing data volume is selected:

```sh
docker compose build node
docker compose stop node
docker compose run --rm --no-deps --user root --entrypoint sh node -ec '
  source=/data/.canary-identities
  test -d "$source" && test ! -L "$source"
  test -z "$(find /identity -mindepth 1 -maxdepth 1 -print -quit)"
  set -- "$source"/*.json
  test -f "$1"
  cp -a "$source"/. /identity/
  chown -R canary:canary /identity
  chmod 0700 /identity
'
docker compose up -d node
```

The helper starts no monitoring process and copies rather than moves the old
state. It refuses a missing source or nonempty destination. If any step fails,
keep the node stopped and inspect that failure before continuing; a partial copy
is not an approved replacement for the latest complete directory. For a custom
old identity path, use that actual path as the source. An existing deployment
already using a separate retained directory should keep its mount and
`CANARY_IDENTITY_DIRECTORY` together. A new deployment needs no migration.

Retain `canary-identity` independently when replacing `canary-data`; each node
needs its own data and identity storage. These configuration changes do not
copy or modify an existing deployment automatically.

## Validation

Parent fixtures verify live committed WAL contents, whole-schema/data equality,
consistent snapshots during concurrent two-record transactions, hashes,
standalone files without WAL sidecars, session revocation/quarantine, ownership,
cancellation, foreign-source rejection and refusal to overwrite files. Restore
fixtures also exercise exclusive identity ownership, missing identity state,
activation rollback/retry, fresh sequence/generation, and validated peer
acceptance after an older backup replaces a later outage. These
use temporary local databases only. The escaped database-path fix also passes
actual Alpine recovery fixtures for literal `?` filenames; see
[container validation](container-validation.md).

Independent origin/restore-policy review found no correctness blocker. Focused
backup/recovery fixtures also pass on actual Alpine and restricted Docker; see
the dated artifact scopes in [container validation](container-validation.md).
The separate-volume deployment passed 11 actual Alpine/Docker checks; a stopped
migration and restore passed seven further checks with origin sequences advancing
across every reopen. Compose was unavailable, so equivalent `docker run` commands
exercised the migration rather than the Compose CLI. These results cover the
recorded deployment snapshot, not later application changes. Abrupt power-loss recovery is not established by
these tests.
