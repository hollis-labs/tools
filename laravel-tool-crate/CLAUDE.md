# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

**laravel-tool-crate** is a Laravel package that provides an MCP (Model Context Protocol) server built on top of the official `laravel/mcp` package. It offers a context-lean toolset for AI assistants to interact with Laravel projects, focusing on JSON querying, text search, file operations, git operations, and data analysis.

**Status**: Active development, not production-ready. Breaking changes expected.

## Development Commands

### Testing Tools via CLI
```bash
# Test jq/json_query functionality
php artisan tool:jq '.packages[] | {name,version}' --file=composer.lock

# Test text_search functionality
php artisan tool:search 'Route::' --paths=app --paths=routes --ignore
```

### Testing MCP Server
```bash
# Initialize MCP server (should complete without errors)
echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}' | php artisan mcp

# List available tools
echo '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' | php artisan mcp
```

### Package Installation (Development)
```bash
composer require hollis-labs/laravel-tool-crate:* --dev
php artisan vendor:publish --tag=laravel-tool-crate-config
```

## Architecture

### Core Components

**ToolCrateServer** (`src/Servers/ToolCrateServer.php`)
- Extends `Laravel\Mcp\Server`
- Registers and filters tools based on config
- Auto-registered in `ToolCrateServiceProvider` via `Mcp::local()`

**Tool System**
- All tools extend `Laravel\Mcp\Server\Tool`
- Tools implementing `SummarizesTool` interface provide static summary methods for help system
- Tool metadata uses instance properties (Laravel MCP v0.2+ API):
  - `protected string $name` - Tool identifier
  - `protected string $title` - Human-readable title
  - `protected string $description` - Brief description
- Tools also provide static summary methods for the help system:
  - `summaryName()`, `summaryTitle()`, `summaryDescription()`, `schemaSummary()`

**ToolRegistry** (`src/Support/ToolRegistry.php`)
- Dynamically instantiates and summarizes tools
- Uses reflection to extract metadata from tool instances
- Falls back to static `SummarizesTool` methods when properties unavailable
- Powers the `help_index` and `help_tool` tools

**Configuration** (`config/tool-crate.php`)
- `enabled_tools` - Toggle individual tools on/off
- `priority_tools` - Controls which tools appear first in help
- `categories` - Groups tools for help index display

### Available Tools

| Tool | Purpose | Key Parameters |
|------|---------|----------------|
| `json_query` | Query/transform JSON via jq | `program`, `json`, `file`, `raw`, `slurp`, `cwd` |
| `text_search` | Grep-like text search | `pattern`, `text`, `paths`, `glob`, `context_lines`, `ignore_case` |
| `file_read` | Safe file reading with limits | `path`, `offset`, `limit`, `cwd` |
| `text_replace` | Preview-only text replacement | `path`, `search`, `replace`, `regex`, `cwd` |
| `git_status` | Git status (porcelain v2) | None |
| `git_diff` | Git diff or PR diff via gh | `range`, `staged`, `paths`, `pr` |
| `git_apply_patch` | Apply git patches safely | `patch`, `check_only`, `cwd` |
| `git_sandbox` | Create/manage temp worktrees | `action`, `description`, `interactive`, `tmux`, `theme`, `allow_dirty`, `keep`, `cwd` |
| `db_query` | Safe SELECT queries | `sql`, `connection`, `limit`, `bindings` |
| `db_inspect` | Schema inspection | `connection`, `table`, `show_columns`, `show_indexes`, `show_foreign_keys` |
| `db_schema_dump` | SQL schema dumps | `connection`, `output_path`, `method`, `cwd` |
| `table_query` | SQL queries over CSV/TSV | `file`, `query`, `delimiter`, `has_header`, `cwd` |
| `help_index` | List tools with priorities | `limit` |
| `help_tool` | Detailed tool info | `name` |

### Utility Classes

