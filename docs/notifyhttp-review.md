# notifyhttp: first independent correctness review

Reviewed the frozen `apps/node-go/internal/notifyhttp` leaf only, on
2026-10-22 (workspace date). Recommendation: address the two acknowledgement
findings before integration and the lower-priority strict-PEM finding.
These are source-derived counterexamples, **not executed reproductions**.
No build, test, vet, browser, VM, download command, provider request, child agent,
fixture edit, or production edit was performed. This report is the only write.

## Freeze and evidence

Read root `AGENTS.md` and `docs/development-storage.md`. Read-only SHA256/length
checks matched all nine entries in the manifest, plus the supplied manifest
SHA256:

`7cf06a1b4598e5a276046d353ef2d569fd17e2f7533ba49ce8c4c02bc5490de3`.

The ten leaf files, including the manifest, are the supplied 111,444-byte freeze.
The following production-file hashes identify the reviewed branches:

| File | SHA256 |
| --- | --- |
| `config.go` | `b8a30e6d4e637c642686cf6976589c4e55732eadf104dc3f6021ce25f8a5890c` |
| `engine.go` | `d47c89ad651544ea1886c1af46851e5b403edef48e85880bb95a7e3b98d76005` |
| `json.go` | `deb150595da07c97a4310a19f7cb7e0cc8641005a61d8ea0ed8993ae55742103` |
| `providers.go` | `679870b5dcdcdf2482a507f558b649719798107c80df8538d9f7b1e435d8d0c9` |
| `types.go` | `1bbd268564e8bd2c188a7e8557d03490d909320202aa5cb402d9a442ad552edc` |

Root-reported evidence remains: Windows/amd64, Go 1.27.3, CGO=0, 21 native
fixture groups PASS (1.105 seconds); vet and Linux amd64/arm64 CGO=0 library
compilation PASS. I inspected the tests but did not rerun or independently
authenticate their execution records. No Alpine runtime, integration, or live
provider validation is established by this review. The active guarded Alpine
command's eventual result is outside this report.

## Findings

### F1 — P2: Gotify accepts an incomplete, non-native success object

`providers.go:188-195` accepts any HTTP-200 object with a positive integer `id`
and no listed error keys. With the existing valid Gotify fixture configuration,
the complete body `{"id":1}` reaches `accepted(r, "1")`: status accepted,
Accepted=true, ProviderID=SHA256("1"). Even
`{"id":1,"appid":"wrong-type","message":null,"date":null}` takes that branch.

