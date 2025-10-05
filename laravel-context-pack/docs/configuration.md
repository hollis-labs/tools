# Configuration Guide

This guide covers all configuration options for Laravel Context Pack, including optimization settings, token budgets, and output formats.

## Table of Contents

- [Configuration File](#configuration-file)
- [Environment Variables](#environment-variables)
- [CLI Options](#cli-options)
- [Optimization Settings](#optimization-settings)
- [Output Format Settings](#output-format-settings)
- [Generator Settings](#generator-settings)
- [Performance Settings](#performance-settings)

## Configuration File

Laravel Context Pack uses a PHP configuration file located at `config/context-pack.php`.

### Creating the Configuration File

If the file doesn't exist, create it:

```bash
mkdir -p config
touch config/context-pack.php
```

### Default Configuration

```php
<?php

return [
    'optimization' => [
        'enabled' => false,
        'max_tokens' => 200000,
        'token_counter' => 'approximate',
        'strategy' => 'proportional',
        // ...
    ],
    'output' => [
        'format' => 'markdown',
        // ...
    ],
];
```

## Environment Variables

Override configuration using environment variables in your `.env` file:

| Variable | Type | Default | Description |
|----------|------|---------|-------------|
| `CONTEXT_OPTIMIZATION_ENABLED` | boolean | `false` | Enable/disable optimization globally |
| `CONTEXT_MAX_TOKENS` | integer | `200000` | Maximum token budget |
| `CONTEXT_TOKEN_COUNTER` | string | `approximate` | Token counter: `approximate` or `gpt4` |
| `CONTEXT_OPTIMIZATION_STRATEGY` | string | `proportional` | Strategy: `proportional`, `hierarchical`, or `relevance` |
| `CONTEXT_OUTPUT_FORMAT` | string | `markdown` | Output format: `markdown` or `json` |

### Example .env

```env
# Enable AI optimization
CONTEXT_OPTIMIZATION_ENABLED=true

# Set token budget for GPT-4 Turbo (128k context window)
CONTEXT_MAX_TOKENS=110000

# Use GPT-4 tokenizer for accuracy
CONTEXT_TOKEN_COUNTER=gpt4

# Use hierarchical strategy
CONTEXT_OPTIMIZATION_STRATEGY=hierarchical

# Output as JSON for API consumption
CONTEXT_OUTPUT_FORMAT=json
```

## CLI Options

Override configuration at runtime using command-line options:

### Basic Options

| Option | Type | Description |
|--------|------|-------------|
| `--project` | string | Path to Laravel project (required) |
| `--output` | string | Output directory path |
| `--force` / `-f` | flag | Overwrite existing output directory |
| `--dry-run` | flag | Validate without writing files |

### Optimization Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `--max-tokens` | integer | `200000` | Maximum token budget |
| `--optimization-strategy` | string | none | Strategy: `proportional`, `hierarchical`, `relevance` |
| `--token-counter` | string | `approximate` | Counter: `approximate`, `gpt4` |
| `--format` | string | `markdown` | Format: `markdown`, `json` |

### Examples

**Basic generation without optimization:**
```bash
./bin/laravel-context-pack generate --project=/path/to/laravel
```

**With token budget and strategy:**
```bash
./bin/laravel-context-pack generate \
  --project=/path/to/laravel \
  --max-tokens=128000 \
  --optimization-strategy=proportional
```

**Full optimization with JSON output:**
```bash
./bin/laravel-context-pack generate \
  --project=/path/to/laravel \
  --max-tokens=200000 \
  --optimization-strategy=hierarchical \
  --token-counter=gpt4 \
  --format=json \
  --output=/custom/output
```

## Optimization Settings

### Token Budget (`max_tokens`)

Controls the maximum number of tokens in the generated output.

**Configuration:**
```php
'optimization' => [
    'max_tokens' => env('CONTEXT_MAX_TOKENS', 200000),
],
```

**Recommended values:**

| LLM | Context Window | Recommended Budget | Notes |
|-----|---------------|-------------------|-------|
| Claude 3.5 Sonnet | 200k | 180k | Leave 20k for prompts |
| GPT-4 Turbo | 128k | 110k | Leave 18k for prompts |
| GPT-3.5 Turbo | 16k | 14k | Leave 2k for prompts |
| Gemini 1.5 Pro | 1M | 900k | Large headroom |

### Token Counter (`token_counter`)

Determines how tokens are counted.

**Configuration:**
```php
'optimization' => [
    'token_counter' => env('CONTEXT_TOKEN_COUNTER', 'approximate'),
],
```

**Options:**

1. **`approximate`** (Default)
   - Method: 4 characters ≈ 1 token
   - Speed: Fast
   - Accuracy: ±10-15%
   - Best for: Quick iterations, development

2. **`gpt4`**
   - Method: GPT-4 tokenizer approximation
   - Speed: Moderate
   - Accuracy: ±5%
   - Best for: Production, precise budgets

### Optimization Strategy (`strategy`)

Determines how content is summarized when exceeding token budget.

**Configuration:**
```php
'optimization' => [
    'strategy' => env('CONTEXT_OPTIMIZATION_STRATEGY', 'proportional'),
],
```

**Options:**

1. **`proportional`** (Default)
   - Allocates budget proportionally based on content size
   - Preserves all generators with proportional truncation
   - Best for: Balanced, general-purpose context

2. **`hierarchical`**
   - Structures content in layers: summary → insights → details
   - Preserves structure while removing details
   - Best for: Exploratory analysis, progressive disclosure

3. **`relevance`**
   - Prioritizes by relevance score, highest first
   - May exclude entire generators if budget limited
   - Best for: Focused tasks, critical information

### Generator Budget Allocation

Control per-generator token allocation (percentages must sum to ≤ 1.0):

**Configuration:**
```php
'optimization' => [
    'generator_budgets' => [
        'schema' => 0.15,        // 15% for database schema
        'routes' => 0.25,        // 25% for route summary
        'models' => 0.30,        // 30% for model overview
        'static_analysis' => 0.20, // 20% for static analysis
        'other' => 0.10,         // 10% buffer for other generators
    ],
],
```

**Example with 100k token budget:**
- Schema: 15k tokens
- Routes: 25k tokens
- Models: 30k tokens
- Static Analysis: 20k tokens
- Other: 10k tokens

### Hierarchical Strategy Levels

Configure allocation across hierarchical levels (only used with `hierarchical` strategy):

**Configuration:**
```php
'optimization' => [
    'hierarchical_levels' => [
        'summary' => 0.10,    // 10% for executive summary
        'insights' => 0.25,   // 25% for key insights
        'details' => 0.65,    // 65% for detailed content
    ],
],
```

## Output Format Settings

### Default Format

**Configuration:**
```php
'output' => [
    'format' => env('CONTEXT_OUTPUT_FORMAT', 'markdown'),
],
```

**Options:**
- `markdown`: Human-readable Markdown (default)
- `json`: Structured JSON for programmatic consumption

### Markdown Options

**Configuration:**
```php
'output' => [
    'markdown' => [
        'include_metadata' => true,      // Include metadata header
        'include_token_stats' => true,   // Include token usage statistics
    ],
],
```

**Example output with metadata:**
```markdown
<!-- Context Pack Metadata
Generated: 2024-10-05T10:30:00+00:00
Optimization Strategy: proportional
Truncated: Yes
-->

## Token Usage Statistics
- **Original Tokens**: 150,000
- **Optimized Tokens**: 100,000
...
```

### JSON Options

**Configuration:**
```php
'output' => [
    'json' => [
        'pretty_print' => true,  // Pretty-print JSON (vs. minified)
    ],
],
```

## Generator Settings

Control which generators are enabled and their relevance scores:

**Configuration:**
```php
'generators' => [
    'schema_dump' => [
        'enabled' => true,
        'relevance_score' => 0.9,
    ],
    'route_summary' => [
        'enabled' => true,
        'relevance_score' => 1.0,  // Highest priority
    ],
    'model_overview' => [
        'enabled' => true,
        'relevance_score' => 1.0,  // Highest priority
    ],
    'static_analysis' => [
        'enabled' => true,
        'relevance_score' => 0.7,  // Lower priority
    ],
],
```

**Relevance scores** (0.0 - 1.0):
- `1.0`: Critical (always included if possible)
- `0.7-0.9`: Important (included when budget allows)
- `0.5-0.6`: Optional (may be excluded)
- `< 0.5`: Low priority (likely excluded)

## Performance Settings

### Execution Time Limit

**Configuration:**
```php
'performance' => [
    'max_execution_time' => 300,  // 5 minutes (0 = no limit)
],
```

**Recommendations:**
- Small projects (<100 files): 60 seconds
- Medium projects (100-500 files): 180 seconds
- Large projects (>500 files): 300-600 seconds

### Memory Limit

**Configuration:**
```php
'performance' => [
    'memory_limit' => null,  // null = use PHP default
],
```

**Recommendations:**
- Small projects: 128M
- Medium projects: 256M
- Large projects: 512M-1G

## Configuration Validation

Laravel Context Pack validates configuration on load. Common validation errors:

### Generator Budgets Don't Sum to ≤ 1.0

**Error:**
```
Configuration error: generator_budgets must sum to ≤ 1.0 (got 1.15)
```

**Fix:**
```php
'generator_budgets' => [
    'schema' => 0.15,
    'routes' => 0.25,
    'models' => 0.30,
    'static_analysis' => 0.20,
    'other' => 0.10,  // Total: 1.00 ✓
],
```

### Invalid Token Counter

**Error:**
```
Invalid token counter: 'tiktoken' (must be 'approximate' or 'gpt4')
```

**Fix:**
```php
'token_counter' => 'gpt4',  // Valid option
```

### Invalid Strategy

**Error:**
```
Invalid optimization strategy: 'smart' (must be 'proportional', 'hierarchical', or 'relevance')
```

**Fix:**
```php
'strategy' => 'proportional',  // Valid option
```

## Advanced Configuration

### Custom Token Counter

Implement `TokenCounter` interface:

```php
use LaravelContextPack\Optimizer\TokenCounter;

class CustomTokenCounter implements TokenCounter
{
    public function count(string $text): int
    {
        // Custom logic
        return strlen($text) / 3;
    }

    public function getName(): string
    {
        return 'custom';
    }
}
```

### Custom Summarization Strategy

Implement `SummarizationStrategy` interface:

```php
use LaravelContextPack\Optimizer\SummarizationStrategy;
use LaravelContextPack\Optimizer\OptimizedContext;
use LaravelContextPack\Optimizer\TokenCounter;

class CustomStrategy implements SummarizationStrategy
{
    public function summarize(array $outputs, int $tokenBudget, TokenCounter $counter): OptimizedContext
    {
        // Custom summarization logic
    }

    public function getName(): string
    {
        return 'custom';
    }

    public function getDescription(): string
    {
        return 'Custom summarization strategy';
    }
}
```

### Custom Output Formatter

Implement `OutputFormatter` interface:

```php
use LaravelContextPack\Formatter\OutputFormatter;
use LaravelContextPack\Optimizer\OptimizedContext;

class CustomFormatter implements OutputFormatter
{
    public function format(OptimizedContext $context, array $metadata = []): string
    {
        // Custom formatting logic
    }

    public function getExtension(): string
    {
        return 'custom';
    }

    public function getName(): string
    {
        return 'custom';
    }

    public function getMimeType(): string
    {
        return 'application/custom';
    }
}
```

## Configuration Examples

### Example 1: Claude 3.5 Sonnet Integration

```php
// config/context-pack.php
return [
    'optimization' => [
        'enabled' => true,
        'max_tokens' => 180000,  // 200k window - 20k headroom
        'token_counter' => 'approximate',
        'strategy' => 'hierarchical',
    ],
    'output' => [
        'format' => 'json',
        'json' => ['pretty_print' => true],
    ],
];
```

### Example 2: GPT-4 Turbo with Relevance Priority

```php
return [
    'optimization' => [
        'enabled' => true,
        'max_tokens' => 110000,  // 128k window - 18k headroom
        'token_counter' => 'gpt4',
        'strategy' => 'relevance',
    ],
    'generators' => [
        'route_summary' => ['relevance_score' => 1.0],
        'model_overview' => ['relevance_score' => 1.0],
        'schema_dump' => ['relevance_score' => 0.8],
        'static_analysis' => ['relevance_score' => 0.6],
    ],
];
```

### Example 3: Development Mode (No Optimization)

```php
return [
    'optimization' => [
        'enabled' => false,
    ],
    'output' => [
        'format' => 'markdown',
        'markdown' => [
            'include_metadata' => true,
            'include_token_stats' => false,
        ],
    ],
];
```

## See Also

- [AI Integration Guide](ai-integration.md) - LLM-specific integration examples
- [Generator Documentation](generators.md) - Individual generator configuration
- [Troubleshooting](ai-integration.md#troubleshooting) - Common issues and solutions
