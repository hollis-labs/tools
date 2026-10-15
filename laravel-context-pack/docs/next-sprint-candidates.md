# Next Sprint Candidates (Dogfooding Insights)

## High Priority
- **ENG-003 (Schema Fallback Hardening)** – verify mysqldump/pg_dump behaviour in containerised environments, add retry/backoff, and surface credential guidance when external binaries are missing.
- **ENG-004 (Route Intelligence)** – enrich summaries with controller source links, guard resource routes, and emit JSON alongside Markdown for downstream automation.
- **ENG-006 (Static Analysis Depth)** – support Larastan-specific config, parse Pint/PHPStan summaries into actionable bullet lists, and allow per-tool timeouts.

## Medium Priority
- **ENG-005 (Model Overview Enhancements)** – ingest factory metadata, detect soft deletes/casts, and emit relationship diagrams (Mermaid or Graphviz) for visual onboarding.
- **PM-003 (Dogfooding Loop)** – schedule runs across three representative Laravel apps (REST, Livewire, Filament) and collect qualitative feedback on artefact usefulness.
- **DOC-001 (Onboarding Docs)** – add troubleshooting matrix for common failures (missing artisan, DB credentials, missing binaries) and embed sample snippets from generated artefacts.

## Optional / Stretch
- **Exporters** – provide zipped bundle and JSON summary for external tooling.
- **CLI UX** – add progress indicators and `--only` flags to target specific generators.
- **Toolchain Detection** – detect front-end stacks (Vite, Mix) to prepare for future asset analysis.

Backlog references: ENG-003/004/005/006, PM-003, DOC-001. Update the respective TODOs after prioritisation in the next planning session.
