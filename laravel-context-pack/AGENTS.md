# Repository Guidelines

## Project Structure & Module Organization
The repository hosts project orchestration assets (`delegation/`) alongside the PHP CLI implementation. Keep runtime code under `src/` (commands, support services, future generators) with the executable in `bin/context-pack.php`. Composer metadata (`composer.json`, `composer.lock`) lives at the root, and test suites sit in `tests/`. Generated context packs belong inside a git-ignored `context-pack/` directory. Treat `delegation/agents/` and `delegation/sprint-00x/` as living documentation—update them whenever sprint plans or task packs change.

## Build, Test, and Development Commands
Install dependencies with `composer install`. Run the CLI locally via `php bin/context-pack.php generate --project=/path/to/app`. Lint with `composer lint`, static analyse with `composer analyse`, execute Pest suites with `composer test`, and run Rector in dry-run mode using `composer rector -- --dry-run`. Multi-agent flows rely on worktrees: `./delegation/setup-worktree.sh 001` creates backend/frontend/integration environments, and `./delegation/cleanup-worktree.sh 001` removes them when a sprint closes.

## Coding Style & Naming Conventions
Target PHP 8.2+, prefer strict types, and keep files formatted with Pint (`composer lint`). Follow PSR-12 for structure, use `PascalCase` for classes and `camelCase` for methods and variables. Place shared helpers inside `src/Support/` and keep command orchestration in `src/Command/`. Execute Pint before committing and document any deviations in `README.md`.

## Testing Guidelines
Organise tests under `tests/`, mirroring the `src/` tree, and suffix files with `Test.php`. Use Pest (`composer test`) and add integration fixtures for complex CLI interactions. Maintain ≥85% coverage, matching the quality gates in `delegation/README.md`, and gate PRs below that threshold. Store sample Laravel fixtures under `tests/fixtures/`, anonymising any external data.

## Commit & Pull Request Guidelines
Adopt Conventional Commits (`feat:`, `fix:`, `chore:`) so changelog automation stays predictable. Reference sprint/task IDs from `delegation/` packs in commit bodies (e.g., `Refs ENG-001`). Keep PRs focused on one deliverable, include test evidence (commands run, screenshots for TUI updates), and link to the relevant sprint status update. Secure one review from a role-aligned agent before merging.

## Security & Configuration Tips
Never commit `.env` secrets; ship a redacted `.env.example` instead. Document required external API keys (OpenAI, Anthropic, Ollama) in the main README so contributors know what to provision locally. When analysing third-party Laravel projects, scrub sensitive data before copying it into fixtures or generated context packs.
