# Changelog

connect-matrix is new software. There are no tagged releases or compatibility guarantees yet.

## Unreleased

### Added

- `connect-matrix run`: per-pair proof of mint, accepted, wrong-audience refusal, revoke and rejection within a bound, as a table and optional JSON report that never contains a token.
- `connect-matrix mock-issuer`: JWKS, deny-list, mint and revoke endpoints for proving targets with no real issuer.
- An `http` issuer adapter, a placeholder shape until Tether's service-token command exists.
