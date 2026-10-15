# Background
- Stakeholders identified schema dumps as the lowest-hanging fruit and highest priority artefact.
- Laravel offers `php artisan schema:dump` (Laravel 8+) but we must accommodate older versions or projects without the dump command enabled.
- Output spec from PM-002 will define filename and formatting expectations.

# Dependencies
- ENG-001 CLI scaffolding for command invocation.
- ENG-002 metadata to know Laravel version and DB driver before executing.

# Acceptance Criteria
- Tool generates schema file in output pack every run (overwrite with confirmation or versioning strategy).
- Supports fallback (e.g., running migrations to dump via `mysqldump`/SQLite `.schema`) when `schema:dump` unavailable.
- Captures command logs/errors alongside result for troubleshooting.
- Respects `.env` database credentials securely (no leaks in logs).
