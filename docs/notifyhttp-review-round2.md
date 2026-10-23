# notifyhttp: second independent source review

Reviewed on 2026-10-22 (workspace date), in a fresh round. Read AGENTS.md first,
then development-storage.md, the first review, leaf sources/fixtures, and
relevant primary contracts and owned Go source.

**Conclusion:** the three original sets of counterexamples are repaired in
source. One P3 remainder prevents fully closing Gotify's strict receipt
validation: its new date parser accepts non-RFC3339 strings. No additional
P1/P2 defect was established in the inspected paths. Resolve R2-F1 before
calling that strict boundary complete. This is a component source review.

## R2-F1 — P3: Gotify still accepts non-RFC3339 receipt dates

Location: apps/node-go/internal/notifyhttp/providers.go:198-203.

Exact source-derived counterexample: return HTTP 200 and this entire body to
the existing valid Gotify fixture configuration, sending Message.Text "fixture":

```json
{"id":1,"appid":4,"message":"fixture","date":"2026-10-22T0:00:00Z"}
```

The IDs and message type pass. time.Parse(time.RFC3339Nano, ...) also succeeds
on this single-digit hour under the owned Go source, so acknowledgement calls
accepted(r, "1"): Status=accepted, Accepted=true, HTTPStatus=200,
ProviderID=SHA256("1"), with errors/retry hints cleared.
This is an inferred execution path, **not an executed reproduction**.

