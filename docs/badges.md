# Published status badges

Development protocol 10 now serves SVG badges from the existing public status
projection. The renderer reproduces Opus's [badge design](../design/badges.svg).
Functional projection/route/XML checks pass; Opus visual review and combined
artifact/platform validation remain pending.

```text
GET /public/badge/operations.svg
GET /public/badge/operations.svg?group=public-services&monitor=Public%20service
```

The first route shows the published page's aggregate state. The second selects
exactly one published monitor by its public section ID and public display name.
Use URL encoding for both query values. If names are duplicated within that
section, the selection fails closed with 404; neither a private monitor ID nor
list order disambiguates it. Changing a published name changes its badge URL.

No credential is required. Credentials on the request do not allow a badge for
an unpublished, disabled, deleted, conflicted, or incomplete status page. The
same public read budgets apply: 16,384 current resource heads and 16 MiB of
status-page JSON per request. Budget exhaustion returns the same static 404 as
a missing publication, without altering retained resources.

Badges show `up`, `down`, `pending`, or `unknown` with both a word and a distinct
shape. Stale or missing observations remain unknown. Maintenance is an extra
segment beside the measured state; it does not turn a failing service green.
No percentage, raw observation, target, private name, node ID, incident text,
certificate, or tag is included in the SVG. It contains no scripts, links,
external fonts, or external references. Label text and colors are fixed.

GET and HEAD responses have public CORS, `no-store`, `nosniff`, and an SVG
content-security policy. HEAD has no body. Other methods, path aliases,
repeated/unrecognized query keys, and malformed encodings return a static JSON
404. The route reads neither a request body nor historical SQL.

The node sends `Cache-Control: no-store`; an embedding site's image proxy may
apply its own refresh policy. A badge is a view from the node serving it, with
the same partition and freshness limits as that node's public status page.
