# Contributing

1. **Branch** from `main`: `<type>/<short-slug>` with type `feat`, `fix`, `docs` or `chore`.
2. **Change one thing** per branch, in one tool directory where possible. Each Go tool is its own module; run its tests from its directory (`go vet ./... && go test ./...`).
3. **Run the repository guards**: `scripts/check-layout` and `scripts/scan-public`. A new tool or import follows the layout in `README.md`.
4. **Push and open a pull request** against `main`. Say what the change does, what it leaves alone, and the commands you ran with their results.

Commit subjects use the conventional-commit shape: a type, an optional scope, a colon, then the summary.

Releases are per module: write the `## vX.Y.Z` section in `<name>/CHANGELOG.md`, then `scripts/release <name> vX.Y.Z`. See `../README.md`.

By contributing you agree your work is licensed under the MIT License in `LICENSE`.
