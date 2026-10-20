# connect-matrix

Proves that one app can call another app's API with a service token, and that revoking the token stops it. For each configured pair it runs:

| check | meaning |
| --- | --- |
| `mint` | the issuer produced a service token for the pair's audience and scopes |
| `accepted` | the target answered the probe request with the expected status |
| `wrong_audience` | a token meant for another audience is refused (401 or 403) |
| `revoke` | the issuer accepted the revocation |
| `revoked` | the target starts refusing the token, and how many seconds that took (must be within `revoke_bound_seconds`, default 45: a 30s deny-list poll plus slack) |

`wrong_audience` and `revoked` only mean something when the target is enforcing. Set `"enforcing": false` for a target that admits anonymous callers and those two checks are skipped; claiming `enforcing` on a target that is not fails with a hint. Tokens and credentials are never printed or written to the JSON report.

```sh
go install github.com/hollis-labs/tools/connect-matrix/cmd/connect-matrix@latest

# 1. a stand-in issuer: JWKS, deny-list, mint and revoke (loopback or tailnet only)
connect-matrix mock-issuer -listen 127.0.0.1:9100 -iss mock -pub-key-out /tmp/mock.pub.pem

# 2. start each target trusting it, for example Torque:
#      HOLLIS_AUTH_TORQUE=enforce TORQUE_AUTH_ISSUER=mock \
#      TORQUE_AUTH_JWKS_URL=http://127.0.0.1:9100/.well-known/jwks.json \
#      TORQUE_AUTH_DENYLIST_URL=http://127.0.0.1:9100/auth/denylist torque serve
#    (the mock does not need to be reachable by the target if you pass
#     TORQUE_AUTH_BOOTSTRAP_KEY_FILE=/tmp/mock.pub.pem, but revocation needs the deny-list URL)

# 3. prove the pairs
connect-matrix run -config examples/pairs.json -json results.json
```

Exit status is 0 when every pair passes, 1 when a pair fails, 2 for a usage or configuration error.

## Issuers

- `mock`: a `connect-matrix mock-issuer` instance. Its service tokens are Ed25519 JWTs with `kind=service` and an `exp` ten years out (the go-auth verifier requires `exp`, so "no expiry" is a distant one), revoked through its deny-list. State is in memory.
- `http`: a real issuer. `mint_url` receives `POST {"name","audience","scopes","kind":"service"}` and answers `{"token","id"}`; `revoke_url` receives `POST {"id"}`; both send `Authorization: Bearer <contents of credential_file>`. These shapes are a placeholder until Tether's service-token issuing command exists; only the issuer adapter changes then, not the pair definitions.

## Config

See `examples/pairs.json`. Unknown fields are rejected. A pair needs `name`, `target`, `audience`, at least one scope and `probe.path`; `probe.method` defaults to GET and `probe.ok_status` to 200. `other_audience` (default `connect-matrix-other`) is the audience of the wrong-audience token.

## Development

`go vet ./... && go test -race ./...` in this directory. The tests run a go-auth guard as the target and the mock issuer as the issuer, so they exercise the real verifier and deny-list poller.
