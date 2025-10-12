# Laravel Tool Crate

> ⚠️ **Status: _Not Ready for Use_** — active development preview. Expect breaking changes, incomplete docs, and missing test coverage. Please do **not** depend on this package in production yet.

**hollis-labs/laravel-tool-crate** — an opinionated, local-first MCP server for Laravel (built on **official `laravel/mcp`**), with:
- a context-lean **help** layer (`help_index`, `help_tool`),
- **developer tools**: `json_query`, `text_search`, `file_read`, `text_replace`,
- **git tools**: `git_status`, `git_diff`, `git_apply_patch`, `git_sandbox` (uses `gh` and `git-sandbox` when available),
- **database tools**: `db_query`, `db_inspect`, `db_schema_dump` (read-only queries, schema inspection, SQL dumps),
- **data tool**: `table_query` (SQLite over CSV/TSV),
- **CLI commands** mirroring the jq/search tools.

## Install (path repo during development)
```json
{
  "repositories": [
    { "type": "path", "url": "packages/laravel-tool-crate", "options": { "symlink": true } }
  ]
}
```
```bash
composer require hollis-labs/laravel-tool-crate:* --dev
php artisan vendor:publish --tag=laravel-tool-crate-config
```

This registers a **local** MCP server in `routes/ai.php` automatically:
```php
use Laravel\Mcp\Facades\Mcp;
use HollisLabs\ToolCrate\Servers\ToolCrateServer;

Mcp::local('tool-crate', ToolCrateServer::class);
```

## Tools
- `help_index` → prioritized + categorized list with follow-up hints
- `help_tool` → details for a named tool (schema summary + hint)
- `json_query` → jq wrapper
- `text_search` → grep-like search (files or inline text)
- `file_read` → safe read with cap & slice
- `text_replace` → preview-only replacement with unified diff
- `git_status` → porcelain v2 (`-z`); reports whether `gh` is present
- `git_diff` → `git diff` by range/staged/paths or `gh pr diff {#}` if available
- `git_apply_patch` → safe by default (`--check`); enable apply with `check_only=false`
- `git_sandbox` → create/manage temporary Git worktrees for safe experiments
- `db_query` → execute safe SELECT queries (read-only, row limits)
- `db_inspect` → inspect database schema (tables, columns, indexes, foreign keys)
- `db_schema_dump` → generate SQL schema dumps (structure only, no data)
- `table_query` → load CSV/TSV to **SQLite (in-memory)** and run a `SELECT`

All names/descriptions/schemas are intentionally terse to keep discovery/context lean.

## CLI examples
```bash
php artisan tool:jq '.packages[] | {name,version}' --file=composer.lock
php artisan tool:search 'Route::' --paths=app --paths=routes --ignore
```

## Config
See `config/tool-crate.php` to enable tools, set priorities, and group categories.

## Requirements
- PHP 8.2+, Laravel 10/11/12
- `jq` for `json_query`
- `git` for git tools
- `gh` (optional) for `git_diff` PR support
- `git-sandbox` (optional) for `git_sandbox` - install via `brew install hollis-labs/tap/git-sandbox`
- SQLite PDO extension for `table_query`
