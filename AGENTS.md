# Uptime-Canary working rules

The goal remains a ready-to-ship independent Go/SQLite HA monitoring system
with monitoring and notification abilities near Uptime Kuma. Do not narrow
that goal or describe component checks as a complete release validation.

## Storage is a user requirement

- Run agent builds/tests through `python scripts/bounded_validation.py`.
  Root coordinates all commands that produce build or runtime artifacts;
  other agents inspect source and write small fixtures until root runs them.
- For Docker-image work, use Linux/container or hosted Linux validation. Do
  not launch native Windows application or test executables on this workstation;
  they trigger unwanted Windows Firewall prompts.
- Only one validation command may run at a time. Reuse the one owned Go cache.
  Never copy build/module caches or create per-task source snapshots, binary
  archives, browser-profile collections or persistent validation VMs.
- The monitored repository ceiling is 3 GiB; the idle target is 1.25 GiB.
  Disposable scratch is limited to 512 MiB. The runner trims its cache to
  256 MiB and retains only bounded logs and twelve small command results.
- Shared module-cache growth is also monitored. Do not shift artifact or cache
  growth to another directory to evade the repository limit.
- If storage grows beyond the budget, stop the producing command and reclaim
  only its known disposable files. If safe cleanup cannot restore the budget,
  stop delegated work and pause the goal until the user assists.
- See `docs/development-storage.md`. Do not recreate the old multi-gigabyte
  Alpine VM or private Go caches.

## Preserve application data and scope

- Never read `.env`, use the user's database for tests, or replace a running
  user's binary. Use private temporary databases, loopback fixtures and no real
  external notifications or paid remote measurements.
- Never delete `.canary/browser-auth-proof/run-ZgaV8S`; an earlier cleanup
  approval rejected that artifact. Application databases, backups and retained
  identity state are also outside generated-file cleanup scope.
- Opus 5.5 xhigh owns graphical work: layout, styling, visual previews and
  graphics under `apps/web`, `design`, `docs/assets`, and `badges_svg.go`.
  Per the user's clarification, Codex/root owns non-visual frontend logic,
  API helpers, tests, README/design prose and other documentation, as well as
  core integration, shared types, modules and release validation.
- State exact source and platform limitations. Preserve original failing
  regressions; change an expectation only when the accepted behavior changes
  explicitly and reference evidence supports the new expectation.
