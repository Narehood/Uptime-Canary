# Development storage

Development resumed after removing 33.3 GiB of accumulated validation files.
The repository baseline is approximately 940 MiB, including dependencies, the
portable Go toolchain, application data and retained small source/report files.
The old external Go compiler cache was also cleared; its former 7.6 GiB is not
an allowance to accumulate more files elsewhere.

All agent builds and tests use `scripts/bounded_validation.py`. It permits one
command at a time, one shared owned Go build cache, and one disposable work
directory. It monitors the repository against a **3 GiB hard ceiling**, command
scratch against **512 MiB**, and shared module-cache growth against **256 MiB**
from the recorded baseline. An unexpected external compiler cache over 16 MiB
also stops the command. The idle target is **1.25 GiB**.

The runner terminates its command tree on a limit or timeout. Afterward it
removes its scratch and output files and trims its owned cache to 256 MiB.
Only the latest bounded log and result are kept. It never deletes application
data, source, the shared module cache or arbitrary system directories. If safe
cleanup fails or the footprint remains above budget, work pauses for the user.

Scratch accounting includes both compiler temporary files and disposable build
outputs. Results record the highest usage observed by the periodic monitor;
these sampled peaks are not a measurement of every instantaneous allocation.

No per-task cache copies, repeated whole-source snapshots, archived binaries,
browser-profile collections or persistent validation VMs may be created.
Reviewers author small fixtures against existing source; root runs the checks.
Current source hashes and concise results replace duplicate source trees.
The v2 source fingerprint covers Go, JS/MJS/CJS, TypeScript/TSX, styles, HTML,
Python, module/JSON files and deployment configuration/Dockerfiles. Environment
files and generated directories stay excluded. Earlier v1 fingerprints did
not include TypeScript; their historical hashes must not be presented as full
dashboard/proof-source immutability evidence. Eight guard regressions pass,
including detection of frontend changes and exclusion of generated/private data.
Future Alpine work must fit the same budget with disposable infrastructure;
the old multi-gigabyte persistent VM must not be recreated.

Example from the project directory:

```powershell
python scripts/bounded_validation.py --check
$GoTool = (Resolve-Path -LiteralPath '.canary/tools/go/bin/go.exe').Path
python scripts/bounded_validation.py --timeout 180 -- $GoTool test -mod=readonly ./apps/node-go/internal/heartbeat
```

Commands run without application `CANARY_*` environment settings. They do not
load `.env`, and compilation never changes persistent machine configuration.

Automatic approval review rejected removal of four opt-in identifier-compaction
experiment files, `blocked by policy`. Their exact paths and hashes are recorded
in `.canary/jsonata-identifier-study-result.json`; do not retry that deletion by
another tool. The rejected private experiment occupies 97,644 bytes and is not
enabled in production or ordinary candidate builds. Retaining it does not breach
the idle budget. Its failure does not justify another Alpine run.
