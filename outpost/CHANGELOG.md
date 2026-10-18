# Changelog

Outpost is new software being built in the open. There are no tagged releases
or compatibility guarantees yet.

## Unreleased

### Added

- Shared per-machine host for independent subprocess plugins, with scoped
  controller pairing, immediate revocation and plugin health supervision.
- Public usage, security and contribution documentation under the MIT license.
- Linux and macOS CI with race tests, vet, lint and vulnerability scanning.

### Changed

- Prepare the independent Go module for `hollis-labs/tools/outpost`.
- Clarify the operator's responsibility for encrypted transport and trusted
  plugins; the listener validates address ranges, not interface ownership.

### Fixed

- Reject trailing configuration data instead of silently ignoring it.
- Refuse using the pairing store itself as the credential output file.
