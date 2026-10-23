# Offline account recovery: independent review

Reviewed 2026-10-22. Scope: `recovery.go`, its CLI dispatch, SQLite ownership and
URI handling, password primitives, restart authentication, and the existing four
recovery regressions. This review owns only this report and
`apps/node-go/internal/canary/recovery_review_test.go`; production sources and
modules were read only.

Portable release sign-off is withheld for the path-selection defect below. The
exercised Windows recovery behavior passes. The Unix defect is established by
the application and pinned driver's source; its two executable regressions need
a Unix run and are explicitly skipped on Windows.

## Finding: P2 — a literal `?` selects a different writable database on Unix

Locations: `apps/node-go/internal/canary/recovery.go:40`,
`apps/node-go/internal/canary/recovery.go:67`, and
`apps/node-go/internal/canary/store.go:39` / `:50`.

The read-only precheck uses an escaped `file:` URI. The writable `openStore`
instead passes its canonical filesystem path directly to `sql.Open("sqlite",
path)`. In the pinned `modernc.org/sqlite v1.61.0`, `conn.go`'s `newConn` splits a
non-`file:` DSN at the first `?` before opening it. `?` is legal in Unix filenames.
The ownership lock is still taken for the complete, untruncated filename.

For an initialized file named `literal?mode=rwc.db`, the precheck reads that
file, recovery locks `literal?mode=rwc.db.lock`, and the SQLite connection opens
the sibling `literal`. If that sibling is absent, recovery creates and
initializes an unintended SQLite file before reporting that the account was not
found. If it is another node database containing the same account name, recovery
changes that other account and its sessions. A node holding `literal.lock`
does not prevent the operation. The requested file retains its old password.

This is a wrong-file mutation and ownership mismatch during an authorized
recovery operation. It does not require an adversary to replace a database,
edit ownership files, or race the operator. Windows disallows this filename, so
the native Windows results cannot establish the Unix behavior dynamically.

Two portable regression fixtures are included:

- `TestRecoveryReviewQuestionMarkFilename` requires recovery of the literal
  filename, verifies its actual password, and rejects creation of the truncated
  sibling.
- `TestRecoveryReviewQuestionMarkCannotSelectAnOwnedSibling` keeps the sibling
  owned by another `Node`, gives the requested account a different role, and
  requires that sibling's entire schema and data to remain unchanged.

Both are expected to fail against the reviewed source on Unix. Use an escaped
`file:` URI for the canonical writable path so the SQLite file and ownership
file identify the same database. Alternatively reject unsupported paths before
opening anything writable. Run these fixtures on Unix before closing this
finding. No production fix was made within this review's ownership.

## Verified behavior

The independent fixtures use temporary database/password files and direct HTTP
handlers. They never start monitoring or a listener. Cross-process checks invoke
only the temporary Go test executable. CLI checks provide an HTTP client and
environment getter that fail if used.

- Recovery preserves admin/editor/viewer roles, account IDs, and API hashes by
  default. All four reset/revoke combinations work after a fresh `New`.
- Old passwords and every target session, including expired and stale-generation
  records, stop authorizing after restart. Another account's API token and
  session remain usable. Explicit API revocation survives startup with the
  original bootstrap token configured. The new password creates an authorizing
  session.
- Default recovery retains the TOTP secret and replay counter and still requires
  an authenticator code. Explicit reset clears them. Both paths clear pending
  enrollment and increment `AuthVersion` exactly once.
- Every application table and schema definition outside the target account and
  its sessions is compared before/after successful recovery. Fixtures include
  another account, runtime settings, unrelated records, and raw history.
- Pre-cancelled contexts, unknown/ambiguous names, malformed stored account or
  session JSON, and an injected account-update SQL failure leave all logical
  data and schema intact and release ownership. The parent's session-delete
  failure regression also passes, proving transaction rollback after account
  update.
- A separate process rejects recovery while a node holds the OS lock, using
  absolute, relative, and dot-segment aliases of a filename containing spaces,
  `#`, `%`, `+`, and non-ASCII text. Recovery succeeds using the relative path
  after the owner closes. The existing process-death test confirms the kernel
  releases ownership without requiring deletion of the persistent lock file.
