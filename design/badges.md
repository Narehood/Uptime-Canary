# Status badges

Implemented in **working protocol 11** (development, not released). Every node serves a badge for each published status page at `/public/badge/<slug>.svg`, without an account, cookie or token. The route contract (selection, headers, 404 rules) is [docs/badges.md](../docs/badges.md); this file records the visual contract and the dashboard's share links. Preview, drawn from the renderer's own output: [`badges.svg`](badges.svg).

![Six status badges at actual size: up, down, pending and unknown, then down with a maintenance segment and up with a maintenance segment.](badges.svg)

## Content rules
- A badge describes **one published status-page selection**: the whole page (`/public/badge/<slug>.svg`) or one service picked by its **public section ID and public name** (`?group=<section ID>&monitor=<public name>`, exactly those two parameters). It reads the same public projection as the page, so anything a badge says is already public. Private monitor IDs, names and targets are never accepted or shown.
- States are exactly `up`, `down`, `pending`, `unknown`. Stale, missing, paused, conflicting or unpublished data is `unknown` (or the page's "not found"), never `up`.
- Maintenance is an **extra segment** beside the measured state. It never replaces or recolors it: a failure during planned work reads "down · maintenance".
- No percentages. Observed-time availability exists only inside the dashboard, from one node, and is not published.
- Two services with the same public name in the same section are ambiguous, so neither gets a badge (404). Drafts and conflicting pages have no badges.

## Visual rules (renderer)
- 20 px high, 11 px semibold system sans. Label segment 58 px: ink `#151514`, the canary mark and the word "status" in `#f0efec`.
- State segment with a **shape and a word** in white: up = check in a filled circle on `#0b7a0b` (40 px); down = cross in a rounded square on `#b02f2f` (55 px); pending = clock ring on `#5c5b57` (69 px); unknown = dashed ring with "?" on `#5c5b57` (74 px). Maintenance adds a 90 px segment: wrench and ink text on `#fab219`.
- Contrast (WCAG): white on `#0b7a0b` 5.5:1, on `#b02f2f` 6.4:1, on `#5c5b57` 6.8:1; ink on `#fab219` 10:1; the label text 15.9:1.
- Only the outer corners are rounded (4 px), drawn as paths, so adjacent segments meet without notches and the state segment stays square where maintenance follows. Every word has a fixed `textLength`, so the layout can't overflow whichever fallback font renders it.
- The root `<svg>` has `role="img"` and `<title>` "status: down, maintenance"; no scripts, links, external references or `url(`. Every byte comes from fixed tokens; any other state renders `unknown`.

## Visual review (2026-10-22)
The first renderer (SHA-256 `0ccd4d63…`, inside the protocol-11 development binary `e6666539…`) matched the palette, glyphs and words, but its segment rectangles rounded all four corners, leaving visible notches at each joint, and its fixed widths left 12–18 px of trailing space after the word. The revised renderer `apps/node-go/internal/canary/badges_svg.go` (freeze SHA-256 `eb2c830a726241aa200e43409d07ee3c4024240a87e21df78c30f95d73fadf53`, same signature) fixes both. It was reviewed at actual 20 px size on white, `#0d1117` and `#f6f8fa` backgrounds and at 4× zoom. A scratch copy (package clause changed only) passes the conditions of the node's renderer test for all four states and an untrusted state, each with and without maintenance (10/10), and is `gofmt`-clean. It is included in the protocol-12 development binary `78a390ec…`, where the backend owner's badge, status-page and public-route tests pass and the dashboard's badge proof passes 23/23 with it.

## Sharing in the dashboard
- **Status pages → Share** and the editor's **Share** card list badge addresses for the *saved, published* page: the whole page, then each service whose section ID and public name pick exactly one entry. Duplicates are listed with the reason and no link. Drafts say "Publish the page to get badge links".
- **Served by** picks the node address that serves the image (the endpoint or a peer's address). Loopback, private-network and plain-HTTP addresses get a caveat, because sites that fetch images for you can't reach them or may refuse them. An image shows that one node's view; it doesn't switch nodes like the page.
- Each row has a live preview and copies **Markdown**, **HTML** (whole page) or the **image URL**. The Markdown and HTML link to the public page; names and addresses are escaped.
- The copy explains that changing the page address, a section ID or a public name, or unpublishing, breaks existing embeds (the node answers 404), and that renaming a *section* keeps its ID and its badges. The editor warns when unsaved changes would change or remove a current address.
- The public page has an **Embed a status badge** panel with the same rows, served by the node that answered. Its previews load only while the panel is open.

Browser proof (protocol-11 development binary `e6666539…` with the earlier renderer, and protocol-12 development binary `78a390ec…` with this one; private fixtures): unauthenticated GET and HEAD return the page's and each service's public state, including down-with-maintenance and paused-as-unknown; duplicate names, drafts, extra parameters and private names return 404; previews load at 20 px with no `Authorization` header or cookie; the copied Markdown, HTML and image URL are exact.
