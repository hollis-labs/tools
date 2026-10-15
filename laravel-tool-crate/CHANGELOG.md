# Changelog

All notable changes to `laravel-tool-crate` will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- New `git_sandbox` tool for creating temporary Git worktrees
- New database tools:
  - `db_query` - Execute safe SELECT queries with row limits
  - `db_inspect` - Inspect database schema (tables, columns, indexes, foreign keys)
  - `db_schema_dump` - Generate SQL schema dumps (structure only, supports artisan/mysqldump/pg_dump/sqlite3)
- GitHub Actions CI workflow with PHP 8.2/8.3 and Laravel 10/11/12 matrix testing
- GitHub Actions Release workflow for automated releases on version tags
- CLAUDE.md documentation for AI-assisted development
- CHANGELOG.md to track version history

### Changed
- Updated CI/Release workflows to be appropriate for PHP/Laravel package (previously bash script focused)
- Added database tools to priority list and new "Database" category in config

## [0.2.2] - 2025-10-06

### Fixed
- Renamed tools for MCP naming compliance ([f61f321](https://github.com/hollis-labs/laravel-tool-crate/commit/f61f321))

## [0.2.1] - 2025-10-06

### Changed
- Updated all tools to Laravel MCP v0.2 API syntax ([181512f](https://github.com/hollis-labs/laravel-tool-crate/commit/181512f))
  - Converted static methods (`name()`, `title()`, `shortDescription()`) to instance properties
  - Updated JsonSchema methods: `minimum()` → `min()`, `maximum()` → `max()`
  - Tools now implement proper `SummarizesTool` interface with static summary methods

### Removed
- Removed orchestration commands ([3e5bcb4](https://github.com/hollis-labs/laravel-tool-crate/commit/3e5bcb4))

### Fixed
- Various tweaks to get everything working ([a2842f8](https://github.com/hollis-labs/laravel-tool-crate/commit/a2842f8))
- Dependency updates ([852586d](https://github.com/hollis-labs/laravel-tool-crate/commit/852586d))

## [0.2.0] - 2025-10-05

### Added
- Initial package bootstrap
- Core MCP server implementation (`ToolCrateServer`)
- Tool implementation:
  - `json_query` - Query/transform JSON via jq
  - `text_search` - Grep-like text search
  - `file_read` - Safe file reading with limits
  - `text_replace` - Preview-only text replacement
  - `git_status` - Git status (porcelain v2)
  - `git_diff` - Git diff or PR diff via gh
  - `git_apply_patch` - Apply git patches safely
  - `table_query` - SQL queries over CSV/TSV
  - `help_index` - List tools with priorities
  - `help_tool` - Detailed tool info
- CLI commands:
  - `tool:jq` - Mirror of json_query tool
  - `tool:search` - Mirror of text_search tool
- Configuration system for enabling/disabling tools and setting priorities
- Support classes:
  - `Exec` - Process execution wrapper
  - `GitRunner` - Git command helper
  - `ToolRegistry` - Dynamic tool introspection
- Service provider with automatic MCP server registration

## Links

[Unreleased]: https://github.com/hollis-labs/laravel-tool-crate/compare/v0.2.2...HEAD
[0.2.2]: https://github.com/hollis-labs/laravel-tool-crate/compare/v0.2.1...v0.2.2
[0.2.1]: https://github.com/hollis-labs/laravel-tool-crate/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/hollis-labs/laravel-tool-crate/releases/tag/v0.2.0
