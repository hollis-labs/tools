# Background
- Route documentation is core to onboarding; stakeholders expect clear HTTP contract summaries in the context pack.
- Laravel's `php artisan route:list --json` (Laravel 10+) can supply machine-readable data; older versions need text parsing.
- Artefact must be human-readable and optionally machine-friendly for future automation.

# Dependencies
- ENG-001 CLI scaffold and ENG-002 metadata (know Laravel version to select command options).
- PM-002 output spec (defines file location/name, e.g., `03-api-endpoints.md`).

# Acceptance Criteria
- Generates Markdown (and optionally JSON) summarising routes with method, URI, controller, middleware, name.
- Groups routes by domain/namespace or prefix where helpful.
- Includes summary stats (counts per method, auth requirement, etc.).
- Handles large output gracefully (pagination, formatting) and logs raw CLI output for debugging.
