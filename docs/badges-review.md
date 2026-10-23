# Public badge backend correctness review

2026-10-22: **No correctness or privacy blockers found in the captured backend.**
Ten independent review tests pass alongside the four existing badge tests and
nine existing status-page tests. No production changes are requested.

The reviewed authority chain is `api.go:142` -> `servePublicBadge`
(`badges.go:38`) -> `publicBadge` (`badges.go:13`) -> `publicStatusPage`
(`status_pages.go:487`). Selection uses the public projection's section ID and
display name. Private monitor IDs remain internal to `publicStatusMonitor`
(`status_pages.go:444`), where monitor readiness, current revision, assigned
nodes, freshness and maintenance determine the public state.

The added tests in
[`badges_review_test.go`](../apps/node-go/internal/canary/badges_review_test.go)
establish these concrete results:

- **Published selection:** names and section IDs read from public JSON work,
  including URL-encoded Unicode, punctuation and markup characters. Identical
  names in different sections select their respective monitors. Private IDs,
  private names, unpublished monitors, case changes and extra spaces do not
  select them. A rename retires the old selection. Duplicate names within one
  section return 404 even when one referenced monitor is missing. An operator's
  explicit public name equal to a private ID is accepted as intentional
  publication; that value still never enters the fixed SVG.
- **Evidence and maintenance:** mixed observations preserve the existing
  down/unknown/pending/up precedence. Missing, stale, future-dated or obsolete
  revision observations produce unknown. Maintenance remains a separate marker
  beside down and disappears when its window ends. Missing, disabled,
  unassigned, conflicted, unready or deleted monitors remain unknown on an
  otherwise valid published page.
- **Aggregate scope:** a hidden monitor's outage and maintenance do not change
  a published badge. Maintenance on one published monitor affects the page
  rollup and that monitor's badge, while another monitor remains up without
  maintenance. An empty published page is unknown and has no selectable monitor.
- **HTTP boundary:** GET/HEAD receive wildcard public CORS, no-store and
  nosniff; successful SVGs receive the restrictive CSP. HEAD writes no body
  and has the same application headers as GET. Encoded path aliases, malformed
  encodings, unknown/repeated parameters, private-ID parameters, invalid slugs,
  missing routes and unsupported methods produce static 404 JSON without
  reflecting host, origin, token or request data. A body that panics on Read
  remains unread, including for oversized bodies and unsupported methods.
  Conditional headers do not turn these dynamic responses into 304s. Public
  CORS and unauthenticated authority do not spread into management endpoints.
  A 64-byte valid slug works; a 65-byte slug fails closed.
- **Publication authority:** drafts, deleted/conflicted/unready pages,
  duplicate slugs, draft or unready duplicate claimants, faulted nodes and
  stopped nodes all return the same 404. Neither admin nor cluster credentials
  expand that public authority, for either rollup or selected GET/HEAD requests.
- **Budgets and persistence:** exactly 16,384 current resource heads and
  exactly 16 MiB of scanned status-page JSON still permit publication; one
  extra head or byte returns 404. Successful and exhausted reads leave the
  retained resource payloads/heads, durable records digest and SQLite
  `total_changes()` unchanged. Padding is confined to a synthetic in-memory
  read view and is never admitted or persisted. A separate test holds the only
  SQLite connection while reading down-plus-maintenance badges successfully,
  demonstrating that neither historical nor other SQL reads are required.
- **Restart and peers:** private SQLite restart preserves publication,
  observations and maintenance; elapsed observations become unknown, and a
  deleted page remains unavailable after another restart. An authenticated
  loopback peer exchange publishes the same measured state. Independently
  edited page branches and duplicate slugs fail closed after exchange;
  complete resolution restores the badge and a replicated tombstone removes it.

Successful SVGs are parsed as XML and checked for a single fixed state title,
safe SVG elements, and absence of active attributes, external references,
private fixture markers, certificates, observations, history and percentages.
No geometry, color placement or image snapshot assertions were added.

Validation used the existing pinned `go1.27.3 windows/amd64` toolchain with
`-mod=readonly`. The final frozen-source command was:

```powershell
& 'C:/Users/Michael/Documents/DEV/Uptime-Canary/.canary/tools/go/bin/go.exe' test -mod=readonly ./apps/node-go/internal/canary -run '^(TestBadgeReview|TestPublicBadge|TestStatusBadge|TestStatusPage)' -count=1 -v
```

Result: **PASS, 23 top-level tests, 3.632s**. The same frozen package passed
`go vet -mod=readonly ./apps/node-go/internal/canary`. The workspace review
tests also passed; the final additional aggregate-scope test passed separately.
No whole-application stress or race run was needed.

Before inspection, the original core files were copied and hashed privately at
`C:\Users\Michael\AppData\Local\Temp\uptime-badges-review-20261022T195232Z-cf5abfb4`.
`baseline-sha256.json` records the original eight inputs. A separate
`frozen-workspace` contains 246 captured Go/module files plus the new review
test; the original captures were preserved. `validation-badges-status.txt`
contains the final verbose passing run. Initial core SHA-256 values are:

```text
docs/badges.md
FFB1F7694A449E8D69DEFF3E0F19110F585F52D65EB00810970037F2F068E2E2
apps/node-go/internal/canary/badges.go
9464B5CFCBFE2C5EE452A3F39E25C6CF7995F4BAB385FB23B51E0A893933641D
apps/node-go/internal/canary/badges_svg.go
0CCD4D630253722850C50E01F80EC6938102A5A33F8891CE8F920B312F7C88C8
apps/node-go/internal/canary/status_pages.go
2CEF00037B30BC2CA900F423982CFBEFD23CE885819F2961D91BBF5D9FFBB046
apps/node-go/internal/canary/badges_test.go
9A734AF9A2A3C718AE22B43E02D9AC43F82E431E8A7A982231E8E6CADA9D2C4A
apps/node-go/internal/canary/api.go
36A2E5D8AE3DC6A074F32CB0653948A0DB0409D1DB4D1370E89D979FBC2E6B4D
go.mod
2C95B02CD9F1C89CB56B40ECCBE3FEEB35BC772FD5BD0F7153AD821EA099E77A
go.sum
6321676C3E2A59DF8DF8A425028035A3C03F9B93A135C70FAC74A9515915AD47
```

The final review test SHA-256 is
`1973707D69A813E1ADD6139B6D8B98D556AB6A148C14EA4828894E2E6019B483`.
Core files and module metadata matched their original hashes during validation.
The broader workspace comparison observed an unrelated concurrent change to
`internal/jsonquery/profile_review_test.go`; the captured source remained intact.

This conclusion covers the reviewed Go backend and private fixtures. Opus owns
renderer visual quality. Combined release-artifact validation, Alpine/platform
validation and embedding-proxy refresh behavior remain outside this review.
All fixtures used isolated nodes, temporary SQLite databases, httptest and
loopback peers; the user instance, environment files, existing database and
external network were not used. Only this document and the new review test
were written in the project workspace.
