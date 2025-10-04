# Laravel Context Pack (MVP)

Prototype CLI that assembles high-signal artefacts from Laravel applications so teammates and AI agents can ramp quickly.

## Requirements
- PHP 8.2+
- Composer
- Access to the target Laravel project (same machine)

## Quick Start
```bash
# Clone and install dependencies
composer install

# Generate a context pack (writes to <project>/context-pack by default)
php bin/context-pack.php generate --project=/path/to/laravel-app

# Overwrite an existing pack or target a custom directory
php bin/context-pack.php generate --project=/path/to/app --output=./build/context-pack --force
```

The command validates the Laravel project, prepares the output workspace, captures metadata, generates `01-schema-dump.sql` via `php artisan schema:dump` (with fallbacks for SQLite/MySQL/Postgres), produces `02-route-summary.md` from `php artisan route:list --json`, assembles a `03-model-overview.md`, and runs available tooling (Pint, Pest, PHPStan/Larastan, Rector) to summarise findings in `04-static-analysis.md`, seeding logs for each step.

## Project Layout
```
├── bin/context-pack.php         # CLI entry point
├── src/                         # PHP source (commands, support classes)
├── tests/                       # Pest test suites
├── docs/                        # Planning artefacts and specs
├── composer.json                # Dependencies and scripts
├── pint.json                    # Laravel Pint formatting rules
├── phpstan.neon.dist            # Static analysis configuration
└── rector.php                   # Automated refactoring rules
```

## Developer Tooling
- Format: `composer lint`
- Static analysis: `composer analyse -- --no-progress`
- Tests: `composer test`
- Rector dry run: `composer rector -- --dry-run`

Generated context packs follow the contract in `docs/mvp-output-spec.md`; schema, route, model, and static-analysis artefacts are included, with skipped tools clearly noted when unavailable.

## Generated Artefacts
| File | Description | Notes |
|------|-------------|-------|
| `metadata.json` | Environment snapshot (Laravel/PHP versions, tooling availability, DB info, artefact statuses) | Drives conditional behaviour in generators. |
| `01-schema-dump.sql` | Database schema via `php artisan schema:dump` | Falls back to `sqlite3`, `mysqldump`, or `pg_dump` when artisan support is missing. |
| `02-route-summary.md/.json` | HTTP contract overview with metrics, warnings, and JSON export | Uses `route:list --json`, falls back to text parsing, highlights unnamed/Closure routes, links to controller source files when discoverable. |
| `03-model-overview.md` | Eloquent model summaries (tables, fillable/guarded props, relationships) | Static heuristics over `app/Models`; logs skipped/unknown relationships for later review. |
| `04-static-analysis.md` | Aggregated results from Pint, Pest, PHPStan/Larastan, Rector | Each tool runs only if detected; failures logged and surfaced as `FAILED`/`PARTIAL`. |
| `logs/*.log` | Raw command output per generator | Useful when artefacts are skipped or fail. |

## Logs & Diagnostics
Every generator writes to `logs/<name>.log`. Investigate these when artefacts report `FAILED`/`PARTIAL`. The CLI never stores secrets; DB passwords are kept in process env only.

## Tool Detection
- Painter (`laravel/pint`)
- Pest (`pestphp/pest` or `tests/Pest.php` present)
- PHPStan (`phpstan/phpstan`) / Larastan (`nunomaduro/larastan`)
- Rector (`rector/rector`)

Unavailable tools are marked `SKIPPED` in `metadata.json` and `04-static-analysis.md` so dogfooders understand coverage gaps.
