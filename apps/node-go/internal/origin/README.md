# Retained origin sequence reservations

`origin` is independent of SQLite, the node runtime, networking, and clocks. It
reserves ranges of the node's existing positive `int64` observation/run sequence
in a retained filesystem directory. It does not change the wire identity or
sequence ceiling (`9007199254740991`, or 2^53 - 1).

## Stable integration API

```go
reservation, err := origin.Open(origin.Options{
    Directory:       identityDirectory,
    NodeID:          nodeID,
    MinimumSequence: latestDatabaseSequence,
    RequireExisting: restoringAnExistingIdentity,
})
if err != nil {
    return err
}
defer reservation.Close()

// Inside the caller's serialized database write transaction:
sequence, err := reservation.Next()
if err != nil {
    return err // Stop local issuance. Never fall back to DB incrementing.
}
// Atomically store sequence in settings and in the new local fact/run.
// A rolled-back transaction simply leaves a harmless sequence gap.
```

`Open(Options) (*Reservation, error)` acquires the identity lock and persists its
first block before returning. `Next() (int64, error)` issues an already reserved
number, extending the reservation before crossing a block boundary. `Close()
error` releases the lock and is idempotent. Next and Close are safe concurrently;
do not copy a Reservation. A zero Reservation is closed. Options have exactly
these fields: `Directory string`, `NodeID string`, `MinimumSequence int64`, and
`RequireExisting bool`.

The caller serializes transactions so committed fact order matches allocation
order; concurrent Next calls alone guarantee distinct allocations, not DB commit
ordering. Use one allocator for all local observation/run sequence consumers.
Open it before enabling any local issuance and keep it until writers stop.
Incoming peer facts do not allocate or advance this node's local sequence.

The first issued number is greater than both the retained high water and the
caller's minimum. Each block contains 65,536 numbers, except a shorter final
block at the ceiling. Every reopen skips the entire previous reserved range,
even after a clean close. Gaps and burned blocks are intentional. An exhausted
minimum is durably retained, and an exhausted identity cannot restart at a lower
sequence. No time, random jump, peer claim, or eviction affects allocation.

`errors.Is` recognizes `ErrLocked`, `ErrInvalidState`, `ErrMissingState`,
`ErrExhausted`, and `ErrClosed`. Other errors report filesystem failures.
Failed Next calls return zero. Any refill failure, including an ambiguous
post-rename sync failure, permanently fails that Reservation while keeping its
lock. Stop issuance, Close, and reopen the retained state before retrying.
Open failures release their lock. Close never removes or changes retained state.

## Files and durability

Use a dedicated, local, persistent directory. On Linux it must have mode 0700;
new directories are created with that mode, and manifests/lock files use 0600.
Existing files with unexpected permissions fail closed. On Windows provision
the directory with an ACL limited to the service account and administrators;
POSIX mode bits do not set a Windows ACL.

Each NodeID gets `origin-<sha256 of exact UTF-8 ID>.json` and a separate permanent
empty `.lock` file. Colons, path separators, and Windows reserved names in an ID
never become filename syntax. The NodeID must contain 1..128 valid UTF-8 bytes;
the caller remains responsible for application identity validation. ID case and
Unicode normalization are significant. Directory aliases resolve to the same
OS-locked inode; separate databases using the same directory and ID contend for
that lock. Contention fails immediately. The lock file is never deleted or
replaced, including after failures. Do not rename/remove the directory or lock
file while it is in use. Advisory Linux locks require cooperating processes.

The manifest is a UTF-8 JSON object of at most 4096 bytes:

```json
{"version":1,"nodeId":"the exact node identity","highWater":65536}
```

Only those three case-sensitive fields are accepted. Unknown/missing/duplicate
fields, nulls, wrong types, fractional/exponent sequences, trailing data,
unsupported versions, identity mismatches, and out-of-range values fail closed.
Empty, short, and corrupt existing files are never initialized over. Symlinks,
hard links, directories, and special files at manifest/lock paths are rejected.
Unrelated files are never removed, and stale temporary files are not swept.

Publication writes a unique 0600 temporary file in the same directory, syncs
and closes it, atomically publishes/replaces the manifest, and only then permits
issuance. Linux uses flock, no-follow opens, rename, and directory fsync;
first publication uses renameat2(RENAME_NOREPLACE) to prevent overwriting an
unexpected file. Filesystems/kernels without that operation fail closed.
New Linux directories and their parent directory entries are synced as well.
Windows uses LockFileEx and MoveFileExW with MOVEFILE_WRITE_THROUGH, adding
MOVEFILE_REPLACE_EXISTING only for a validated existing manifest. Its open lock
handle denies delete sharing. Linux amd64/arm64 and Windows require no CGO.
The only nonstandard import is the project's existing golang.org/x/sys module.

If a crash leaves the old reservation, it covers every number returned before
the unfinished publication. If it leaves the new reservation, reopening skips
that new range too. This relies on the local filesystem and storage device
honoring atomic replacement and flush/write-through requests. Network/shared
filesystems and storage that lies about flush completion are outside the
contract. The private directory must not be modified by other code while open;
this package is not a defense against administrators or arbitrary same-user
filesystem mutation.

## Restore limits

- Restoring an old DB under the old node ID is supported **only while the latest
  independent identity directory survives**. Keep that directory outside SQLite
  snapshots and mount it on retained storage in containers.
- For an old identity use `RequireExisting: true`. A missing manifest is an
  error, even if the caller supplies a minimum. A fresh allocator can bootstrap
  with `RequireExisting: false` and the latest DB sequence, but that setting
  cannot distinguish a genuinely new identity from deleted identity state.
- Losing the identity directory requires a **new node ID and re-enrollment**.
  An older database cannot reconstruct numbers already seen by peers.
- **Do not copy a backup over the latest identity manifest.** Restoring or
  cloning that directory backwards defeats the high water guarantee. Never run
  cloned copies of one identity directory on independent hosts.
- This package prevents integer reuse on a retained host identity. It does not
  itself provide HA episode continuity, reconciliation, fencing across hosts,
  a DB incarnation, a wire epoch, or a distributed sequence service.
