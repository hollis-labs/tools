# Laravel Context Pack Generator - OSS Project Bootstrap

## Project Overview
Create a system-level CLI tool that generates comprehensive context packages for AI agents and new developers from any Laravel codebase. This will be an open-source TypeScript project distributed via npm/bun.

## Project Location
- **Directory**: `~/Projects/laravel-context-pack`
- **Repository**: Future OSS project (MIT license)

## Technology Stack
- **Runtime**: Bun (fastest installs, TypeScript-first)
- **Language**: TypeScript
- **TUI Library**: @opentui/core + @opentui/solid
- **Distribution**: npm registry (global install)

## Installation & Usage Pattern
```bash
# Global installation
bun add -g laravel-context-pack

# Usage in any Laravel project
cd /path/to/any-laravel-project
laravel-context-pack [options]
```

## Project Structure
```
~/Projects/laravel-context-pack/
├── src/
│   ├── analyzers/           # Modular analysis engines
│   │   ├── LaravelDetector.ts
│   │   ├── DatabaseAnalyzer.ts
│   │   ├── ModelAnalyzer.ts
│   │   ├── RouteAnalyzer.ts
│   │   └── FilamentAnalyzer.ts
│   ├── generators/          # Output formatters
│   │   ├── MarkdownGenerator.ts
│   │   ├── JsonGenerator.ts
│   │   └── HTMLGenerator.ts
│   ├── ui/                  # TUI components
│   │   ├── MainMenu.tsx
│   │   ├── ProgressView.tsx
│   │   └── ConfigModal.tsx
│   ├── tools/               # Tool integration
│   │   ├── DependencyChecker.ts
│   │   ├── PintRunner.ts
│   │   └── LarastanRunner.ts
│   └── cli.ts              # Entry point
├── templates/              # Output templates
│   ├── executive-summary.md
│   ├── architecture.md
│   └── api-reference.md
├── package.json
├── bun.lockb
├── tsconfig.json
└── README.md
```

## Core Features Required

### 1. Smart Project Detection
- Auto-detect Laravel version (5.x - 12.x)
- Identify frameworks: Filament, Livewire, Inertia, Nova
- Check database type: MySQL, PostgreSQL, SQLite
- Detect existing tools: Pint, PHPStan/Larastan, Rector, Pest/PHPUnit

### 2. Dependency Management
- Check for required tools and offer installation suggestions
- Example: "Missing Pint? Install with `composer require laravel/pint --dev`"
- Validate tool versions and compatibility

### 3. Context Pack Output Structure
```
context-pack/
├── 00-executive-summary.md     # Project overview, tech stack, key features
├── 01-architecture.md          # Core models, services, design patterns
├── 02-database-schema.sql      # Schema dump with relationships
├── 03-api-endpoints.md         # Route analysis and documentation
├── 04-development-guide.md     # Setup, testing, quality tools
└── 05-code-structure.md        # Directory tree with descriptions
```

### 4. TUI Interface Design
```
┌─ Laravel Context Pack Generator ─────────────────────────┐
│                                                          │
│  Project: /Users/dev/my-laravel-app                     │
│  Laravel: 11.x • Filament: 4.x • Database: SQLite      │
│                                                          │
│  📝 Sections to Include:                                │
│  [✓] Executive Summary                                   │
│  [✓] Architecture Overview                              │
│  [✓] Database Schema                                     │
│  [✓] API Endpoints                                       │
│  [ ] Code Quality Report                                 │
│  [✓] Development Guide                                   │
│                                                          │
│  ⚙️  Output Format: Markdown  Depth: Detailed          │
│  📁 Output Directory: ./context-pack/                   │
│                                                          │
│  [Generate] [Configure] [Tools Check] [Exit]            │
└──────────────────────────────────────────────────────────┘
```

## Implementation Phases

### Phase 1: Foundation
- [x] Project directory creation
- [ ] Bun project initialization with TypeScript
- [ ] Basic CLI entry point with argument parsing
- [ ] Laravel project detection logic
- [ ] Simple TUI with @opentui/solid

### Phase 2: Core Analysis
- [ ] Database schema analysis (via `php artisan schema:dump`)
- [ ] Model relationship mapping (AST parsing)
- [ ] Route discovery and documentation
- [ ] Dependency checker implementation

### Phase 3: Output Generation
- [ ] Markdown template system
- [ ] JSON output format
- [ ] Context-aware content optimization
- [ ] Multi-format support

### Phase 4: Tool Integration
- [ ] Pint, PHPStan, Rector integration
- [ ] Test coverage analysis
- [ ] Performance recommendations
- [ ] Security scanning basics

## Key Dependencies to Install
```json
{
  "dependencies": {
    "@opentui/core": "latest",
    "@opentui/solid": "latest",
    "commander": "^11.0.0",
    "chalk": "^5.3.0",
    "ora": "^7.0.1",
    "inquirer": "^9.2.0",
    "glob": "^10.3.0",
    "yaml": "^2.3.0"
  },
  "devDependencies": {
    "@types/node": "^20.0.0",
    "typescript": "^5.0.0",
    "@types/inquirer": "^9.0.0"
  }
}
```

## Example Laravel Project Analysis
Based on the current `/seer` project:
- **Laravel**: 12.x
- **Frameworks**: Filament 4.x, Livewire Flux
- **Database**: SQLite with extensive migrations
- **AI Integration**: Multi-provider (OpenAI, Anthropic, Ollama)
- **Key Features**: Fragment management, embeddings, chat sessions
- **Tools**: Pint, Larastan, Pest, Rector, Horizon

## Success Criteria
1. Can analyze any Laravel project in any directory
2. Generates comprehensive yet digestible context packs
3. Beautiful TUI interface for configuration
4. Fast execution with smart caching
5. Extensible architecture for future enhancements
6. Ready for OSS distribution and community adoption

## Next Steps
1. Initialize Bun project with proper package.json
2. Set up TypeScript configuration
3. Install @opentui dependencies
4. Create basic CLI structure
5. Implement Laravel detection logic
6. Build first TUI prototype

## Context from Current Laravel Project Analysis
The `/seer` project serves as an excellent test case:
- 14,034 lines of PHP code
- 44+ database migrations
- Complex AI provider system
- Filament admin interface
- Queue-based processing
- Rich model relationships
- Modern Laravel 12 structure

This provides real-world complexity to validate the context pack generator against.