The [Gotify schema](https://raw.githubusercontent.com/gotify/server/master/docs/spec.json)
requires a date-time string; the leaf README promises an RFC3339 date.
[RFC3339 section 5.6](https://www.rfc-editor.org/rfc/rfc3339#section-5.6)
requires two-digit hours. The owned toolchain explains the mismatch:

- .canary/tools/go/src/time/format.go:1026-1033 tries the RFC3339 fast path,
  then falls back to the general layout parser.
- format.go:1145-1148 uses getnum(value, false) for the hour;
  format.go:926-936 permits one digit when fixed=false.
- .canary/tools/go/src/time/format_rfc3339.go:163-181 acknowledges that the
  layout parser cannot strictly validate RFC3339; its additional strict checks
  are disabled. Switching to Time.UnmarshalJSON alone also uses this disabled
  strict path.

Two further source-derived date variants reach the same acceptance branch:
2026-10-22T00:00:00,5Z and 2026-10-22T00:00:00+24:00.
The general parser permits comma fractions (format.go:1316-1328) and a 24-hour
offset (format.go:1270-1274); RFC3339 permits neither. These are one finding.

Impact is limited to malformed acknowledgement validation. This timestamp
does not route credentials or control trust/retries, and no live Gotify
occurrence is claimed. A malformed response can nevertheless acquire the
native-acceptance authority root will later persist.

Minimal proposed root fixture: use complete Gotify bodies with each of those
three dates; require Unknown, Accepted=false, HTTPStatus=200, empty ProviderID
and exactly one loopback request. Preserve accepted controls for
2026-10-22T00:00:00Z, 2026-10-22T00:00:00.000Z, and the schema's
2018-02-27T19:36:10.5045044+01:00 example. Add a bounded syntax/range gate before
the existing parse. Simple parse/format equality would reject valid fractional
trailing zeros. Root alone should author/run fixtures and repair after its
active command; this report makes no fixture/production edit.

## Disposition of all three original findings

**F1 / P2, incomplete Gotify receipt: original counterexamples resolved;
strict date validation retains R2-F1 / P3.**
providers.go:195-203 retains contradictory-error gates and now requires positive
integer id/appid, a string message and a parsable date. Neither the original
id-only body nor the wrong-type/null body can accept. The four new Gotify cases
cover those exact bodies, an invalid date and zero appid. The unchanged complete
native success control remains structurally admissible.
The [creation source](https://raw.githubusercontent.com/gotify/server/master/api/message.go)
returns the persisted MessageExternal, supporting those required fields.
No AppToken/appid comparison has been invented; echoed title/text equality is
outside the accepted correction.

**F2 / P2, optional Telegram success description: resolved in source.**
providers.go:155-185 permits an absent or string description, including empty,
and rejects non-string descriptions. Contradictory error fields/parameters,
positive message ID, numeric chat/alias match and configured thread checks
still gate acceptance. Hashing still binds chat/message.
The [Bot API response contract](https://core.telegram.org/bots/api#making-requests)
permits the optional string independently of success/error. The added fixture
includes valid strings and null/number/object rejections; original error/chat/
thread fixtures are unchanged.

**F3 / P3, malformed/unterminated PEM prefix skipped: resolved in source.**
engine.go:45-75 isolates bytes through the first certificate END marker,
requires exactly one canonical certificate BEGIN in that slice, decodes only
that slice, and rejects leftover bytes, headers and non-CA certificates.
The invalid-base64 prefix cannot decode in isolation; an unterminated prefix
followed by a valid CA contains two BEGIN markers and rejects. The owned
encoding/pem/pem.go:89-188 search behavior cannot conceal either original case.
Remaining bytes must begin another certificate after whitespace trimming.
The 256 KiB/32-CA caps, additive SystemCertPool, CA constraints and verified TLS
1.2 minimum remain. New fixtures preserve both prefixes and a valid CA control.

## Nearby regression assessment

No additional concrete production defect was established in these boundaries:

- Validate/Identity remain pure; exactly one matching provider pointer is
  required. Config decoding retains exact keys, duplicate/case guards and
  foreign/null-provider rejection. Config/types/request wiring did not change.
- Slack identity strips only the recognized canonical services/T/B credential
  segment; custom routes keep their final segment. Telegram includes chat/
  thread, ntfy topic, and Pushover user-key digest/device. Gotify intentionally
  excludes AppToken: root MUST bind configuration resource ID with Identity
  before sharing persisted receipts or holds.
- One context/client deadline bounds queue, dial, TLS, write and body. Response
  reads remain bounded, require EOF and close obtained bodies. Known HTTP
  errors remain authoritative through body failures.
- TLS verification/additive trust, explicit HTTP opt-in, no proxy/jar,
  refused redirects, disabled compression/HTTP2, fresh connections and nil
  GetBody remain. There is no internal retry loop or idempotency header.
  Owned net/http/transport.go:844-871 refuses stale-connection retries on
  fresh HTTP/1 connections.
- Exact Slack acknowledgement, ntfy event/topic/ID, Telegram recipient/thread,
  Pushover status/request/error gates, bounded strict JSON/int64 parsing, fixed
  errors and hashed receipts remain. Retry hints stay bounded to 24 hours and
  acceptance clears them.

The new queue fixture occupies both slots before its third send, checks parent
expiry with no third handler and HTTPStatus=0, releases the handlers and observes
both original results. This provides meaningful source coverage of that phase;
I did not execute it.

A coverage limitation accompanies Gotify's stronger receipt gate:
engine_test.go:383-399 builds JSON depth/array/object/node/UTF8/key-limit bodies
around only id plus extra data. Several now remain Unknown even if their
intended JSON guard regresses, because appid/message/date are absent.
This is a coverage observation, not a production finding. Preserve those
original fixtures. Additional cases can start with otherwise complete
goodReply(Gotify), with an accepted within-limit control and over-limit Unknown
cases, or directly check strictObject errors. The current parser still contains
the documented limits.

## Fresh hashes and unchanged original fixtures

Read-only SHA256 checks matched the supplied repairs:

```text
providers.go (6,956 bytes)
78eab01aad3a7b66d49943b6ead420896c1b96d494763d9c39f6d83e1feeb92d
engine.go (9,353 bytes)
9f8c00c23d024512716b873c29a1d4d44140a31ceb07ce41aca62773423add92
acknowledgement_review_test.go (4,610 bytes)
c64e4506416773a01b2576cf74590408123bce7b3a352126fd3300d24f81a12a
```

At the start, the historical SOURCE_FREEZE.json still hashed to
7cf06a1b4598e5a276046d353ef2d569fd17e2f7533ba49ce8c4c02bc5490de3.
All three original fixture hashes AND lengths matched its entries; a later
read confirmed the same values:

```text
config_test.go (15,427 bytes)
9e0d24326923d251d838d03ee40e5045997f3a41613beec5389cbb89c9ef46f1
engine_test.go (15,541 bytes)
0a44870a75cae996c5ca05e34103affc0a7b914e347aae975deb93c2cc701d75
providers_test.go (18,907 bytes)
07761aa9268a83441bab36733e292d68b66adbe7738aa891552f95ebe7dc930c
```

Config/types/json also matched the old manifest:

```text
config.go
b8a30e6d4e637c642686cf6976589c4e55732eadf104dc3f6021ce25f8a5890c
types.go
1bbd268564e8bd2c188a7e8557d03490d909320202aa5cb402d9a442ad552edc
json.go
deb150595da07c97a4310a19f7cb7e0cc8641005a61d8ea0ed8993ae55742103
```

Root refreshed the manifest/README during review. Later observed hashes:

```text
SOURCE_FREEZE.json (6,894 bytes)
a2a519cc2219eb618a7438f45707c8b32966e5a6de3fb446e4f236e6976c6893
README.md (19,593 bytes)
3fdbafb1dbb9fd1c4b2c4529dd2f808f76cc103df2ae78c636c9595060de6cf5
```

The version-2 manifest retains the original freeze as historical and labels
round two pending. I did not change either file. These hashes identify observed
snapshots; root may subsequently update documentation/manifest status.

## Execution provenance and limits

Root reports the initial focused regression run FAILED in 0.676 s, followed by
25 Windows/amd64 CGO=0 groups PASS in 1.246 s (9.969 s guarded), vet PASS in
15.625 s guarded, and Linux/amd64 plus Linux/arm64 CGO=0 library builds PASS in
30.500 s guarded. The refreshed manifest records those results, zero remaining
scratch/module-cache growth and no storage pause. Its pre-refresh unchanged
Go/JS/JSON command fingerprint is:

215d0bb84b1bba7580aa54aabdae48b3eb55a4ea4c1ac15e7cda33f0c6c9cfc5

Those are **root-reported execution results**. I inspected source, hashes,
reports/manifest and primary documents without rerunning or independently
authenticating execution. The reported date fixtures do not cover R2-F1.
Linux library compilation establishes no runtime behavior. No API/HA/UI/
live-provider/Alpine integration is established; the different-package Alpine
R3 gate is outside this review.

Root retains durable per-destination attempts/fanout, unknown/retry policy,
holds, configuration-resource binding, RBAC, redaction, integration and release
validation. Acceptance never proves device delivery or exactly once. The
independent Go/SQLite HA near-Kuma goal remains unchanged; no SMS/full-provider-
option parity or release/platform certification is claimed.

This is the review's only authored file, below 12 KiB. No source/fixture edit,
build/test/vet, download, interactive browser, VM, database access, real
notification, child agent or credential request was performed. Root retains
the single validation command/cache and storage budget.


## Root response: strict date counterexamples

Root preserved the review above and reproduced R2-F1 with three invalid receipt
dates: one-digit hour, comma fractional separator, and a +24:00 offset. All
three were incorrectly accepted in the initial 0.655-second focused run. Three
valid controls include a fractional date with trailing zeroes and a seven-digit
fraction with an offset. No original fixture assertion changed.

The repair combines a lexical RFC3339 shape check with Go's calendar parser.
The full 26-group Windows/amd64 leaf suite subsequently passed in 1.384 seconds,
alongside delivery (1.939 seconds) and the full canary package (143.604 seconds).
Targeted vet for all three packages and Linux/amd64 plus Linux/arm64 CGO-disabled
notifyhttp/delivery library compilation passed. This was one root-owned guarded
command; its later frontend typecheck failed independently on an unescaped
Unicode line separator. The enclosing command therefore exited 1; no web build
or pure frontend check ran in that command.

The unchanged apps/node-go Go/JS/JSON input fingerprint for that command was
`7ab622b641f7b2d33928daebab2d60b5c5c68b8f4ac4d09943f4e10e8f2879c7`.
The guard reclaimed all scratch, recorded no module-cache growth, and required
no storage pause. This is root-run evidence, not a new independent review or
Linux runtime/live-provider/release validation.
