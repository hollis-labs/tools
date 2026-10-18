# Security

Caster is pre-release software. Security fixes target the current main
branch; there are no supported release lines yet.

Report suspected vulnerabilities through [GitHub private vulnerability
reporting](https://github.com/hollis-labs/tools/security/advisories/new).
Include the affected tool, revision and a minimal reproduction with synthetic
data. Do not put credentials, private notes or real command output in a public issue.

Caster trusts the local operating-system user and authenticated devices.
It does not isolate tenants or provide per-namespace permissions. The
`caster.resolve` scope can read anything the home node's configured resolvers
can access. Stored blobs are not encrypted by Caster and have no deletion or
garbage-collection API. Keep credentials out of notes.

Use loopback or a trusted private network with HTTPS. See [access](docs/access.md)
and [home-node setup](docs/home-node.md) for pairing, token storage and revocation.