- CLI inputs reject invalid UTF-8, NUL, embedded or repeated line endings, a bare
  CR, short/long passwords, files over 514 bytes, missing files,
  directory input, unknown/repeated flags, and incorrect argument counts.
  Twelve-rune passwords, 128 ASCII runes, preserved spaces with LF, and 128
  four-byte Unicode runes plus CRLF (exactly 514 bytes) work. The two flags
  together and the parent's single-flag invocation are exercised.
- Errors do not echo supplied password bytes, secret flag/path values, or an
  injected SQL diagnostic secret. The success value serializes only the public
  `AccountView`. The CLI entry point prints this value or the sanitized error;
  it never prints the password-file contents.
- Empty/non-SQLite files and SQLite files with foreign schema/user data, no
  identity, malformed identity JSON, or invalid identity fields retain identical
  bytes. Rejection creates no lock, journal, or other sibling file. The parent's
  missing-file and foreign-file tests also pass.

## Execution and limits

Portable workspace Go: `go1.27.3 windows/amd64`. Commands use the workspace module
and build caches with `GOENV=off`, `GOTOOLCHAIN=local`, `GOWORK=off`, and
`GOFLAGS=-mod=readonly`. No module edits or downloads were required.

```text
.canary/tools/go/bin/go.exe test ./apps/node-go/internal/canary \
  -run='^Test(RecoveryReview|OfflineRecovery|RecoveryCLI|RecoveryRejects|DatabaseOwnershipSurvivesProcessDeathWithoutStaleLock)' \
  -count=1 -timeout=90s -v
PASS — 10.695s, before adding the second Unix-only regression
```

The successful non-race run exercised six new substantive groups with 35 named
subcases, the process helper, all four existing recovery regressions, and the
existing process-death ownership regression. The Unix-only filename test was
skipped. The first development run exposed a fixture using the existing
network-based login helper with an unstarted node; this was corrected to invoke
the handler directly before the successful run.

The final focused native race run used the same selector with `-race`,
`CGO_ENABLED=1`, and the workspace's portable w64devkit compiler:

```text
PASS
ok  uptime-canary/apps/node-go/internal/canary  21.005s
```

It compiled the final test file and completed with 12 top-level passes
(including the process helper) and two explicit Unix-only skips. All 35 named
subcases passed, with no race reports. This was a focused package run; no full
suite was repeated.

Windows denied creation of file and directory symlinks with `A required
privilege is not held by the client.` Those alias fixtures remain conditional;
their canonical `EvalSymlinks` path was inspected but not dynamically proven
here. No registered local WSL distribution was available. Unix `flock`, the two
literal-`?` regressions, UNC/network filesystem behavior, and power-loss recovery
were not executed. Context coverage is pre-cancellation, not a timing-dependent
interrupt during Argon2 hashing. Logical non-mutation checks on recognized node
databases do not assert byte identity after WAL opening/checkpointing. Foreign
file fixtures do assert byte identity.

Keeping password-file permissions/removal under the filesystem administrator's
control, accepting recovery without a remote bearer, retaining MFA unless
explicitly reset, and preserving the account's role are intentional policy
choices consistent with the brief. The identity precheck recognizes an existing
node marker; it is not a provenance or database-repair facility. This review
does not claim new peer-replication or browser coverage and does not rerun the
parent's full race suite on its frozen snapshot.

No user database, `.env`, default binary, port 3100, live external service,
production source, UI, or module file was opened for mutation. No commits or
subagents were created.

Reviewed SHA-256 identities (unchanged across both focused runs):

```text
recovery.go            c0c21d2d5b327a847f93534a9f3540b01c7a3967d45565db542280edf51c1a64
store.go               e61a83d45fd42d2117d859c5c19a313d3752ba8e92cb6f6bb863df8ae7b8a3f9
store_lock_windows.go  01d2a0c5a67228e31bc85cd6769e0a98e19fbd52366005eb210b98596ee6686f
store_lock_unix.go     be824974977661ee2d593dab043d399cd2caaf0818185f3caa037d51a447e838
cli.go                 9ae235bcf68e09389715c23de4d6128e45fce860aa0a256146f4788a203c5f32
```
