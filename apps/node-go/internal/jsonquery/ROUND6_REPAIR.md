# R6 matcher, native partial and length repairs

This round addresses the four mechanisms in `docs/jsonata-matcher-sort-review.md`
and the separately reported Linux maximum-document gate. Its three independent
review files and all 408 earlier JS records/scripts remain byte-identical.
No request/API, shape validation, process code/limits, module metadata, root
integration or outside-package documentation changed. Fresh independent ROUND3
and an actual Alpine maximum-document rerun remain separate root-owned gates.

## Numeric limits and empty input

Matcher limits stay float64 through sign checks and loop comparisons. Positive
0.5 invokes the initial matcher and its required continuation; 1.5 allows two
iterations. Negative fractions fail before any integer conversion. Zero skips
callbacks as in the reference. Literal string splitting retains its distinct
JS ToUint32 limit conversion; it does not use matcher-loop rounding.

Undefined input is checked explicitly. Empty text reaches the matcher in
match/split/replace/contains, preserving required callback errors and valid
empty results. Undefined continues to propagate without evaluating the matcher.

## Retained native calls and context

Native partials now carry a typed record of the original Function header,
bound argument values, placeholder positions, parameter names and input.
They do not parse generated argument source or stop collecting at undefined.
All declared lambda parameters bind even when their argument is undefined, so
missing parameters shadow earlier values. Re-partial application retains its
bound arguments. No closure or captured environment is reflected or inspected.

The partial invokes through the shared finite-result guard. A fixed internal
call pointer on native definition headers routes to the normal apply path,
including its context-aware `$eval` handling. It also avoids a static builtin
initialization cycle. This is interpreter dispatch metadata, not a new DSL
registration, arbitrary binding or configurable callback. Argument wrappers
copy their header/context and never mutate the global function table.

Pinned JS native partials bypass the normal outer signature validator. Applying
that validator unconditionally would introduce wrong finite DOWN outcomes,
such as rejecting numeric text accepted by partial `$power`. The full original
signature remains recorded, while only the temporary native-partial call copy
omits its validator. Ordinary native calls still validate it. The affected
underlying implementations now preserve the tested JS behaviors: contains
rejects an invalid string receiver; sum requires its array and uses primitive
JS addition rather than dropping values; power uses primitive JS numeric
conversion and rejects a nonfinite result. Undefined argument positions are
preserved. No callback error is converted into successful empty output.

Context-aware partial `$eval` also records its actual native parameters `expr`
and `focus`, including bound constants and omitted values. Its frame exposes
those names and no generated placeholder names, and ignores extra bindings
outside the native declaration. Alias/property/lambda/nested-eval, undefined,
coercion, repeated partial, lexical-shadow and context cases are pinned against
JS. Existing approved returned-partial-overflow cases remain bounded DOWN even
where JS succeeds; ordinary finite mismatches were not reclassified as policy.

This is bounded repair of the executed paths. Other retained builtin semantics,
custom coercion methods and opaque callable conversion are not certified as
universally equivalent. No additional user-selected host function, valueOf/
toString callback invocation, global registration or parent evaluation is added.

## Original matcher data and capture positions

MatchResult retains the original map as well as its private slicing fields and
error-aware Next hook. Replacement callbacks receive that original observable
map, including start/end/groups/next and custom fields, without an invented
index. `$match` projects its public index from the original numeric start, so
fractional/custom metadata is not truncated to its private slice index.

Native regex next is exposed as an actual DSL-callable function with the same
per-regex cursor used by continuation consumption. Calling it inside a
replacement callback advances that cursor, as in pinned JS; it does not create
an independent replay. Initial/recursive errors still propagate to every
consumer and fixed public diagnostics.

Every native capture gets a position, including undefined for an unmatched
optional group. Empty captures remain empty strings. The adapter retains
nonstrings/custom group values rather than deleting them. Group projection,
counts, numeric metadata and replacement substitutions keep their positions.
The original map is retained for callbacks rather than reconstructed from a
reduced subset. No opaque value is formatted or called by the data adapters.

## Constant-allocation length count

The retained length implementation previously allocated `stringToArray(str)`
just to count its elements. It now calls a pure helper containing
`utf8.RuneCountInString(str)`. All preceding undefined/type handling is unchanged.
It counts the same codepoints as the old range-based conversion, including
combining characters as separate codepoints; it does not introduce grapheme
counting or shrink the accepted document.

The unchanged one-MiB-document worker test passes on Windows. Pure helper tests
verify ASCII, multibyte, combining, empty and invalid-byte range behavior, plus
zero allocations while counting one MiB. The helper benchmark reports 0 B/op
and 0 allocs/op for all three text classes. These tests perform no engine
compilation/evaluation or host binding in their controller process.

Root's old Alpine/direct and restricted-Docker maximum-document Pending
observations remain failures. Near-limit VmData is a memory-pressure inference;
the exact child exit cause was not established. No 128 MiB kernel/64 MiB soft
limit, timeout or old maximum-document assertion changed. A new actual Alpine
run of the exact frozen source is required before closing that runtime gate.

## Evidence and freeze

The final full leaf run passes 716 test/subtest events: original 505, unchanged
ROUND2's 105 cases plus parent, 97 R6 cases plus parent, and seven count-helper
events. The new JS records separate 73 matching UP, 22 matching DOWN/error and
two approved safe-DOWN outcomes. The 105 embedded review records and all 408
earlier records independently verify offline: 610 JS records total.

The source/provenance freeze is schema version 3. The first R6 freeze was
superseded by the five final native-eval scope probes; its exact changed
tests/reference bytes are retained in a checksummed base64 history record.
All v1/v2 histories and previous failed review artifacts remain preserved.
[SOURCE_FREEZE.json](SOURCE_FREEZE.json) enumerates the final code/tests/data;
[upstream.json](evaluator/upstream.json) records raw/final hashes and the exact
raw-upstream patch. Package prose is excluded from those source hashes.
See [VALIDATION.md](VALIDATION.md) for executed checks and artifact hashes.
