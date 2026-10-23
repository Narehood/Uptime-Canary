# HTTP OAuth2 client credentials

Working protocol 10 adds `http.oauth2` to HTTP monitors. This feature is outside
the frozen protocol-9 release evidence. Structured dashboard fields and final
integrated validation are still in progress.

Each check obtains a fresh access token using the client-credentials grant,
then sends that token as the monitored endpoint's Bearer authorization. Tokens
are not cached or written into monitor configuration. Token acquisition and the
target request share the check deadline.

| Field | Meaning |
| --- | --- |
| `tokenUrl` | HTTPS token endpoint |
| `clientId` | OAuth client identifier |
| `clientSecret` | Client secret; an empty string is permitted |
| `authMethod` | `client_secret_basic` (default) or `client_secret_post` |
| `scope`, `audience` | Optional grant parameters |
| `tokenCaPem` | Optional CA trust specifically for the token endpoint |
| `allowInsecureHttp` | Explicit opt-in to plaintext token and target URLs |

OAuth2 cannot be combined with Basic credentials, a configured Bearer token,
or a custom Authorization header. The token request does not receive the target's
custom headers, body, client certificate, or custom CA trust. The configured
proxy applies to both requests. Token-endpoint redirects are refused; target
redirects use the existing cross-origin credential-stripping rules.

The issuer must return HTTP 200 with an `application/json` object containing a
Bearer `token_type` and a bounded valid `access_token`. Invalid, duplicate,
oversized, or deeply nested responses fail with a fixed diagnostic. Raw token
responses and access tokens are not returned in check messages.

Administrators can retrieve the configured grant credentials. Viewer responses
omit the entire OAuth2 object. Configuration, including credentials, replicates
to authorized cluster peers and persists in SQLite; protect cluster access and
database files accordingly.

Changing grant settings changes monitoring semantics and starts a new check
generation. Renaming a monitor preserves its generation. Authorization-code,
refresh-token, password, and device grants are not implemented.

The client-auth encoding and grant follow [RFC 6749 sections 2.3.1 and 4.4](https://www.rfc-editor.org/rfc/rfc6749).
Focused tests cover independent TLS trust, target mutual TLS, encoded Basic and
POST credentials, bounded issuer responses, redirects, deadlines, viewer
redaction, peer replication, generation changes, and restart persistence.

A development compiled-process proof also passed in 19.227 s: two connected
nodes obtained real grants from a loopback TLS issuer, evaluated JSONata against
the actual HTTPS response, and sent one down and one recovery webhook. History
contained neither client credentials nor access tokens. Its binary SHA-256 is
`0ec39ebc561abb1e9fa63a7fd51265ea6c9adefaec92aa43c9ef1b4f21a94f32`.
The initial fixture run was rejected because its timeout exceeded its interval;
the corrected fixture uses 3,000 ms for both. This supplementary proof does not
certify subsequent numeric/worker repairs or the final release artifact.
