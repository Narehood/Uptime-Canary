# Dependency security checks

Use `python scripts/security-check.py` after source integration settles. The
runner executes npm's advisory audit and Go's source-symbol vulnerability
analysis for Linux amd64 by default. It records raw output, the scanner/database
configuration, exit codes and source hashes under `.canary/security/`. A source
change during the scan makes the run incomplete. Arm64 and Windows can be
selected explicitly.

The runner requires Go, npm and govulncheck. It uses workspace tools when present;
an isolated install of the pinned scanner is:

```sh
GOBIN="$PWD/.canary/tools/security" go install golang.org/x/vuln/cmd/govulncheck@v1.9.0
python3 scripts/security-check.py --govulncheck .canary/tools/security/govulncheck
```

The Go project describes [govulncheck and its reachability analysis](https://go.dev/doc/security/vuln/).
A successful source-symbol scan means it found no known reachable vulnerable
functions in that source/target/database combination. It does not certify
unknown vulnerabilities, deployment configuration or credentials. Module-only
findings are retained alongside the result.

The locally patched JSONata evaluator is part of the main module, so its
upstream module identity is not visible to govulncheck's dependency matching.
The runner therefore also queries OSV for the recorded upstream Go module
version and Git commit. It retains all bounded response pages and fails on
active advisories, malformed/incomplete evidence, or lookup errors. This is
advisory matching, not symbol reachability or proof that the local patch fixes
an upstream advisory. The [official OSV query API](https://google.github.io/osv.dev/post-v1-query/)
defines both query forms and pagination. Offline gate controls are available as
`python scripts/security-check-test.py`.

## Integration checkpoint, 2026-10-22

The initial npm audit reported zero advisories in 84 resolved dependencies.
govulncheck v1.9.0, Go 1.27.3 and the official database last modified
2026-10-21 completed a Windows source-symbol scan with exit 0. Its only finding
was module-level [GO-2026-5932](https://pkg.go.dev/vuln/GO-2026-5932), concerning
the deprecated `golang.org/x/crypto/openpgp` packages. The application does not
import those packages; the finding contained no imported-package or reachable
function trace. Upgrading x/crypto does not remove this all-version advisory.

Raw checkpoint output is `.canary/npm-audit-current.json` and
`.canary/govulncheck-current.json`. This scan occurred during development;
subsequent protocol/service changes and the Linux target require a new coherent
run. The final release also needs locked builds, native race tests, actual
Alpine execution and the application-specific authorization, redaction,
notification and peer-protocol proofs.

## Linux protocol 8 development checkpoint, 2026-10-22 17:12 UTC

`python scripts/security-check.py --goos linux` completed with exit 0 using
govulncheck v1.9.0, Go 1.27.3, Linux amd64 and the database last modified
2026-10-21. Source fingerprints remained unchanged throughout the run. npm
reported zero advisories; Go reported no reachable vulnerable symbols. The
same module-only OpenPGP advisory above remains recorded, without an imported
package or reachable function trace.

Exact source fingerprints, scanner output and commands are retained under
`.canary/security/20261022T171244475047Z/`. This checkpoint includes the expanded
service engines and protocol 8 backend; subsequent code changes require a fresh
scan. It does not replace the independent causal review, race/process tests or
Alpine container validation.

## Working protocol 10 checkpoint, 2026-10-22 19:58 UTC

Current-source fingerprints remained unchanged during the scan in
`.canary/security/20261022T195805730388Z/`. npm reported zero advisories;
Linux amd64 govulncheck exited 0 with no reachable finding. The module-only
OpenPGP advisory remains retained. OSV returned no matching advisories for
either the vendored evaluator's upstream version or its recorded commit.
The three offline pagination/invalid-evidence gate controls pass.

This includes the finite-profile source and badge backend at that timestamp,
not subsequent independent-review fixtures, visual changes, or final release
artifacts. Separate review, race/process checks, and Alpine validation remain
required for the combined source.

## Private evaluator integration preflight, 2026-10-22 22:03 UTC

The isolated integration at
`.canary/jsonata-integration-preflight/20261022T215945453878Z/source` builds with
normal application module selection and no replacement directives. It keeps
`x/crypto v0.58.0` and selects the already recorded 2026 pprof dependency. The
complete ordinary canary-package run passed 815 test/subtest events in 138.360
seconds, with two expected Windows skips. Shared application modules remained
unchanged; this is not production adoption.

The scan in `.canary/security/20261022T220350116042Z/` found zero reachable Go
symbols and zero npm-lockfile advisories. It retains the module-only OpenPGP
finding. That run used the previous scanner: its upstream-identity check covers
the retained Go-port provenance, not the embedded JavaScript package, and its
source fingerprint did not include JavaScript assets. The preflight's separate
complete manifest verified those assets, but this does not substitute for an
embedded-package advisory check.

The scanner now fingerprints embedded JavaScript and requires matching bundle
hash provenance before querying both its npm version and original commit.
Missing or mismatched embedded provenance fails the gate. All five offline
controls pass, including asset mutation detection and npm/commit query identity.
Any adoption must supply that provenance and rerun the new gate against the
combined source. Semantic review and actual Alpine results remain separate.

The notice generator now also requires embedded JSONata version/commit metadata,
the exact bundle SHA-256, a retained license and a patch notice. Its three
offline controls pass: absent assets need no entry; incomplete attribution or
changed code fails; malformed identities cannot produce a plausible entry.
The current 41 notice sets were regenerated and `--check` passes. No embedded
JavaScript runtime is present in the application source yet, so this is a gate
for prospective integration, not evidence of candidate adoption or clearance.