**Exec** (`src/Support/Exec.php`)
- Wrapper for `Symfony\Component\Process\Process`
- Standardized command execution with timeout, stdin, and cwd support
- Returns object with `ok`, `stdout`, `stderr`, `code` properties

**GitRunner** (`src/Support/GitRunner.php`)
- Git command execution helper
- Detects `gh` CLI availability for PR operations

## Laravel MCP v0.2 API Migration

This package uses Laravel MCP v0.2+ API. When creating or modifying tools:

### Tool Metadata (Instance Properties)
```php
class MyTool extends Tool
{
    protected string $name = 'my_tool';
    protected string $title = 'My Tool Title';
    protected string $description = 'Brief description';

    public function schema(JsonSchema $s): array { ... }
    public function handle(Request $r): Response { ... }
}
```

### SummarizesTool Interface (Static Methods)
```php
class MyTool extends Tool implements SummarizesTool
{
    // ... instance properties above ...

    public static function summaryName(): string { return 'my_tool'; }
    public static function summaryTitle(): string { return 'My Tool Title'; }
    public static function summaryDescription(): string { return 'Brief description'; }
    public static function schemaSummary(): array { return ['param' => 'description']; }
}
```

### JsonSchema Method Changes
- Use `min()` and `max()` instead of `minimum()`, `maximum()`, `minItems()`, `maxItems()`
- Remove `additionalProperties(true)` (default behavior)

## Key Principles

**Context-Lean Design**
- Terse names and descriptions to minimize token usage
- Tools return minimal, actionable output
- Help system is separate from tool discovery

**Local-First**
- Designed for local development environments
- Requires external tools: `jq`, `git`, optionally `gh`
- SQLite in-memory database for CSV/TSV querying

**Safe Defaults**
- `git_apply_patch` defaults to `check_only=true`
- `text_replace` is preview-only (returns unified diff)
- File operations respect working directory boundaries

## File Structure
```
src/
├── Console/
│   ├── DevJqCommand.php         # CLI mirror of json_query
│   └── DevSearchCommand.php     # CLI mirror of text_search
├── Servers/
│   └── ToolCrateServer.php      # Main MCP server
├── Support/
│   ├── Exec.php                 # Process execution wrapper
│   ├── GitRunner.php            # Git command helper
│   └── ToolRegistry.php         # Dynamic tool introspection
├── Tools/
│   ├── Contracts/
│   │   └── SummarizesTool.php   # Interface for help system
│   ├── JqQueryTool.php
│   ├── TextSearchTool.php
│   ├── FileReadTool.php
│   ├── TextReplaceTool.php
│   ├── GitStatusTool.php
│   ├── GitDiffTool.php
│   ├── GitApplyPatchTool.php
│   ├── TableQueryTool.php
│   ├── HelpIndexTool.php
│   └── HelpToolDetail.php
└── ToolCrateServiceProvider.php
```

## Dependencies

**Required**
- PHP 8.2+
- Laravel 10/11/12
- `laravel/mcp` ^1.0|^0.2|dev-main
- `symfony/process` ^7.0
- `symfony/finder` ^7.0
- `ext-pdo_sqlite` (for table_query)

**External Tools**
- `jq` - Required for json_query
- `git` - Required for git tools
- `gh` (optional) - Enhances git_diff with PR support
- `git-sandbox` (optional) - Required for git_sandbox tool (install: `brew install hollis-labs/tap/git-sandbox`)

## Adding New Tools

1. Create tool class in `src/Tools/` extending `Tool`
2. Implement `SummarizesTool` if it should appear in help system
3. Define instance properties: `$name`, `$title`, `$description`
4. Implement static methods: `summaryName()`, `summaryTitle()`, `summaryDescription()`, `schemaSummary()`
5. Implement `schema()` and `handle()` methods
6. Register in `ToolCrateServer::$tools` array
7. Add to `getToolConfigKey()` mapping
8. Add to `config/tool-crate.php` in all three arrays
