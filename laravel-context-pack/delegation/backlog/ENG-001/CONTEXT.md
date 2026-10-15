# Background
- The current repository lacks an executable CLI; `PROJECT.md` provides a future-state structure but nothing has been scaffolded yet.
- Stakeholders only require a simple CLI for MVP. UX polish and TUI work are deferred.
- Tool must accept a target Laravel project path, validate prerequisites, and create an output workspace.

# Dependencies
- PM-001 runtime decision determines language/tooling to use.
- PM-002 output spec defines default directory layout and naming.

# Acceptance Criteria
- Running the command with `--project /path/to/app --output ./context-pack` creates the output folder and stores a stub README/log.
- Command exits early with friendly errors if required executables (e.g., `php`, `artisan`) are missing.
- Code organised to allow adding feature modules (schema dump, route report) without rewrites.
