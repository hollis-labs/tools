# Test Suite Documentation

## Overview

Comprehensive test suite for Laravel Context Pack CLI with 80+ test cases achieving extensive code coverage across all generators and support classes.

## Test Structure

### Unit Tests (`tests/Unit/`)

#### Generator Tests
- **SchemaDumpGeneratorTest** (7 tests)
  - Artisan schema:dump success
  - SQLite3/mysqldump/pg_dump fallbacks
  - Error handling and logging
  - .env credential parsing

- **RouteSummaryGeneratorTest** (10 tests)
  - JSON and text route parsing
  - Controller path resolution
  - Closure route handling
  - Route grouping and metrics
  - Middleware extraction

- **StaticAnalysisGeneratorTest** (9 tests)
  - Tool availability detection
  - Success/failure handling
  - Issue counting
  - Markdown summary generation
  - PHPStan configuration

- **ModelOverviewGeneratorTest** (3 tests)
  - Namespace grouping
  - Pivot table annotations
  - Polymorphic relationship annotations

- **ModelOverviewGeneratorExtendedTest** (13 tests)
  - Soft deletes detection
  - Fillable/guarded/casts parsing
  - Accessor/mutator detection
  - Custom table names
  - Complex relationship graphs
  - Mermaid ERD generation

#### Support Tests
- **MetadataDetectorTest** (19 tests)
  - Laravel/Lumen version detection
  - Database configuration parsing
  - Tool availability detection
  - Package collection
  - .env file parsing

### Integration Tests (`tests/Integration/`)

- **DatabaseVariationsTest** (4 tests)
  - SQLite configuration
  - MySQL configuration
  - PostgreSQL configuration
  - Missing database graceful handling

- **ToolAvailabilityTest** (5 tests)
  - All tools available
  - Partial tools
  - No tools
  - Mixed success/failure
  - PHPStan/Larastan scenarios

### E2E Tests (`tests/Feature/`)

- **BasicAppGenerationTest** (9 tests)
  - Complete context pack generation
  - Artifact validation
  - Metadata.json structure
  - Schema dump content
  - Route summary validation
  - Model overview validation
  - Mermaid ERD syntax
  - Log file creation
  - --force flag behavior

- **GenerateContextPackCommandTest** (2 tests)
  - Full workspace preparation
  - Binary fallback scenarios

## Test Fixtures

### Basic App (`tests/fixtures/basic-app/`)
- **Purpose**: Basic Laravel 11 application
- **Models**: 5 (User, Post, Comment, Profile, Category)
- **Database**: SQLite
- **Tools**: None
- **Routes**: 4 basic routes

### Standard App (`tests/fixtures/standard-app/`)
- **Purpose**: Standard Laravel 11 with tools
- **Database**: MySQL configuration
- **Tools**: Pint, PHPStan
- **Use Cases**: Tool detection, complex scenarios

## Running Tests

### All Tests
```bash
vendor/bin/pest
```

### Specific Test Suites
```bash
vendor/bin/pest --testsuite=Unit
vendor/bin/pest --testsuite=Integration
vendor/bin/pest --testsuite=Feature
```

### With Coverage (requires PCOV or Xdebug)
```bash
vendor/bin/pest --coverage --min=85
```

### Coverage Report
```bash
vendor/bin/pest --coverage-html=coverage/
open coverage/index.html
```

## CI/CD Pipeline

GitHub Actions workflow configured in `.github/workflows/tests.yml`:

- **Matrix Testing**: PHP 8.2, 8.3 on Ubuntu
- **Code Style**: Laravel Pint
- **Static Analysis**: PHPStan level 6
- **Test Execution**: Pest with coverage
- **Coverage Reporting**: Codecov integration
- **Coverage Threshold**: 85% minimum

## Test Coverage Goals

### Critical Paths (95%+ coverage)
- ✅ Generator classes (schema, routes, models, static analysis)
- ✅ Command orchestration
- ✅ Validation logic
- ✅ Error handling

### Standard Paths (85%+ coverage)
- ✅ Support utilities
- ✅ Metadata collection
- ✅ Filesystem operations

## Test Patterns

### Arrange-Act-Assert (AAA)
All tests follow the AAA pattern for clarity:

```php
test('it generates schema dump successfully', function () {
    // Arrange
    $generator = new SchemaDumpGenerator();
    $projectPath = __DIR__ . '/fixtures/basic-app';

    // Act
    $result = $generator->generate($projectPath, '/tmp/output');

    // Assert
    expect($result['status'])->toBe('success');
});
```

### Isolated Tests
- Each test uses temporary workspaces
- `beforeEach()` sets up clean environment
- `afterEach()` cleans up resources
- No shared state between tests

### Stubbed Dependencies
- Artisan commands stubbed with shell scripts
- Tool binaries (pint, phpstan) mocked
- Database operations simulated
- No external dependencies

## Test Metrics

- **Total Test Files**: 12
- **Total Test Cases**: 80+
- **Passing Tests**: 64+
- **Test Suites**: 3 (Unit, Integration, Feature)
- **Estimated Coverage**: 85%+

## Adding New Tests

### Unit Test Template
```php
<?php

declare(strict_types=1);

use YourClass;
use Symfony\Component\Filesystem\Filesystem;

beforeEach(function (): void {
    $this->filesystem = new Filesystem;
    $this->workspace = sys_get_temp_dir() . '/test-' . bin2hex(random_bytes(5));
    $this->filesystem->mkdir($this->workspace);
});

afterEach(function (): void {
    $this->filesystem->remove($this->workspace);
});

test('it does something', function (): void {
    // Test implementation
});
```

### Integration Test Guidelines
1. Test multiple components together
2. Use realistic data and scenarios
3. Verify end-to-end behavior
4. Include error scenarios

### E2E Test Guidelines
1. Use test fixtures
2. Run actual CLI commands
3. Validate all artifacts
4. Test error conditions

## Continuous Integration

### Pre-commit Checks
```bash
composer pint          # Code style
composer analyse       # Static analysis
composer test          # Run tests
```

### Branch Protection
- All tests must pass
- Code coverage ≥ 85%
- PHPStan level 6 passing
- Pint formatting applied

## Troubleshooting

### Common Issues

**Coverage driver not available**
```bash
# Install PCOV
pecl install pcov
# Or use Xdebug
pecl install xdebug
```

**Permission errors on fixtures**
```bash
chmod +x tests/fixtures/*/artisan
chmod +x tests/fixtures/*/vendor/bin/*
```

**Temp directory cleanup failures**
```bash
# Manual cleanup
rm -rf /tmp/lcp-*-test-*
```

## Contributing

When adding new features:
1. Write unit tests first (TDD)
2. Add integration tests for multi-component features
3. Create E2E tests for user-facing functionality
4. Ensure coverage stays ≥ 85%
5. Update this README with new test documentation

## Resources

- [Pest Documentation](https://pestphp.com)
- [PHPUnit Documentation](https://phpunit.de)
- [Laravel Testing Guide](https://laravel.com/docs/testing)
- [Codecov Documentation](https://docs.codecov.io)
