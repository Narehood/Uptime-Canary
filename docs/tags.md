# Reusable tags

Tags are replicated metadata, independent of monitor placement, checking and
notification policy. Administrators and editors manage the catalog and assign
tags; viewers can read it. Tag edits, assignments and deletions do not start a
new check generation or report recovery.

Each tag has a name (up to 128 bytes) and a six-digit hexadecimal color such as
`#aabbcc`. Names are not unique identifiers. The node generates stable IDs and
revision IDs and normalizes colors to lowercase. Arbitrary CSS is not accepted.

The management API uses `/api/tags` for listing and creating. Individual
`/api/tags/ID` routes support GET, revision-checked PUT and DELETE. GET
`/conflicts` returns candidate tags; POST `/resolve` accepts name, color and
`revisions` naming every observed head exactly once. Conflicting or incomplete
catalog entries do not choose an arbitrary public label.

Monitor input optionally contains:

```json
{
  "tags": [
    { "tagId": "CATALOG-TAG-ID", "value": "production", "public": true }
  ]
}
```

At most 32 distinct tags can be assigned to a monitor; values are at most 128
bytes. Local edits require ready catalog references. Peer pages can arrive in
any order; missing, conflicting or deleted tags are omitted from public labels
while monitoring continues. Deleting a catalog tag retains a tombstone and the
monitor's reference, so old peer pages cannot recreate it.

Publication requires both the assignment's `public:true` and the status-page
selection's `showTags:true`. Both default to false. Public JSON contains only
the selected tag's name, color and value, without catalog IDs, revisions or
private tag associations. The publication flags do not grant anonymous access
to catalog management or private monitor configuration.

Headless commands are `tags`, `add-tag FILE`, `update-tag ID FILE`,
`delete-tag ID REVISION`, `tag-conflicts ID` and `resolve-tag ID FILE`.
The ordinary resource capacity and coordinated same-build peer upgrade rules
apply. Catalog UI, assignment editing and filtering are being implemented by
the dashboard designer.

Private fixtures verify CRUD/RBAC, strict colors and assignments, metadata
incident continuity, both publication opt-ins, private-value redaction,
conflicts/resolution, serialized peer transfer, tombstones and restart.
