# Background
- Beyond the schema dump, engineers and AI agents need a narrative of models, relationships, and noteworthy traits.
- `PROJECT.md` envisions analyzers like `ModelAnalyzer.ts`; this task prototypes that capability for the CLI MVP.

# Dependencies
- ENG-002 metadata (including detected packages/frameworks).
- ENG-003 schema dump for column/table context.

# Acceptance Criteria
- Scans `app/Models` (and configured paths) to identify Laravel models.
- Detects relationships via method heuristics (`hasOne`, `belongsTo`, etc.) and documents them.
- Produces human-readable summary (Markdown) plus machine-readable data if feasible.
- Flags potential issues (missing docblocks, mass assignment guard, etc.) for future enhancements.
