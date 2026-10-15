# Execution Plan

1. **Design Analysis Heuristics** (1.5h)
   - Decide whether to use simple AST parsing (e.g., `php-parser`, `nikic`) or regex-based heuristics for MVP.
   - Define data points to extract per model.
2. **Implement Model Discovery** (1h)
   - Traverse directories for model classes.
   - Identify table name, guarded/fillable properties.
3. **Relationship Extraction** (2h)
   - Parse relationship methods and capture type, target model, key fields.
   - Map to schema metadata where available.
4. **Generate Reports** (1h)
   - Produce Markdown summary plus optional JSON export.
   - Include stats (model count, relationship density).
5. **Validation & Documentation** (0.75h)
   - Test against sample projects.
   - Document limitations and follow-up opportunities.

**Estimated Effort**: 6.25 hours
