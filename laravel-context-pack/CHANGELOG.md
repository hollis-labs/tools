# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0-beta] - 2025-10-05

### Added

#### Core Generators
- **Schema Dump Generator** - Extract database schemas with multiple fallback strategies
  - Native Laravel schema dump support
  - MySQL dump fallback
  - PostgreSQL pg_dump fallback
  - SQLite .dump fallback
- **Route Summary Generator** - Comprehensive route documentation
  - Route listing with middleware, methods, and parameters
  - JSON export format for programmatic consumption
  - Markdown format for human readability
- **Model Overview Generator** - Enhanced model documentation
  - Model attributes and relationships detection
  - Relationship type detection (hasOne, hasMany, belongsTo, belongsToMany, etc.)
  - Accessor and mutator detection
  - Mermaid ERD diagram generation
  - 80%+ relationship detection accuracy
- **Static Analysis Generator** - Aggregate analysis tool results
  - PHPStan integration
  - Laravel Pint integration
  - Pest integration
  - Rector integration

#### Extensibility Framework
- **GeneratorInterface** - Contract for all context generators
- **AbstractGenerator** - Base class with common functionality
- **GeneratorRegistry** - Dynamic generator registration and management
- **MakeGeneratorCommand** - Scaffold custom generators with one command
  - Creates generator class from template
  - Creates feature test with assertions
  - Generates boilerplate for canRun(), generate(), and config methods
- **Generator Examples** - 3 production-ready example generators
  - ChangelogGenerator - Generate changelogs from git history
  - EnvironmentVariablesGenerator - Document .env variables
  - DependencyGraphGenerator - Map Composer dependencies

#### Quality Metrics System
- **Validation Framework** - 4 core validation rules
  - Context structure validation
  - JSON schema compliance
  - Required sections checking
  - Format correctness
- **Quality Scoring** - 5 weighted quality metrics
  - Generator Coverage (25%)
  - Model Coverage (20%)
  - Route Coverage (20%)
  - Parse Accuracy (20%)
  - Metadata Richness (15%)
- **Report Generation** - Multiple output formats
  - Markdown reports with visual score bars
  - JSON reports for programmatic access
  - Grading system (A-F)
  - Actionable recommendations
- **Benchmark Suite** - Performance and quality tracking
  - Regression detection
  - Historical comparison
  - Quality trend analysis

#### AI Optimization
- **Token Counters** - 2 token counting strategies
  - Approximate counter (fast estimation)
  - GPT-4 tokenizer (accurate counting)
- **Optimization Strategies** - 3 summarization strategies
  - Proportional - Uniform reduction across all sections
  - Hierarchical - Priority-based reduction (preserves critical sections)
  - Relevance - Keyword-weighted reduction
- **Token Budget Enforcement** - Ensure context fits LLM limits
  - Configurable max token limits
  - Automatic section trimming
  - Token savings reporting
- **Multi-Format Output** - 2 output formats
  - Markdown - Human-readable format
  - JSON - Machine-readable format
- **LLM Integration Guides** - Documentation for popular LLMs
  - Claude integration
  - GPT-4 integration
  - LangChain integration

#### CLI Commands
- `generate` - Generate comprehensive context packs
- `validate` - Validate context pack structure and content
- `quality-score` - Calculate quality metrics for context packs
- `benchmark` - Run quality and performance benchmarks
- `make:generator` - Scaffold custom generators
- `list` - List all available generators

#### Laravel Compatibility
- Laravel 8.x support
- Laravel 9.x support
- Laravel 10.x support
- Laravel 11.x support
- Laravel 12.x support (tested up to 12.30.1)

#### Testing & Quality
- 100+ comprehensive tests
- 85-90% test coverage
- PHPStan level 6 compliance (0 errors)
- GitHub Actions CI/CD pipeline
- Codecov integration
- Feature, integration, and unit test suites

#### Documentation
- Comprehensive README.md with quick start guide
- `docs/extensibility.md` - Generator creation guide (768 lines)
- `docs/ai-integration.md` - LLM integration guide (560 lines)
- `docs/configuration.md` - Configuration reference (410 lines)
- `docs/ai-agent-quickstart.md` - Quick start for AI agents
- `docs/route-json-format.md` - Route JSON schema documentation
- `docs/compatibility/` - Laravel version compatibility matrix
- `docs/examples/` - Example custom generators
- Sprint retrospectives and completion reports

### Changed
- Migrated all generators to new extensibility framework
- Improved Mermaid diagram labels and clarity
- Enhanced CLI output with better progress indicators
- Refined relationship detection algorithm

### Fixed
- Model accessor detection edge cases
- Route parameter parsing in Laravel 12.x
- Relationship detection for polymorphic relationships
- Static analysis tool integration reliability

### Notes
- No breaking changes from internal development versions
- All features backward compatible within beta scope
- Static analysis may fail gracefully if tools not configured

---

## Contributors

This release was developed by the Hollis Labs team using AI-assisted development:
- Sprint 001: Foundation & Architecture
- Sprint 002: Quality & Distribution
- Sprint 003: Context Quality & Integration

Generated with collaboration between human developers and AI agents.

---

[0.1.0-beta]: https://github.com/hollis-labs/laravel-context-pack/releases/tag/v0.1.0-beta
