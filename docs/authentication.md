# Authentication implementation contract

The optional dashboard can sign in with a node-local username/password, an
authenticator code when enabled, or a headless account token. Accounts and login
sessions remain local to each node. This document records implemented API
behavior; dashboard integration is a separate step.

- `GET /api/auth/status` is public and reports password login support, whether
  any password exists, `setupRequired`, token support, and MFA support.
- `POST /api/auth/login`: `{name,password,code?}`. Returns
  `{token,expiresAt,user}` or `{mfaRequired:true}` after a valid password when a
  code is still required. Invalid credentials return 401. The returned opaque
  bearer session lasts 12 hours, is persisted only as a hash, and can be revoked.
  The dashboard must keep it in memory, not browser storage or a URL.
- `GET /api/auth/me`: this account's public view.
- `POST /api/auth/password`: `{password,currentPassword?,code?}`. The existing
  account token authorizes first password setup. Later changes require the
  current password and a fresh authenticator code when enabled. Changing the
  password revokes this account's password sessions and clears any pending MFA
  enrollment. API tokens are independent.
- `POST /api/auth/logout`: revoke this password session. API tokens remain valid.
- `POST /api/auth/logout-all`: revoke all this account's password sessions and
  advance its credential version so earlier in-flight logins cannot recreate one.
- `POST /api/auth/totp/setup`: `{currentPassword}`. Returns a secret and
  `otpauth://` URI for a 10-minute enrollment, without enabling MFA yet.
- `POST /api/auth/totp/confirm`: `{currentPassword,code}`. Enables RFC 6238
  SHA-1, six digits, 30 seconds, with one step of clock tolerance. Revokes
  sessions; the operator signs in again with a new code. Codes cannot be reused.
- `POST /api/auth/totp/disable`: `{currentPassword,code}`. Disables MFA and
  revokes password sessions. Account/password administration needs an independent
  administrator if the authenticator is lost.
- Admin `POST /api/accounts`: `{name,role,password?,token?}`; require at least
  one credential. Both are supported for the same account.
- Admin `PUT /api/accounts/:id`: change name, role and/or token. Revokes sessions.
- Admin `POST /api/accounts/:id/password`: `{password,resetMfa?}`; reset a
  password and optionally lost MFA, revoking sessions and clearing pending MFA
  enrollment. Enabled MFA remains enabled unless `resetMfa` is true.
- Admin `DELETE /api/accounts/:id`: remove the account and its sessions. The
  last administrator cannot be deleted or demoted.

Identity mutation bodies are JSON objects with the exact fields documented for
their route. Unknown, duplicate, differently cased, null, and incorrectly typed
fields return 400. Logout and DELETE accept an empty body or `{}`. Unsupported
methods, trailing slashes, and empty account IDs do not alias supported routes.
Password reset uses its POST subroute; PUT does not accept password or MFA fields.
Password changes require 12 to 128 characters, without NUL, CR, or LF.

Passwords use a fresh random 16-byte salt with Argon2id (19 MiB, two iterations,
one lane, 32-byte output). Password derivation runs outside the monitoring lock
with two concurrent workers. Worker saturation returns 429 and does not block
logout or account deletion. Login attempts are limited to 10 per minute per
socket IP; forwarded headers do not confer trust. The process-local limiter keeps
at most 4096 IPs; at capacity, new IPs receive 429 while existing IPs retain their
remaining attempts. Its windows reset after one minute.

At most eight active password sessions are kept per account. A new login evicts
the oldest when needed, and prunes expired, orphaned, or revoked session records.
Expiry is absolute; a session is invalid at its `expiresAt` timestamp. Account
credential versions invalidate persisted sessions across restarts. Identity
mutations recheck the bearer and account version after request-body reads and
password hashing. Logout-all and changes to a password, MFA, role, or API token
advance the account version. Account changes, MFA replay counters, session
creation, and associated session deletions commit atomically. A persistence
failure returns 503 and faults the runtime instead of reporting a successful
revocation or login. In-memory authentication changes follow a successful commit.
Bootstrap environment credentials initialize only an empty account database;
they cannot resurrect a revoked token or undo account edits on restart.

The API accepts explicit bearer credentials rather than ambient cookies, so a
separately hosted dashboard does not depend on third-party cookie access. Exact
CORS origins still apply. Use HTTPS or a trusted private network. Protect the
SQLite database and backups: password/token hashes, authenticator seeds and
monitoring configuration are sensitive local records. MFA seeds are not currently
encrypted at rest. Authentication does not replicate with operational snapshots.

A database or backup reader can recover the MFA seeds. A future at-rest encryption
change should encrypt both pending and enabled seeds with an authenticated cipher
(for example AES-256-GCM), unique random nonces, and associated data binding the
node, account, and seed purpose. The node key must be supplied outside SQLite and
its backups; migration and restore behavior need an explicit contract before
shipping that change. No seed-encryption or key-rotation API is implemented here.

Integration review found an outstanding race in the operational API outside these
identity handlers: it authenticates before reading the body and later passes the
stale account to `apiLocked`. Reproduced: pause an editor's monitor-creation body,
demote the editor, then finish the body; creation returns 201 instead of 403.
The parent must reauthenticate under `n.mu` immediately before `apiLocked` and
apply RBAC to that fresh account, covering token rotation, logout, expiry, account
deletion, and role changes during request-body I/O. This is required before
claiming revocation is enforced across all management endpoints.

Password hashing parameters follow the [OWASP password storage guidance](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html).
## Offline account recovery

An operator with access to the node's database can recover an existing account
without its current token, password or authenticator. Stop that node first. The
recovery command requires the database's exclusive OS ownership lock and does
not start monitoring or contact peers.

```sh
canary recover-account /data/canary.db admin /run/secrets/new-password --reset-mfa
```

The password file supplies 12-128 characters; one final LF/CRLF is allowed and
password spaces are preserved. The command never prints the password. Keep the
file private and remove it after recovery according to your deployment's secret
handling. File access is the recovery authority, so this command intentionally
does not accept a remote bearer as a replacement for database access.

The password reset and revocation of all password sessions for that account are
atomic. Role and API token remain unchanged by default. Add `--reset-mfa` for a
lost authenticator and `--revoke-token` to disable the old API token. Other
accounts, node configuration, peers and measurements are unchanged. An existing
database and unambiguous account name are required; recovery does not create a
new database or silently promote an account to administrator.

The implementation has exclusive-ownership, actual credential verification,
session isolation, transaction rollback, restart persistence and foreign-file
non-mutation regression coverage. This is not a full database backup/restore
facility.

