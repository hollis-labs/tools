# Background
- MVP outputs depend on knowing environment data (Laravel version, database, installed tooling) to decide which generators to run and what commands are safe.
- `PROJECT.md` lists analyzers that rely on this metadata; building them later will be easier once a detection layer exists.

# Dependencies
- ENG-001 provides the CLI foundation and configuration loading.
- PM-002 output spec informs which metadata fields are required first.

# Acceptance Criteria
- Detection routine verifies the target path is a Laravel app (checks `artisan`, `composer.json`, `config/app.php`).
- Captures and stores metadata JSON (e.g., `.laravel-context-pack/meta.json`) including:
  - Laravel version, PHP version (if available).
  - Active packages of interest (Filament, Livewire, etc.).
  - Database driver inferred from `.env` or `config/database.php`.
  - Availability of Pint, PHPStan/Larastan, Pest/PHPUnit.
- Exposes results to later tasks via CLI output or intermediate file.
