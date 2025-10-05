# Extensibility Guide

Laravel Context Pack is designed to be highly extensible. You can create custom generators to document any aspect of your Laravel application.

## Table of Contents

- [Overview](#overview)
- [Quick Start](#quick-start)
- [Generator Interface](#generator-interface)
- [AbstractGenerator Base Class](#abstractgenerator-base-class)
- [Generator Registry](#generator-registry)
- [Creating Custom Generators](#creating-custom-generators)
- [Configuration Schema](#configuration-schema)
- [Testing Your Generator](#testing-your-generator)
- [Real-World Examples](#real-world-examples)
- [Best Practices](#best-practices)
- [Troubleshooting](#troubleshooting)

---

## Overview

The generator architecture provides:

- **Interface-based contracts**: All generators implement `GeneratorInterface`
- **Base functionality**: Common features via `AbstractGenerator`
- **Registry system**: Dynamic discovery and registration
- **Scaffolding tools**: Quick generator creation with `make:generator`
- **Type safety**: PHPStan level 6 compliant

### Architecture Diagram

```
┌─────────────────────────────────────┐
│      GeneratorInterface             │
│  - getName()                        │
│  - getDescription()                 │
│  - generate()                       │
│  - canRun()                         │
│  - getConfigSchema()                │
└─────────────────────────────────────┘
                 ▲
                 │
                 │ implements
                 │
┌─────────────────────────────────────┐
│      AbstractGenerator              │
│  + filesystem                       │
│  + appendLog()                      │
│  + now()                            │
│  + isDirectoryAccessible()          │
│  + isFileAccessible()               │
└─────────────────────────────────────┘
                 ▲
                 │
                 │ extends
                 │
┌─────────────────────────────────────┐
│      YourCustomGenerator            │
│  Implement your specific logic      │
└─────────────────────────────────────┘
```

---

## Quick Start

### 1. Create a New Generator

Use the scaffolding command to create a new generator:

```bash
bin/context-pack.php make:generator ApiDocumentation
```

This creates:
- `src/App/Generators/ApiDocumentationGenerator.php` - Generator class
- `tests/Feature/Generators/ApiDocumentationGeneratorTest.php` - Test file

### 2. Implement Your Logic

Edit the generated file and implement the `generate()` method:

```php
public function generate(string $projectPath, string $outputPath): array
{
    // Your implementation here
    $apiEndpoints = $this->scanApiRoutes($projectPath);
    $documentation = $this->buildDocumentation($apiEndpoints);

    $outputFile = $outputPath . '/api-documentation.md';
    $this->filesystem->dumpFile($outputFile, $documentation);

    return [
        'status' => 'success',
        'message' => 'API documentation generated',
        'endpoints' => count($apiEndpoints),
    ];
}
```

### 3. Register Your Generator

```php
use LaravelContextPack\Registry\GeneratorRegistry;
use App\Generators\ApiDocumentationGenerator;

$registry = new GeneratorRegistry();
$registry->register(new ApiDocumentationGenerator());
```

### 4. Run Your Generator

Your generator is now available in the system!

---

## Generator Interface

All generators must implement the `GeneratorInterface`:

```php
interface GeneratorInterface
{
    /**
     * Get the unique identifier for this generator.
     * Use lowercase, hyphenated format (e.g., 'api-documentation').
     */
    public function getName(): string;

    /**
     * Get a human-readable description.
     * Displayed in help text and documentation.
     */
    public function getDescription(): string;

    /**
     * Execute the generator and produce output.
     *
     * @return array{status: string, message: string, ...}
     *   Status must be: 'success', 'skipped', or 'failure'
     */
    public function generate(string $projectPath, string $outputPath): array;

    /**
     * Check if this generator can run on the given project.
     * Return false if required files/directories are missing.
     */
    public function canRun(string $projectPath): bool;

    /**
     * Get the configuration schema for this generator.
     *
     * @return array<string, array<string, mixed>>
     */
    public function getConfigSchema(): array;
}
```

### Method Details

#### `getName(): string`

Returns a unique, lowercase, hyphenated identifier for your generator.

**Examples:**
- `api-documentation`
- `feature-flag`
- `permission-matrix`
- `event-listeners`

#### `getDescription(): string`

Returns a human-readable description of what your generator does.

**Examples:**
- `"Generate API endpoint documentation from routes and controllers"`
- `"Document feature flags and their usage across the application"`
- `"Generate permission matrix for policies and gates"`

#### `generate(): array`

The main method that executes your generator logic.

**Parameters:**
- `$projectPath` - Absolute path to the Laravel project root
- `$outputPath` - Absolute path where files should be written

**Returns:**
An array with at least:
- `status` - One of: `'success'`, `'skipped'`, `'failure'`
- `message` - Human-readable status message

**Optional return fields:**
- `outputFile` - Path to generated file
- Custom metrics (e.g., `endpoints`, `models`, `routes`)

**Example:**

```php
public function generate(string $projectPath, string $outputPath): array
{
    if (!$this->canRun($projectPath)) {
        return [
            'status' => 'skipped',
            'message' => 'Required directories not found',
        ];
    }

    try {
        // Your implementation
        $result = $this->performGeneration($projectPath, $outputPath);

        return [
            'status' => 'success',
            'message' => 'Generation completed successfully',
            'itemsProcessed' => $result['count'],
        ];
    } catch (\Exception $e) {
        return [
            'status' => 'failure',
            'message' => 'Generation failed: ' . $e->getMessage(),
        ];
    }
}
```

#### `canRun(): bool`

Optional pre-check to determine if the generator should run.

**Use cases:**
- Check if required directories exist
- Verify necessary configuration files are present
- Validate project structure

**Example:**

```php
public function canRun(string $projectPath): bool
{
    return $this->isDirectoryAccessible($projectPath . '/app/Http/Controllers/Api')
        && $this->isFileAccessible($projectPath . '/routes/api.php');
}
```

#### `getConfigSchema(): array`

Define configuration options for your generator.

**Example:**

```php
public function getConfigSchema(): array
{
    return [
        'include_examples' => [
            'type' => 'boolean',
            'default' => true,
            'description' => 'Include code examples in documentation',
        ],
        'output_format' => [
            'type' => 'string',
            'default' => 'markdown',
            'enum' => ['markdown', 'html', 'json'],
            'description' => 'Output format',
        ],
        'max_depth' => [
            'type' => 'integer',
            'default' => 3,
            'description' => 'Maximum directory depth to scan',
        ],
    ];
}
```

---

## AbstractGenerator Base Class

The `AbstractGenerator` provides common functionality to simplify generator development.

### Features

#### 1. Filesystem Access

```php
$this->filesystem->dumpFile($path, $content);
$this->filesystem->mkdir($directory);
$this->filesystem->remove($path);
```

#### 2. Logging Utilities

```php
$this->appendLog($logFile, 'Processing started');
$this->appendLog($logFile, sprintf('Found %d items', $count));
```

#### 3. Timestamp Helper

```php
$timestamp = $this->now(); // ISO 8601 format
// Example: 2024-06-28T10:30:00+00:00
```

#### 4. Path Checking

```php
if ($this->isDirectoryAccessible($path)) {
    // Directory exists and is readable
}

if ($this->isFileAccessible($file)) {
    // File exists and is readable
}
```

### Default Implementations

`AbstractGenerator` provides sensible defaults:

```php
public function canRun(string $projectPath): bool
{
    return true; // Override if you need pre-checks
}

public function getConfigSchema(): array
{
    return []; // Override if you need configuration
}
```

---

## Generator Registry

The `GeneratorRegistry` manages generator registration and discovery.

### Basic Usage

```php
use LaravelContextPack\Registry\GeneratorRegistry;

$registry = new GeneratorRegistry();

// Register a generator
$registry->register(new ApiDocumentationGenerator());
$registry->register(new FeatureFlagGenerator(), priority: 10);

// Get a specific generator
$generator = $registry->get('api-documentation');

// Get all generators (ordered by priority)
$allGenerators = $registry->all();

// Check if registered
if ($registry->has('api-documentation')) {
    // ...
}

// Get generator names
$names = $registry->getNames();

// Count registered generators
$count = $registry->count();
```

### Priorities

Higher priority generators run first:

```php
$registry->register(new CriticalGenerator(), priority: 100);
$registry->register(new NormalGenerator(), priority: 0);
$registry->register(new LowPriorityGenerator(), priority: -10);
```

---

## Creating Custom Generators

### Step-by-Step Guide

#### 1. Use the Scaffolding Command

```bash
bin/context-pack.php make:generator YourGenerator
```

Options:
- `--namespace=Custom\\Namespace` - Custom namespace
- `--path=/custom/path` - Custom output path
- `--force` - Overwrite existing files

#### 2. Implement Core Methods

Edit the generated file and fill in the TODOs:

```php
final class YourGeneratorGenerator extends AbstractGenerator
{
    public function getName(): string
    {
        return 'your-generator';
    }

    public function getDescription(): string
    {
        return 'Describe what your generator does';
    }

    public function generate(string $projectPath, string $outputPath): array
    {
        // Step 1: Discover files
        $files = $this->discoverFiles($projectPath);

        // Step 2: Parse and analyze
        $data = $this->analyzeFiles($files);

        // Step 3: Generate documentation
        $content = $this->buildDocumentation($data);

        // Step 4: Write output
        $outputFile = $outputPath . '/your-output.md';
        $this->filesystem->dumpFile($outputFile, $content);

        return [
            'status' => 'success',
            'message' => 'Documentation generated',
            'files' => count($files),
        ];
    }
}
```

#### 3. Add Pre-run Checks

```php
public function canRun(string $projectPath): bool
{
    // Check if required paths exist
    if (!$this->isDirectoryAccessible($projectPath . '/app')) {
        return false;
    }

    // Check for specific files
    if (!$this->isFileAccessible($projectPath . '/composer.json')) {
        return false;
    }

    return true;
}
```

#### 4. Define Configuration (Optional)

```php
public function getConfigSchema(): array
{
    return [
        'enabled' => [
            'type' => 'boolean',
            'default' => true,
        ],
        'output_format' => [
            'type' => 'string',
            'default' => 'markdown',
            'enum' => ['markdown', 'json'],
        ],
    ];
}
```

---

## Configuration Schema

### Schema Format

```php
[
    'option_name' => [
        'type' => 'string|integer|boolean|array|object',
        'default' => 'default_value',
        'description' => 'Human-readable description',
        'enum' => ['allowed', 'values'], // Optional
        'required' => true, // Optional, default false
    ],
]
```

### Examples

#### Boolean Option

```php
'include_private' => [
    'type' => 'boolean',
    'default' => false,
    'description' => 'Include private methods in documentation',
]
```

#### String with Enum

```php
'format' => [
    'type' => 'string',
    'default' => 'markdown',
    'enum' => ['markdown', 'html', 'json'],
    'description' => 'Output format',
]
```

#### Integer with Range

```php
'max_depth' => [
    'type' => 'integer',
    'default' => 3,
    'min' => 1,
    'max' => 10,
    'description' => 'Maximum directory scan depth',
]
```

#### Array Option

```php
'exclude_paths' => [
    'type' => 'array',
    'default' => ['vendor', 'node_modules'],
    'description' => 'Paths to exclude from scanning',
]
```

---

## Testing Your Generator

### Basic Test Structure

```php
final class YourGeneratorTest extends TestCase
{
    private YourGenerator $generator;
    private string $projectPath;
    private string $outputPath;

    protected function setUp(): void
    {
        parent::setUp();

        $this->generator = new YourGenerator();
        $this->projectPath = sys_get_temp_dir() . '/test-project-' . uniqid();
        $this->outputPath = sys_get_temp_dir() . '/test-output-' . uniqid();

        mkdir($this->projectPath);
        mkdir($this->outputPath);
        mkdir($this->outputPath . '/logs');
    }

    protected function tearDown(): void
    {
        // Cleanup
        if (is_dir($this->projectPath)) {
            // Remove recursively
        }
        if (is_dir($this->outputPath)) {
            // Remove recursively
        }

        parent::tearDown();
    }
}
```

### Essential Test Cases

```php
public function test_it_has_correct_name(): void
{
    $this->assertSame('your-generator', $this->generator->getName());
}

public function test_it_generates_output_successfully(): void
{
    $result = $this->generator->generate($this->projectPath, $this->outputPath);

    $this->assertEquals('success', $result['status']);
    $this->assertFileExists($this->outputPath . '/your-output.md');
}

public function test_can_run_returns_false_when_requirements_missing(): void
{
    $this->assertFalse($this->generator->canRun('/nonexistent/path'));
}

public function test_it_handles_errors_gracefully(): void
{
    // Test error conditions
    $result = $this->generator->generate('/invalid/path', $this->outputPath);

    $this->assertContains($result['status'], ['skipped', 'failure']);
}
```

---

## Real-World Examples

See the following complete examples in `docs/examples/`:

1. **ApiDocumentationGenerator** - Document REST API endpoints
2. **FeatureFlagGenerator** - Track feature flags and their usage
3. **PermissionMatrixGenerator** - Document authorization policies
4. **EventListenerGenerator** - Map events to listeners
5. **ServiceProviderGenerator** - Document service providers and bindings

Each example includes:
- Complete implementation
- Tests
- Usage documentation
- Real-world patterns

---

## Best Practices

### 1. Naming Conventions

- **Generator names**: lowercase-hyphenated (e.g., `api-documentation`)
- **Class names**: PascalCase with "Generator" suffix (e.g., `ApiDocumentationGenerator`)
- **File names**: Match class names

### 2. Error Handling

Always return proper status:

```php
try {
    // Your logic
    return ['status' => 'success', 'message' => '...'];
} catch (\Exception $e) {
    $this->appendLog($logFile, 'ERROR: ' . $e->getMessage());
    return ['status' => 'failure', 'message' => $e->getMessage()];
}
```

### 3. Logging

Log important operations:

```php
$this->appendLog($logFile, '[' . $this->now() . '] Starting generation');
$this->appendLog($logFile, sprintf('Processing %d files', count($files)));
$this->appendLog($logFile, '[' . $this->now() . '] Generation completed');
```

### 4. Output Format

Use consistent markdown structure:

```markdown
# Title

*Generated at:* 2024-06-28T10:30:00+00:00
*Project:* my-laravel-app

## Section 1

Content...

## Section 2

Content...
```

### 5. Performance

- Use Symfony Finder for file discovery
- Batch operations when possible
- Cache expensive computations
- Log progress for long-running operations

### 6. Type Safety

- Use strict types: `declare(strict_types=1);`
- Type all parameters and return values
- Use PHPDoc for arrays with specific structures
- Ensure PHPStan level 6 compliance

---

## Troubleshooting

### Generator Not Found

**Problem:** `GeneratorNotFoundException: Generator "my-generator" not found`

**Solution:**
1. Ensure generator is registered in the registry
2. Check the name returned by `getName()` matches
3. Verify generator implements `GeneratorInterface`

### Files Not Created

**Problem:** Output files are not being created

**Solutions:**
1. Check output path permissions
2. Verify `$outputPath` directory exists
3. Check for errors in the log file
4. Ensure `generate()` returns `status: 'success'`

### PHPStan Errors

**Problem:** Type errors when running PHPStan

**Solutions:**
1. Add proper type hints to all methods
2. Use `@return` annotations for complex arrays
3. Check `getConfigSchema()` returns `array<string, array<string, mixed>>`
4. Ensure `generate()` return type is correct

### Infinite Loops or Crashes

**Problem:** Generator hangs or crashes

**Solutions:**
1. Add depth limits to recursive operations
2. Validate input paths before processing
3. Add timeout limits for long operations
4. Check for circular dependencies

### Tests Failing

**Problem:** Tests are failing

**Solutions:**
1. Check file permissions in test directories
2. Ensure cleanup in `tearDown()` is working
3. Use unique temp directories for each test
4. Mock filesystem operations if needed

---

## Additional Resources

- [GeneratorInterface API Reference](../src/Generator/GeneratorInterface.php)
- [AbstractGenerator API Reference](../src/Generator/AbstractGenerator.php)
- [Example Generators](./examples/)
- [Contributing Guide](../CONTRIBUTING.md)

---

## Support

For questions or issues:

1. Check this documentation
2. Review example generators
3. Open an issue on GitHub
4. Join our community discussions

Happy generating!
