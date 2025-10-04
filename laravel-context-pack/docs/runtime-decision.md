# MVP Runtime Decision

**Date**: 2025-02-14  
**Owner**: Project Management (PM-001)

## Context
- `PROJECT.md` described a Bun/TypeScript CLI with a future TUI, but stakeholders asked for a lean MVP focused on artefact generation.
- The tool must integrate deeply with Laravel- and PHP-specific tooling (Artisan, Pint, Larastan, Pest) while remaining flexible enough to inspect front-end stacks (React, Vue, Livewire) when present.
- Distribution can remain repository-local for the MVP; cloning the repo and running a script is acceptable. Windows support and global installers can wait.

## Options Considered
1. **PHP CLI (Symfony Console / bespoke script)**
   - ✅ Native access to PHP ecosystem tooling and Laravel helpers.
   - ✅ Easy to ship as Composer script, PHAR, or raw PHP entrypoint later.
   - ⚠️ Requires shelling out to Node/Bun for JS analysis, but acceptable for MVP.
2. **Hybrid PHP orchestrator + Node helpers**
   - ✅ Leverages strengths of each ecosystem.
   - ⚠️ Added coordination overhead for MVP and complicates initial setup.
3. **Node/Bun CLI**
   - ✅ Strong integration with JS tooling and future cross-stack features.
   - ⚠️ Awkward access to Laravel/PHP internals; highest ramp-up for the current team.
4. **Shell-only scripts**
   - ✅ Fastest to spike.
   - ⚠️ Poor maintainability, weak error handling, non-portable.

## Decision
Proceed with a **PHP-based CLI MVP** (single entrypoint script using Symfony Console components or Laravel Zero style bootstrap).

## Rationale
- Aligns with team expertise and target ecosystem (Laravel-first, expanding outward later).
- Minimal impedance when invoking Artisan commands, Composer-installed tools, or reading PHP config.
- Keeps initial developer experience simple (clone, run `php bin/context-pack.php --project ...`).
- Provides a stable foundation to layer optional Node/Bun helpers once front-end analysis becomes a priority.

## Implementation Notes
- Scaffold under `bin/context-pack.php` (or `app/Console` if adopting Laravel Zero).
- Manage dependencies via Composer; Composer project creation can follow after MVP CLI is stable.
- Structure code so feature modules live in `src/` namespaces, easing future refactors or packaging.

## Follow-up Actions
- Update backlog items to reflect PHP runtime decision (ENG-001, ENG-002, etc.).
- Ensure `ENG-001` scaffolding task adopts PHP entrypoint and Composer setup.
- When front-end analysis is required, design a companion Node/Bun helper script callable from PHP.
