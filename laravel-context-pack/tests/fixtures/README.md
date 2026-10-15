# Test Fixtures

This directory contains minimal Laravel project fixtures for testing the Laravel Context Pack CLI.

## Available Fixtures

### 1. basic-app
- **Purpose**: Basic Laravel 11 application with minimal features
- **Models**: 5 simple models with basic relationships
- **Database**: SQLite
- **Tools**: None
- **Use Cases**: Testing core functionality, basic model parsing, simple route detection

### 2. standard-app
- **Purpose**: Standard Laravel 11 application with common features
- **Models**: 10+ models with varied relationships
- **Database**: MySQL configuration
- **Tools**: Pint, PHPStan
- **Use Cases**: Testing tool detection, complex relationships, middleware handling

### 3. complex-app
- **Purpose**: Complex Laravel 11 application with advanced features
- **Models**: 20+ models with polymorphic relationships
- **Database**: PostgreSQL configuration
- **Tools**: All tools (Pint, PHPStan, Pest, Rector)
- **Use Cases**: Testing advanced relationships, multiple namespaces, comprehensive tool analysis

## Fixture Structure

Each fixture contains:
- `composer.json` - Project dependencies
- `composer.lock` - Locked package versions
- `.env` - Database configuration
- `artisan` - Stubbed artisan CLI
- `app/Models/` - Model files
- `app/Http/Controllers/` - Controller files
- `routes/` - Route definitions (simulated via stubbed artisan)
- `storage/framework/cache/` - Cache directory for schema dumps

## Usage

Tests use these fixtures to:
1. Validate generator behavior across different Laravel configurations
2. Test fallback mechanisms when tools are unavailable
3. Ensure proper parsing of various model and route structures
4. Verify graceful handling of edge cases
