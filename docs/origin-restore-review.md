Independent restore/origin/checkpolicy review completed against the shared working tree on 2026-10-22. No correctness blocker was confirmed in this review's scope. This is a time-bounded review, not a freeze of the other agents' work. Only `apps/node-go/internal/canary/origin_restore_review_test.go` and this report were added by the reviewer; product/core files were read only.

The final focused integration run started at **2026-10-22 11:52:16 EDT** and finished at **11:52:24 EDT** (15:52 UTC). It passed **16 top-level tests** in `canary` (`4.902s` reported package time), including seven new independent review tests and the existing nine origin/restore/retry/backup tests. The pinned toolchain reported `go version go1.27.3 windows/amd64`; `GOTOOLCHAIN=local`, `GOWORK=off`, `CGO_ENABLED=0`, and `-mod=readonly` were used. Separate executions also passed `internal/origin` (`2.021s`) and `internal/checkpolicy` (`0.490s`).

Executed independent cases:

- A probe deliberately delays its exit after cancellation. Stop retains origin ownership until that writer exits; a differently named database and, on Windows, an uppercase directory alias cannot obtain that identity meanwhile. The canceled writer publishes no sample, policy or sequence, and the next owner skips the reserved block.
- A confirmed down run survives service pending, restart, retry pending, and an interval-only edit with its original down-run identity. Inversion changes the semantic generation, resets retry evidence, and administratively retires the old outage without a recovery receipt.
- An in-flight result from the previous document revision is rejected after an interval-only edit even though the check generation is preserved. It writes no sample, policy, sequence or history; the current revision begins with one accepted failure.
- Restore activation preserves two live conflicting heads, and separately a live/deleted conflict. Each live candidate receives its own child with a fresh generation; the tombstone is retained and no winner becomes runnable.
- Restoring a head whose parent edge is missing cannot manufacture complete proof. It stays unrunnable through activation; arrival of the missing real parent edge makes the restored child runnable.
- A later semantic revision retained by a peer and the reset revision created from an older backup survive actual snapshot-page serialization, wire validation and bidirectional merge as two conflicting heads.
- A trigger rejects the final quarantine update after activation has attempted auth changes. Accounts, TOTP replay counter, staged session, activation marker and quarantine roll back together. Retrying activation revokes sessions and pending MFA enrollment, keeps the enrolled seed, rejects all codes in the activation-time ±1-step window through the login endpoint, accepts a later unused step, and rejects replay of that accepted code.

The existing tests executed in the same final run additionally cover a failed origin allocator, startup with missing or already-owned retained identity state, activation commit rejection/retry, retry spacing and persistence, pending versus recovery, and policy/sample/history rollback. The parent's restore-continuity test was independently rerun here: an old backup replaces a later outage, fresh monitoring sends a new down notification rather than reusing the old receipt, and a real serialized/validated peer sweep accepts the restored generation and sequence. Its webhook is an ephemeral local test server. Backup tests exercised live WAL contents, concurrent atomic source transactions, session revocation, quarantine, integrity/hash checks, publication ownership, cancellation and foreign sources.

Source inspection, distinct from those executed cases:

- `ActivateRestoredDatabase` opens the private quarantined planner and does not call `Start`, `tick`, synchronization or delivery. CLI dispatch reaches backup/prepare/activation before management-client setup. This supports the no-network-before-activation conclusion; activation was not run under a network syscall interceptor.
- `restore_activate.go` resets live heads and retires prior generations while quarantine is still set. Those preparatory commits can survive a failed final activation; quarantine remains the gate and a retry creates another fresh child. The final transaction commits sequence floor, account changes, session deletion, activation marker and quarantine release together. This intentionally does not promise an unchanged staged database on failed activation.
- `runtime.go` accepts only the current exact monitor revision before applying policy. One database transaction stores sample, local policy, local sequence and history; in-memory publication follows commit. `Snapshot` carries observations, not the local `probePolicies` map. `sameCheck` preserves interval-only continuity and changes it for result-changing edits.
- `origin` reserves before issuing, permanently poisons a reservation after refill failure, keeps the lock until Close, and skips the previous durable reservation on reopen. Runtime Stop waits for its writers before closing the store and allocator. Lost retained state is rejected by the old-identity activation path; an older copy of that directory cannot establish the required latest high water.
- The updated `docs/backups.md` correctly distinguishes backup from the quarantine/session-revoking prepare step and describes activation with separately retained identity state. A plain backup retains its original logical contents, including sessions; it is an input to `prepare-restore`.

