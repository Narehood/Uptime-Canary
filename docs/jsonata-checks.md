# JSONata conditions

Working protocol 10 adds optional `jsonata` conditions to HTTP, MQTT, SNMP, and
MongoDB monitor options. These additions are outside the frozen protocol-9
validation. Final worker reliability, compatibility review, dashboard integration,
and current-artifact platform checks remain required.

```json
{
  "expression": "$sum(items.value)",
  "operator": ">=",
  "expected": 5
}
```

The supported operators are `==`, `!=`, `>`, `>=`, `<`, `<=`, and `contains`.
The expected value must be a JSON string, finite number, or boolean. Expressions
must return a non-null scalar. Missing values, containers, compile failures, and
evaluation failures do not satisfy a condition, including `!=`.

| Monitor | Input to the expression |
| --- | --- |
| HTTP | Whole response body after accepted status and existing keyword/JSON conditions |
| MQTT | A received publication satisfying the existing scalar condition |
| SNMP | The scalar value from the authenticated, correlated GET response |
| MongoDB | The unique first document from the configured bounded collection query |

Valid JSON retains its structure. Other UTF-8 payloads become JSON strings;
invalid UTF-8 is rejected. MongoDB uses relaxed Extended JSON, so BSON-specific
types may appear as objects rather than ordinary scalar values. MongoDB JSONata
requires a collection; this feature does not add arbitrary database commands.
Existing conditions remain additional requirements.

MQTT keeps waiting through nonmatching publications within the original check
deadline, just as its scalar matcher does. Query syntax/evaluation failures also
continue waiting because the transport receives the same fixed failure outcome;
there is no parent-side compilation to distinguish them. Unavailable worker
admission returns `pending` immediately. Packet-count bounds still apply.

Expression compilation and evaluation occur only in the guarded child process,
after resource controls are installed. Configuration validation checks shape
and size without compiling. The worker has a 128 MiB kernel memory limit, a
64 MiB soft heap target, `GOMAXPROCS=1`, and shared admission of two
workers. Parsing and evaluation consume the check deadline. No in-process
fallback runs when worker admission or platform controls are unavailable; that
outcome is `pending`, rather than a claim that the monitored service failed.

Expressions are limited to 4,096 UTF-8 bytes; input and encoded output are
limited to 1 MiB. Additional evaluator depth, range, and time checks supplement
the process boundary. These advisory checks do not cover every native builtin;
the child memory limit and kill/reap deadline remain necessary.

This is a pinned Go implementation, not complete JavaScript JSONata parity.
Compatibility tests, unsupported language constructs, numeric behavior, and
remaining limits are documented in the [leaf README](../apps/node-go/internal/jsonquery/README.md)
and [numeric compatibility report](../apps/node-go/internal/jsonquery/NUMERIC_COMPATIBILITY.md).
Time and random builtins are not deterministic cluster agreement inputs.

Expressions and expectations can contain private information. Viewer HTTP
responses omit the JSONata object, and viewer service responses omit service
options. Authorized configuration replication and SQLite persistence retain the
configured values. Check messages use fixed diagnostics rather than returning
expressions, documents, or evaluator errors.
