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
| `03-model-overview.md` | Eloquent model summaries (tables, fillable/guarded props, casts, soft deletes, relationships) | Static heuristics over `app/Models`; logs skipped/unknown relationships for later review. |
| `04-static-analysis.md` | Aggregated results from Pint, Pest, PHPStan/Larastan, Rector | Each tool runs only if detected; issues counted per tool and failures marked `FAILED`/`PARTIAL`. |
| `logs/*.log` | Raw command output per generator | Useful when artefacts are skipped or fail. |

## Logs & Diagnostics
Every generator writes to `logs/<name>.log`. Investigate these when artefacts report `FAILED`/`PARTIAL`. The CLI never stores secrets; DB passwords are kept in process env only.

## Tool Detection
- Painter (`laravel/pint`)
- Pest (`pestphp/pest` or `tests/Pest.php` present)
- PHPStan (`phpstan/phpstan`) / Larastan (`nunomaduro/larastan`)
- Rector (`rector/rector`)

Unavailable tools are marked `SKIPPED` in `metadata.json` and `04-static-analysis.md` so dogfooders understand coverage gaps.

## Extensibility

Laravel Context Pack is designed to be extended with custom generators. Create generators to document any aspect of your Laravel application.

### Creating a Custom Generator

Use the scaffolding command to quickly create a new generator:

```bash
php bin/context-pack.php make:generator YourGenerator
```

This creates:
- Generator class in `src/App/Generators/` (or custom namespace)
- Test file with basic test cases
- PHPStan-compliant, production-ready code

### Example Generators

The following example generators demonstrate real-world use cases:

- **ApiDocumentationGenerator** - Document REST API endpoints from routes and controllers
- **FeatureFlagGenerator** - Track feature flags and their usage across the codebase
- **PermissionMatrixGenerator** - Generate authorization matrix for policies and gates

See [docs/extensibility.md](docs/extensibility.md) for complete documentation and [docs/examples/](docs/examples/) for working implementations.

### Generator Interface

All generators implement `GeneratorInterface`:

```php
interface GeneratorInterface
{
    public function getName(): string;              // Unique identifier (e.g., 'api-documentation')
    public function getDescription(): string;       // Human-readable description
    public function generate(string $projectPath, string $outputPath): array;
    public function canRun(string $projectPath): bool;
    public function getConfigSchema(): array;
}
```

### Quick Example

```php
use LaravelContextPack\Generator\AbstractGenerator;

final class MyGenerator extends AbstractGenerator
{
    public function getName(): string
    {
        return 'my-generator';
    }

    public function getDescription(): string
    {
        return 'Generate custom documentation';
    }

    public function generate(string $projectPath, string $outputPath): array
    {
        $outputFile = $outputPath . '/my-output.md';
        $content = $this->buildDocumentation($projectPath);

        $this->filesystem->dumpFile($outputFile, $content);

        return [
            'status' => 'success',
            'message' => 'Documentation generated',
        ];
    }
}
```

### Key Features

- **Type-safe**: PHPStan level 6 compliant
- **Base functionality**: Common utilities via `AbstractGenerator`
- **Registry system**: Dynamic discovery and registration
- **Scaffolding tools**: Quick generator creation
- **Comprehensive docs**: Full guide with examples

For complete documentation, see [docs/extensibility.md](docs/extensibility.md).
