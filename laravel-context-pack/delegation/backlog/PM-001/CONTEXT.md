# Background
- PROJECT.md outlines a Bun/TypeScript architecture with TUI aspirations, but leadership requested a lean CLI (even shell) MVP that focuses purely on generating artefacts (schema dump, summaries, static analysis logs).
- Early engineering work depends on knowing what runtime, packaging, and dependency story to target; this decision unblocks scaffolding tasks.

# Inputs
- `PROJECT.md` technology stack expectations.
- Constraints from latest stakeholder directive (CLI only, prioritize output over UX polish).
- Knowledge of Laravel project environments (PHP, Composer, Artisan) the tool must call into.

# Dependencies
- None; this decision is the prerequisite for all implementation tasks (ENG-001 onward).

# Deliverables
- Decision note stored in `docs/` or `delegation/` summarizing:
  - Chosen runtime/platform.
  - Required tooling (e.g., Bun, Node, shell + PHP).
  - Fast-follow risks or considerations.
- Update `AGENTS.md` and planning docs if the decision diverges from current guidance.
