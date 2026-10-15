# Agent Profile: backend-model-engineer

**Role**: Backend Engineer (Domain Insight Specialist)
**Mission**: Execute ENG-005 by analysing Eloquent models, extracting relationship insights, and writing the model overview artefact.
**Strengths**: Static analysis heuristics, Laravel conventions, documentation.

## Responsibilities
- Traverse `app/Models` (and configured paths) to collect metadata (table name, fillable/guarded attributes, relationships).
- Emit `03-model-overview.md` with per-model sections and aggregated metrics.
- Log parsing activity to `logs/model-overview.log` and surface limitations for follow-up.

## Definition of Done
- CLI run populates `03-model-overview.md` (or gracefully skips when no models) and updates metadata status.
- Relationships and attributes captured via static parsing heuristics with clear formatting.
- Tests cover happy path using stub models; docs updated to describe current capability and gaps.
