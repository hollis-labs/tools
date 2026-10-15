# Execution Plan

1. **Inventory Tools & Flags** (0.75h)
   - Map metadata signals to command invocations (e.g., Pint: `./vendor/bin/pint`).
   - Decide timeouts and failure handling rules.
2. **Implement Runner Abstractions** (1.5h)
   - Add utility to execute commands, capture exit codes, stdout/stderr.
   - Support optional commands without failing overall run.
3. **Parse & Summarise Outputs** (2h)
   - For each tool, parse relevant sections (counts, file paths, severities).
   - Build Markdown sections summarising findings and suggestions.
4. **Log Management** (0.5h)
   - Store raw logs under `logs/` with timestamps.
   - Annotate summary with log references.
5. **Testing & Documentation** (0.75h)
   - Validate with projects that have Pint/Larastan/Pest configured.
   - Document configuration requirements and skip behaviour.

**Estimated Effort**: 5.5 hours