The official POST `/message` contract returns a Message whose required fields
are `id`, `appid`, `message`, and `date`, with integer/string/date-time types.
The server returns the newly created MessageExternal after persistence.
An arbitrary ID object or malformed partial Message therefore becomes a false
positive acknowledgement, which root could persist and use to stop delivery
attempts. No real-provider occurrence is claimed. The leaf README's positive-ID
profile documents this implementation but does not establish a native creation
acknowledgement for the counterexamples. See the [official schema](https://raw.githubusercontent.com/gotify/server/master/docs/spec.json)
and [creation implementation](https://raw.githubusercontent.com/gotify/server/master/api/message.go).

Suggested correction: require a structurally valid native creation receipt,
including the required fields and their types, before acceptance. The opaque app
token prevents comparing `appid` with a configured application ID; do not invent
that comparison or expand this into a new application-discovery API. Comparing
echoed title/text is a separate contract decision, not required by this finding.

Minimum new root fixture: HTTP 200 with each of the two exact bodies above must
return unknown, Accepted=false, empty ProviderID, and exactly one request. Keep
the existing complete Gotify success fixture and its expected acceptance.

### F2 — P2: a permitted Telegram success description becomes unknown

`providers.go:154` treats the presence of `description` as contradictory even
when `ok` is true and the message/chat/thread receipt is otherwise correct.
Telegram's general response contract permits an optional string description of
the result; it is not exclusively an error discriminator. See [Making requests](https://core.telegram.org/bots/api#making-requests).

Exact counterexample, HTTP 200 with the existing numeric-chat fixture:

```json
{"ok":true,"description":"Message sent","result":{"message_id":123,"chat":{"id":-1000123456789,"type":"supergroup"}}}
```

Source path: strict parsing succeeds; classifyHTTP produces unknown; the
description check returns that result before receipt matching. This changes a
provider acknowledgement into uncertainty, potentially causing duplicate sends
under a root policy that retries unknown outcomes. Actual retry behavior is not
integrated or exercised here.

Suggested correction: allow a well-typed success description while retaining
`ok`, destination/thread matching, duplicate/case guards, and genuinely
contradictory error-field checks.

Minimum new root fixture: the exact body above must accept once, HTTPStatus=200,
ProviderID equal to the existing success digest for chat/message 123, zero
RetryAfter, and no error strings. Preserve existing explicit-error and mismatched
chat/thread regressions. A non-string description can remain unknown.

### F3 — P3: PEM decoding skips malformed material after the prefix check

`engine.go:45-60` checks that remaining bytes start with a certificate BEGIN
marker, then lets `pem.Decode` find the next decodable block. The owned Go
1.27.3 source at `.canary/tools/go/src/encoding/pem/pem.go:89` explicitly searches
past malformed blocks, including failed base64 decoding; it does not guarantee
that the returned certificate began at the checked marker.

Exact proposed input: a valid CA from existing `privateCertificate(t, false)`,
prefixed by these literal bytes:

```text
-----BEGIN CERTIFICATE-----\n!\n-----END CERTIFICATE-----\n
```

Here `\n` denotes an actual LF when root constructs the bytes. The prefix passes,
Decode skips the invalid block, the following CA passes X.509 checks, and New
succeeds. An unterminated certificate BEGIN followed by a valid CA is another
case. This violates the documented rejection of garbage/malformed trust input.
It does not install OS trust, accept a non-CA, or bypass TLS verification; the
scope is strict process-option validation.

Suggested correction: delimit and decode each certificate block without allowing
the PEM search to skip any non-whitespace material.

Minimum new root fixture: append the existing generated CA to each malformed
prefix; New must return a nil engine and the fixed errOptions. A valid CA-only
control must still construct successfully. This needs no network I/O. Keep
existing invalid/leaf/oversize/33-certificate and additive-trust regressions.

## Other reviewed boundaries and focused remaining evidence

No additional concrete source defect was established in these inspected paths:

- One timeout context/client deadline covers connection queue, DNS/dial, TLS,
  writing and bounded reading. Parent cancellation wins. Bodies close on every
  obtained-response branch. I/O tests cover header/body stalls, TLS stalls,
  parent cancellation, truncation, and ambiguous connection loss.
- Owned transport has verified TLS 1.2+, additive CA trust, explicit HTTP opt-in,
  no proxy/cookies/redirect following, no compression, disabled HTTP/2 and
  keep-alives, and nil GetBody. Local Go transport source confirms fresh
  HTTP/1 connections do not take the stale-connection request-retry path.
  There is no internal retry loop. Unknown is not proof of non-acceptance.
- Request/response and header/trailer limits, bounded strict JSON parsing,
  integer receipt parsing, fixed safe errors, SHA256 receipts, and retry-hint
  saturation are present. Known HTTP errors survive body failures in the leaf's
  obtained-response path. Accepted results clear retry hints.
- Slack requires exact `ok`; ntfy binds message-event/topic and a 12-character
  ID; Telegram binds numeric chat or alias username and configured thread;
  Pushover requires status 1 plus a UUID-shaped request token and no error keys.
  These checks are provider acknowledgement only. The 12-character ntfy ID is
  supported by [official v2.14.0 source](https://raw.githubusercontent.com/binwiederhier/ntfy/v2.14.0/server/types.go);
  shorter illustrative IDs in current prose examples alone are not a defect.
- Validate/Identity are pure. Kind/pointer agreement, strict key names,
  foreign/null provider rejection, URL validation, provider option bounds and
  message limits are enforced locally. Response case/duplicate guards and
  programmatic validation were inspected, including identity tuple boundaries.
- Identity excludes authentication/presentation changes and includes documented
  routing fields. Gotify's app-token-rotation assumption requires root's
  configuration resource ID binding. Pushover UserKey changes identity; AppToken
  does not. Slack strips only the recognized canonical credential segment and
  retains arbitrary custom routes. None of those stated design choices is a
  finding by itself.

Beyond the three regressions above, one useful additional bounded transport
fixture is cancellation while waiting for MaxConnsPerHost: hold two loopback
requests at the server using channels, issue a third with a short parent
deadline, require unknown promptly with no third handler call, then release the
first two and join all fixture goroutines. This tests the currently unexercised
connection-queue phase without files, sleeps, remote services, or large output.

Provider caveat for root's destination semantics: Pushover can broadcast to all
active devices when a specified single-user device is invalid, and can ignore a
device selector for non-Team groups. Success is still native queue acceptance;
the API does not prove exact device delivery. Document this when integrating
device selection; do not claim that a success receipt verifies that device.
See [Users, Groups, and Devices](https://pushover.net/api#users).

Root alone should create and run any new fixtures after the active fingerprinted
command has ended, using `scripts/bounded_validation.py`, sequentially with the
owned cache and storage limits. This review leaves the original frozen fixtures
and expectations intact. It does not authorize source changes during that command.

Root still owns persisted per-destination fanout, retry/unknown policy, holds,
configuration-resource binding, test-send RBAC, redaction, API/HA integration and
platform validation. The near-Kuma independent Go/SQLite HA monitoring goal
remains unchanged. No SMS/full-option parity or full release validation is
claimed by the leaf or this review.

## Root execution and repair response

The report above remains the independent source review of the original freeze.
Root subsequently added `acknowledgement_review_test.go` without changing any
original fixture. The first guarded focused run failed in 0.676 s (25.031 s
including compilation/guard): all four incomplete Gotify receipts, both valid
Telegram descriptions, and the first malformed PEM prefix reproduced the
findings. Its unchanged Go/JS/JSON source fingerprint was
`587ebf46d4af1eda37bb044771864a868f2195e2bbef6267e0dcd787f8867c65`.

Root repaired `providers.go` and `engine.go`: native Gotify acceptance requires
message/application IDs, string message and RFC3339 date; Telegram permits an
optional string success description; each certificate block is isolated before
PEM decoding, including rejecting a nested BEGIN that could hide an unfinished
prefix. Receipt hashes, destination/thread matching, contradictory error checks,
TLS requirements, request limits and retry behavior are preserved.

The complete native suite, now 25 top-level groups, passed in 1.246 s (9.969 s
guarded). Targeted vet passed with no output (15.625 s guarded). The fingerprint
before/after both was
`215d0bb84b1bba7580aa54aabdae48b3eb55a4ea4c1ac15e7cda33f0c6c9cfc5`.
The malformed and unterminated PEM prefixes both reject; valid CA construction
and the original native success fixtures pass. The new connection-queue fixture
occupies both slots, proves a third request expires without reaching a handler,
then observes both original requests accepted after releasing the fixture.

These are root-run Windows results, not a second independent review or current
Linux/runtime/integration certification. All commands used the bounded runner;
scratch and module-cache growth were zero afterward, with no storage pause.