One **nonblocking source-verified documentation mismatch** remains in [`internal/checkpolicy/README.md`](../apps/node-go/internal/checkpolicy/README.md): its opening paragraph says runtime integration is deferred and changes no existing behavior. Runtime now calls `applyProbePolicy`, persists policy state and follows its retry spacing. That opening paragraph should be refreshed by its owner. No core edits were made to address it here.

Reproduction commands from the repository root:

```powershell
$env:GOTOOLCHAIN = 'local'
$env:GOWORK = 'off'
$env:CGO_ENABLED = '0'
& .canary/tools/go/bin/go.exe test -mod=readonly -count=1 -timeout=60s ./apps/node-go/internal/canary -run '^(TestOriginRestoreReview.*|TestOriginReservationsOutliveDatabasePathsAndProbeFailures|TestRestoreActivationRequiresRetainedIdentityAndResetsContinuity|TestRestoreActivationCommitFailureKeepsQuarantine|TestProbeRetriesPersistAndUseFailureSpacing|TestPendingRetryCannotRecoverOrReplaceConfirmedOutage|TestProbePolicyRollbackAndConfigurationValidation|TestDatabaseBackupCapturesLiveWALAndRestoreRevokesSessions|TestDatabaseBackupIsConsistentDuringTransactions|TestDatabaseCopiesRejectUnsafePublicationAndForeignSources)$'
& .canary/tools/go/bin/go.exe test -mod=readonly -count=1 -timeout=60s ./apps/node-go/internal/origin ./apps/node-go/internal/checkpolicy
```

SHA-256 observations for the reviewed files follow. The final run's runtime, causal-local and review-test hashes were checked immediately before and after that run and were unchanged. Other entries were recorded at 11:51:50 EDT; this report does not assert that all shared files remained frozen throughout the review. No Git state was inspected.

```text
apps/node-go/internal/canary/origin_state.go
27a820a85c153230dac015987c1d5ea30648553712592cee81af1d5b3372281e
apps/node-go/internal/canary/restore_activate.go
53e503d019406584330154fdb55a30ee5f818ebe509e587577f379dfd68a5aab
apps/node-go/internal/canary/backup.go
6486139e6e8557c20989590314af0e998a6a33c8f23054dc8c3b6c3df0826dc4
apps/node-go/internal/canary/retry_policy.go
61dcdd0a5d59474c7979e8fa6c534876da18534aca5902de213022f900f233f7
apps/node-go/internal/canary/runtime.go
1ba3c09b28139ed0a29f3cab80dda59fe63f7a1110f66864044439c7e60517fa
apps/node-go/internal/canary/store.go
ee6e655c782f823cc54735bd95299f8d608b771ba0d705c384f166ade60f3d57
apps/node-go/internal/canary/cli.go
a71cb7ee57538f0d19895203e8157d3ebba17056f7409b5091dbdef95e6c4e19
apps/node-go/internal/canary/lifecycle.go
a0a36f8b433d32139e65080c86f08fd3f9aeeabc1961a50a045d8343621763bf
apps/node-go/internal/canary/revision_joins.go
2f2e1f36fe17e4c5723749b959b9c6546e78f61d8e47fa1b69cde6942b74b7c8
apps/node-go/internal/canary/causal_local.go
3f0a72fbee4e70081ea6dabcd08999956cee7b9eceae848a6f683c8c7a800a70
apps/node-go/internal/canary/origin_restore_review_test.go
f30acbf2d63e42baa96bc95428c08bf39a12579de23571a4375b0ebd1c80aabd
docs/backups.md
72c2ed469360a436b38b88643064c1684de5fd4f94d7453a546bc935e88db308
```

All review databases and identity directories were temporary private fixtures. Review probes returned synthetic local results and no live notifications were sent. No `.env`, user database, default application executable, Git command or nested agent was used. The inherited report of Alpine verification for the historical literal-`?` SQLite path issue was not independently rerun here. This review did not run the full repository suite, a race-enabled build, Linux/Alpine activation, a real storage power-loss test, or future changes made after the recorded run.
