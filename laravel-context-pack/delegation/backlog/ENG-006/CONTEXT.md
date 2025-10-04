# Background
- Static analysis outputs elevate the context pack by highlighting style issues, type safety gaps, and failing tests.
- Optional tools (Pint, Larastan, Pest) may or may not be installed; the system must detect availability and degrade gracefully.

# Dependencies
- ENG-002 metadata detection (know which tools are present).
- ENG-001 CLI infrastructure for executing commands and storing logs.

# Acceptance Criteria
- Automatically runs available tools; skips missing ones with clear notice.
- Collects outputs into structured summaries (e.g., lint warnings grouped by file, type errors grouped by severity).
- Stores raw logs for deep dives (`logs/` folder).
- Provides consolidated Markdown report with actionable insights and next steps.
