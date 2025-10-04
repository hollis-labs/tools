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

The command validates the Laravel project, prepares the output workspace, writes initial metadata, and seeds log files. Subsequent modules (schema dump, routes, models, static analysis) will build on this structure.

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
- Static analysis: `composer analyse`
- Tests: `composer test`
- Rector dry run: `composer rector -- --dry-run`

Generated context packs follow the contract in `docs/mvp-output-spec.md` and include placeholders for artefacts still under development.
