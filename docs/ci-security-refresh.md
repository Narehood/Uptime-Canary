# CI review and dependency security refresh

Claude Opus 5.5, configured at **High** effort, reviewed the existing tests,
builds and CI before this refresh. The review was read-only. Root implemented
the small reductions below while preserving the original runtime, race,
capacity, interoperability and release assertions and deadlines.

## CI findings and decisions

The observed `d848e92` initial run consumed approximately **3,668 runner-seconds**
(61.1 runner-minutes), excluding its subsequent proof retry. The race job took
**1,682 seconds** and determined wall-clock completion; its Canary package
accounted for about 1,471 seconds. The process proof took 629 seconds on its
failed attempt, automation 393 seconds and release validation 335 seconds.
These are observed baseline timings, not measurements of the refreshed pipeline.

| Finding | Decision |
| --- | --- |
| Automation repeats native provider, delivery and scheduling tests already covered by the shipped-mode Alpine Go job | Remove those three duplicate invocations. Their original steps consumed up to 113 seconds; net savings are unmeasured because later Chrome work also used their warm compile cache. |
| Automation and image builds run for unrelated docs-only changes | Filter those runs. Keep `docs/node-onboarding.md` as an explicit input for image and runtime validation because a web-build contract reads it. PR filters inspect the entire PR diff, so a docs-only follow-up commit on a code PR still runs CI. |
| The web Dockerfile type-checks the same project twice | Keep shared-package checking and the `tsc` inside `npm run build`; remove only the duplicate web invocation. |
| A nonroot-image assertion is silently unavailable during web-image build | Copy `Dockerfile.node` into that build context so the existing assertion runs. |
| Race dominates wall clock | Retain the complete race job here. Three balanced shards might reduce its duration, but that is a model, requires disjoint-cover and zero-test guards, and increases total runner work. Handle it separately from a security refresh. |

Keep all 25 process proofs, including the capacity case, all shipped-mode Go
packages, race instrumentation, node/web hardening and restart checks, Compose
proofs, native release/upgrade proofs and real automation forms. Pure frontend
suites are short and often hidden behind Go compilation, so they remain in
the web build. No cache copies, persistent validation VMs or image archives
between jobs were introduced.

## Runtime and dependency updates

| Component | Refresh |
| --- | --- |
| Alpine runtime | Explicit `alpine:3.24.3` for node/web; Alpine 3.24 variants for Node and Go builders. Runtime stages run `apk upgrade --no-cache` before package installation/use. |
| Node | 24.22.1 to **24.22.2** in images, CI and the exact isolated PM2 launcher profile. |
| Go toolchain | **1.27.3** retained: it is the latest stable release in the official release feed checked for this refresh. An upcoming release is not substituted for a published one. |
| Go modules | Sixteen existing requirements updated, including SQLite, Goja, SSH/SFTP, SQL/Mongo clients and supporting packages; an unused PBKDF2 requirement disappears after `go mod tidy`. |
| Frontend tooling | Vite **8.3.6**, React plugin **6.2.1**, React type packages **19.3.0** and refreshed compatible lockfile resolutions. |
| TypeScript | **6.0.3**, the maintained JavaScript compiler/API line. Version 7 removes the compiler API used by the catalogue extractor; replacing that tooling is a separate migration. Node type declarations stay on the supported Node 24 line. |
| PM2 development fixture | **7.1.2**, with npm integrity verification and refreshed nineteen core-source hashes. Production monitoring continues accepting exact 6.0.8 and 7.1.1 profiles and adds exact 7.1.2. Unknown, decorated and future versions remain rejected. |

The Goja replacement keeps the existing own-data inspection helper intact.
Its new upstream production files and module checksum were verified against
the Go checksum database; its complete recorded inventory and source hashes
were refreshed. Upstream replaces `parser/regexp.go` with `float16.go`; no
application code was copied into a cache or source snapshot.

The source scan retained a module-level
[GO-2026-5932 advisory](https://pkg.go.dev/vuln/GO-2026-5932) for the unmaintained
OpenPGP packages in `golang.org/x/crypto`. Although no reachable symbol was
reported, this refresh pins the advisory's fixed upstream revision
`v0.58.1-0.20261009181718-2f1f834330b2`. The subsequent scan retains no Go findings.
The node does not gain an OpenPGP feature.

Alpine's [3.24.3 release announcement](https://www.alpinelinux.org/posts/alpine-3.21.9-3.22.7-3.23.7-3.24.3-released/)
lists core security fixes, including musl and zlib plus the OpenSSL advisory
batch. [Node 24.22.2](https://nodejs.org/en/blog/release/v24.22.2) is a published
patch release. The [Go release feed](https://go.dev/dl/?mode=json) and
[TypeScript API transition documentation](https://devblogs.microsoft.com/typescript/announcing-typescript-7-0/)
explain the toolchain choices above. These version choices alone are not an
image vulnerability-scan result.

## Security checks added to CI

Each existing image-build job now scans its own result with **Trivy 0.76.2**,
installed through a commit-pinned installer. PR builds load their image locally;
main scans the exact pushed digest remotely, preserving the build action's
default registry provenance. The existing image-publication gate therefore
requires successful scans without changing its required job names.

The scanner requires a complete Alpine package inventory, Alpine 3.24.3 or a
later 3.24 patch, a supported OS, the pinned scanner and database metadata.
Errors or incomplete evidence fail closed. **HIGH and CRITICAL findings block
the job, including unfixed findings.** Every severity is retained; passing this
threshold does not mean there are no lower-severity findings. The database,
raw report and temporary files use bounded scratch; one small summary survives
as the job's security artifact. A hardened, network-isolated startup/health
check also exercises the actual GameDig runtime image.

Release validation now runs live npm audits for all three lockfile roots,
Go source-symbol vulnerability analysis and advisory matching for embedded
and locally replaced code. npm/embedded findings and scan failures block the
job; Go's source-symbol policy blocks reachable vulnerable code and retains
module-only findings. Third-party-notice verification and its controls are
also release gates. Notices were regenerated for the updated dependencies.

## Validation and remaining work

Sixteen guarded local check groups pass, covering all workspace types, scanner
admission controls, attribution, provenance, PM2 compatibility, onboarding,
automation contracts and both calculation-equivalence proofs. The production
web bundle builds successfully into disposable guarded outputs. The refreshed
live scan reports zero npm vulnerabilities across the three roots, no Go
findings and no active advisories for the embedded/replaced sources. These
are bounded local results; they are not Linux container execution.

Matching-head hosted image scans, PM2 interoperability, native/runtime proofs
and release builds must pass before merge. Preserve the initial capacity
timeout and unchanged passing retry from `d848e92` as historical evidence;
its root cause remains unproven. Do not weaken it to make the refresh green.

Deferred recommendations are balanced race sharding, periodic advisory/image
rechecks, making automation a publication requirement and stronger branch
protection. Repository settings are not changed here. Runtime proofs still
exercise sibling builds of the same source, while the security scan on main
targets the exact promoted image digest. No component result establishes full
Go/SQLite HA release readiness.
