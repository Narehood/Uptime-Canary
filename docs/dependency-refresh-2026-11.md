# November dependency and container refresh

The November 21 refresh checks the npm registry, Go module proxy, official
runtime release feeds and Docker registry. Existing published versions stay
when they are current; prereleases are not substituted for stable releases.

- Node advances from 24.22.3 to **26.13.1 LTS** in the web builder, optional
  GameDig service, process-proof image and Linux CI. Both npm engine declarations
  require `>=26.13.1 <27`, and Node declarations advance to **26.6.6**.
- TypeScript advances to **7.0.2**. The two scripts using its compiler API import
  the official **@typescript/typescript6 6.0.2** compatibility package, which
  resolves the maintained **6.0.3** API. Ordinary checking uses TypeScript 7.
- GameDig stays at its current **5.3.3**; its XML parser override advances from
  **5.12.0** to **5.12.1**. The dashboard, service and PM2 fixture locks refresh
  compatible transitive resolutions. PM2 remains **7.1.2**.
- Go requirements refresh SQLite to **1.62.0**, pgx to **5.13.0**, MongoDB
  to **2.10.1**, MSSQL to **1.12.0**, and the supporting crypto, network,
  compression and other modules. Goja advances to upstream commit
  `79d6ad37b976`; its own-data helper is retained byte for byte, and its
  provenance and third-party notices are regenerated.
- Go **1.27.4** and Alpine **3.24.4** remain current. All Dockerfile base images
  are pinned to registry-verified manifest digests, including Node 26's Alpine
  3.24 build. Runtime stages retain `apk upgrade --no-cache`.
- CI uses setup-go **7.1.0**, docker/login-action **4.8.0**, and govulncheck
  **1.10.0**. Other action and Trivy pins were checked against current releases.

The isolated PM2 fixture's exact Node admission advances with CI. Source review
of Node 26.13.1's `lib/internal/cluster/primary.js` confirms that
`createWorkerProcess` still calls `child_process.fork` without a detached option;
the source SHA-256 is
`bf8ec4ef724b4f6ba4d5363252ec9ff841a0e04e275b364f20e3ce7d19b325dd`.
The namespace, privilege-drop, source-integrity and supervisor-death assertions
remain required. This source review alone does not prove runtime containment.

Go module and attribution preparation runs on a disposable GitHub Linux runner
because the workstation's shared module cache is close to its growth allowance.
No native Windows application or test executable is launched. Dependency,
container and interoperability checks are component evidence; they do not
constitute a complete release signoff for the monitoring system.

Sources: [Node release](https://nodejs.org/en/blog/release/v26.13.1/),
[Go releases](https://go.dev/dl/),
[Alpine releases](https://www.alpinelinux.org/releases/),
[TypeScript compatibility guidance](https://devblogs.microsoft.com/typescript/announcing-typescript-7-0/),
[Node cluster source](https://github.com/nodejs/node/blob/v26.13.1/lib/internal/cluster/primary.js).
