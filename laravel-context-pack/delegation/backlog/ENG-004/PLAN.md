# Execution Plan

1. **Investigate Command Support** (0.75h)
   - Determine availability of `route:list --json` based on Laravel version.
   - Decide fallback parsing strategy for text output.
2. **Implement Data Extraction** (1.5h)
   - Run artisan command via CLI scaffold.
   - Parse JSON or text into structured route objects.
3. **Generate Artefacts** (1.5h)
   - Produce Markdown table/sections with grouped routes.
   - Optionally emit raw JSON for machine consumption.
4. **Add Metrics & Summaries** (0.5h)
   - Count routes per HTTP verb, middleware, domain.
   - Highlight potential red flags (missing names, fallback routes).
5. **Testing & Documentation** (0.5h)
   - Validate against sample Laravel app(s).
   - Document usage and limitations in developer docs.

**Estimated Effort**: 4.75 hours
