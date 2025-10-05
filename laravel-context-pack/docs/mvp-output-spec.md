# MVP Context Pack Output Specification

**Date**: 2025-02-14  
**Owner**: Project Management (PM-002)

## Goal
Define the minimum artefacts the Laravel Context Pack generator must emit for the dogfooding-ready MVP. Artefacts should prioritise high-value insights (database, routes, models, tooling health) while keeping implementation incremental.

## Target Directory Layout
```
context-pack/
├── metadata.json
├── 01-schema-dump.sql
├── 02-route-summary.md
├── 03-model-overview.md
├── 04-static-analysis.md
├── logs/
│   ├── schema-dump.log
│   ├── route-summary.log
│   └── static-analysis.log
└── README.md
```

## Artefact Requirements
### 1. `metadata.json` (Mandatory)
- Source: ENG-002 detection layer.
- Contents: project name, Laravel version, PHP version (if available), database driver, detected packages (Filament, Livewire, Inertia, Nova), available tooling (Pint, Larastan, Pest, Rector), timestamp, generator version.
- Purpose: drive conditional logic for downstream generators and provide quick situational awareness.

### 2. `01-schema-dump.sql` (Mandatory)
- Source: `php artisan schema:dump --no-interaction --prune --schema-path=<tmp>` when available; fall back to database-specific dump commands.
- Acceptance: dump should include current schema with relationships (FKs) and be reproducible by re-running generator.
- Notes: include warning header if fallback strategy used or dump incomplete.

-### 3. `02-route-summary.md/.json` (Mandatory)
- Source: `php artisan route:list --json` when supported; fallback to parsing text output.
- Structure: Markdown overview (total routes, methods breakdown, grouped tables) plus JSON export capturing normalised method/URI/action/middleware data.
- Include metrics by HTTP verb and warnings for routes missing names or using `Closure` actions.

### 4. `03-model-overview.md` (Mandatory)
- Source: static analysis of `app/Models` (and configured directories) plus schema metadata.
- Sections per model summarising table, fillable/guarded props, casts, soft deletes usage, relationships (type, target model, keys), noteworthy traits/scopes.
- Provide aggregate metrics (model count, relationships per model, soft delete usage).

### 5. `04-static-analysis.md` (Optional per availability, but file must exist)
- Source: Pint, Larastan/PHPStan, Pest, Rector (dry-run) depending on detected tooling.
- Structure: summary table noting which tools ran, exit status, and notes; logs contain raw output.
- When a tool is unavailable, note "Skipped (not installed)"; when commands fail, mark status `FAILED` with guidance to inspect logs.

### 6. `logs/*.log` (Mandatory directory, files conditional)
- Store raw stdout/stderr for each generator step (`schema-dump.log`, `route-summary.log`, `model-overview.log`, `static-analysis.log`).
- Each log file should include timestamp header and executed command for traceability.

### 7. `README.md` (Mandatory)
- Brief overview of generated artefacts, run metadata (timestamp, command args), and guidance for interpreting files.
- Serves as landing page for humans reviewing the context pack.

## Execution Order & Dependencies
1. Create output workspace and `README.md` scaffold (ENG-001).
2. Generate `metadata.json` (ENG-002) – prerequisite for conditional steps.
3. Schema dump (ENG-003) – uses metadata for DB driver hints.
4. Route summary (ENG-004) – uses metadata for Laravel version features.
5. Model overview (ENG-005) – relies on schema + metadata.
6. Static analysis aggregation (ENG-006) – toggled per tooling availability.
7. Populate README with success/failure summary (ENG-001 enhancement).

## Acceptance Criteria
- Running the CLI against a Laravel project produces the directory layout above with up-to-date contents and logs.
- Generator exits non-zero only when a mandatory artefact fails irrecoverably; optional tooling results should not block completion.
- Artefacts are deterministic for identical project state (no random ordering, include sort normalization).
- README reflects overall status, listing any skipped/failed steps with actionable guidance.

## Enhancement Roadmap (Post-MVP)
1. Architecture narrative (services/modules overview).
2. Front-end asset analysis (NPM dependencies, Vite config, component summary).
3. Test coverage and flakes report.
4. Performance profiling hooks.
5. Export in alternative formats (JSON bundle, HTML dashboard).

## Notes
- All artefacts should avoid leaking secrets; sanitise `.env` or credential references when logging commands.
- Future packaging (Composer global install, PHAR) will reuse this structure; keep paths stable.
