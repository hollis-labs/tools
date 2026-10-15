# AI Integration Guide

This guide explains how to use Laravel Context Pack's AI-optimized features to generate context that fits within LLM token limits and is optimized for AI consumption.

## Table of Contents

- [Overview](#overview)
- [Quick Start](#quick-start)
- [Token Counting](#token-counting)
- [Optimization Strategies](#optimization-strategies)
- [Output Formats](#output-formats)
- [LLM Integration Examples](#llm-integration-examples)
- [Configuration](#configuration)
- [Troubleshooting](#troubleshooting)

## Overview

Laravel Context Pack now includes **AI-optimized context generation** with:

- **Token-aware generation**: Respects LLM token limits (Claude, GPT-4, etc.)
- **Smart summarization**: Multiple strategies for reducing context size
- **Multiple output formats**: Markdown (human-readable) and JSON (machine-parseable)
- **Token usage statistics**: Track original vs. optimized token counts
- **Configurable budgets**: Set global and per-generator token allocations

### Key Features

✅ **Automatic Token Counting**: Approximate or GPT-4 tokenizer
✅ **3 Summarization Strategies**: Proportional, Hierarchical, Relevance-based
✅ **2 Output Formats**: Markdown and JSON
✅ **Backward Compatible**: Optimization is optional
✅ **Performance**: <10% overhead with optimization enabled

## Quick Start

### Basic Usage (No Optimization)

Generate context without any token limits:

```bash
./bin/laravel-context-pack generate --project=/path/to/laravel
```

### With Token Budget

Limit output to 128k tokens (GPT-4 Turbo):

```bash
./bin/laravel-context-pack generate \
  --project=/path/to/laravel \
  --max-tokens=128000 \
  --optimization-strategy=proportional
```

### With Custom Format

Generate JSON output for programmatic consumption:

```bash
./bin/laravel-context-pack generate \
  --project=/path/to/laravel \
  --max-tokens=200000 \
  --optimization-strategy=hierarchical \
  --format=json
```

### All Options Combined

```bash
./bin/laravel-context-pack generate \
  --project=/path/to/laravel \
  --max-tokens=128000 \
  --optimization-strategy=relevance \
  --token-counter=gpt4 \
  --format=json \
  --output=/path/to/output
```

## Token Counting

Laravel Context Pack supports two token counting methods:

### 1. Approximate Token Counter (Default)

**Speed**: Fast
**Accuracy**: ±10-15%
**Method**: 4 characters ≈ 1 token

```bash
--token-counter=approximate
```

**Best for**: Quick iterations, large codebases, Claude 3.5 Sonnet

### 2. GPT-4 Token Counter

**Speed**: Moderate
**Accuracy**: ±5%
**Method**: GPT-4 tokenizer approximation

```bash
--token-counter=gpt4
```

**Best for**: Production use, precise budgets, GPT-4 integration

### Token Budget Recommendations

| LLM Model | Context Window | Recommended Budget | CLI Flag |
|-----------|---------------|-------------------|----------|
| Claude 3.5 Sonnet | 200k tokens | 180k tokens | `--max-tokens=180000` |
| GPT-4 Turbo | 128k tokens | 110k tokens | `--max-tokens=110000` |
| GPT-3.5 Turbo | 16k tokens | 14k tokens | `--max-tokens=14000` |
| Gemini 1.5 Pro | 1M tokens | 900k tokens | `--max-tokens=900000` |

**Note**: Leave 10-20% headroom for system prompts and conversation history.

## Optimization Strategies

### 1. Proportional Strategy

**When to use**: Preserve relative importance of all generators
**How it works**: Allocates budget proportionally based on original content size

```bash
--optimization-strategy=proportional
```

**Example**:
- Original: Schema (30k), Routes (50k), Models (70k) = 150k total
- Budget: 100k tokens
- Result: Schema (20k), Routes (33k), Models (47k) = 100k total

**Best for**: Balanced context, general-purpose LLM tasks

### 2. Hierarchical Strategy

**When to use**: Need quick summary with optional details
**How it works**: Structures content as summary (10%) → insights (25%) → details (65%)

```bash
--optimization-strategy=hierarchical
```

**Example Structure**:
```markdown
## Summary (10% of budget)
Key information and overview

## Insights (25% of budget)
Important patterns and highlights

## Details (65% of budget)
Complete information (truncated if needed)
```

**Best for**: Exploratory analysis, progressive disclosure, large codebases

### 3. Relevance Strategy

**When to use**: Prioritize most important information
**How it works**: Ranks outputs by relevance score, includes highest-scoring content first

```bash
--optimization-strategy=relevance
```

**Example**:
- Routes (relevance: 1.0) → Included fully
- Models (relevance: 1.0) → Included fully
- Schema (relevance: 0.9) → Included partially
- Static Analysis (relevance: 0.7) → Excluded

**Best for**: Focused tasks, API documentation, critical path analysis

### Strategy Comparison

| Strategy | Pros | Cons | Use Case |
|----------|------|------|----------|
| **Proportional** | Fair allocation, preserves all generators | May truncate important content | General-purpose |
| **Hierarchical** | Structured layers, progressive detail | Requires well-structured content | Exploratory analysis |
| **Relevance** | Prioritizes key information | May exclude entire generators | Focused tasks |

## Output Formats

### Markdown Format (Default)

Human-readable format with metadata and token statistics.

```bash
--format=markdown
```

**Output**: `optimized-context.md`

**Example**:
```markdown
<!-- Context Pack Metadata
Generated: 2024-10-05T10:30:00+00:00
Optimization Strategy: proportional
Truncated: Yes
-->

## Token Usage Statistics

- **Original Tokens**: 150,000
- **Optimized Tokens**: 100,000
- **Tokens Saved**: 50,000 (33.3%)
- **Strategy**: proportional

---

## schemaDump

[Schema content here...]

---

## routeSummary

[Route content here...]
```

### JSON Format

Structured format for programmatic consumption.

```bash
--format=json
```

**Output**: `optimized-context.json`

**Example**:
```json
{
  "metadata": {
    "generatedAt": "2024-10-05T10:30:00+00:00",
    "formatVersion": "1.0"
  },
  "optimization": {
    "strategy": "proportional",
    "originalTokens": 150000,
    "optimizedTokens": 100000,
    "truncated": true,
    "tokensSaved": 50000
  },
  "outputs": [
    {
      "generator": "schemaDump",
      "relevanceScore": 0.9,
      "content": "...",
      "metadata": {
        "fileName": "01-schema-dump.sql",
        "status": "success"
      },
      "tokenCount": 20000
    }
  ]
}
```

## LLM Integration Examples

### Claude 3.5 Sonnet (Anthropic API)

```python
import anthropic
import json

# Generate optimized context
# ./bin/laravel-context-pack generate --max-tokens=180000 --format=json

# Load context
with open('context-pack/optimized-context.json', 'r') as f:
    context = json.load(f)

# Extract content
context_text = "\n\n".join([
    f"## {output['generator']}\n{output['content']}"
    for output in context['outputs']
])

# Call Claude API
client = anthropic.Anthropic(api_key="your-api-key")

message = client.messages.create(
    model="claude-3-5-sonnet-20241022",
    max_tokens=4096,
    messages=[
        {
            "role": "user",
            "content": f"""Here is the context for a Laravel application:

{context_text}

Based on this context, please analyze the application architecture and suggest improvements."""
        }
    ]
)

print(message.content)
```

### GPT-4 (OpenAI API)

```python
from openai import OpenAI

# Generate optimized context
# ./bin/laravel-context-pack generate --max-tokens=110000 --token-counter=gpt4 --format=json

# Load context
with open('context-pack/optimized-context.json', 'r') as f:
    context = json.load(f)

# Extract content
context_text = "\n\n".join([
    f"## {output['generator']}\n{output['content']}"
    for output in context['outputs']
])

# Call OpenAI API
client = OpenAI(api_key="your-api-key")

response = client.chat.completions.create(
    model="gpt-4-turbo",
    messages=[
        {
            "role": "system",
            "content": "You are a Laravel expert analyzing application architecture."
        },
        {
            "role": "user",
            "content": f"Here is the context:\n\n{context_text}\n\nAnalyze the code quality."
        }
    ]
)

print(response.choices[0].message.content)
```

### LangChain Integration

```python
from langchain.chat_models import ChatAnthropic
from langchain.schema import HumanMessage, SystemMessage
import json

# Load optimized context
with open('context-pack/optimized-context.json', 'r') as f:
    context = json.load(f)

# Create context prompt
context_text = "\n\n".join([
    f"## {output['generator']}\n{output['content']}"
    for output in context['outputs']
])

# Initialize LangChain
chat = ChatAnthropic(model="claude-3-5-sonnet-20241022")

messages = [
    SystemMessage(content="You are a Laravel architecture expert."),
    HumanMessage(content=f"Context:\n{context_text}\n\nQuestion: What are the main security concerns?")
]

response = chat(messages)
print(response.content)
```

## Configuration

### Environment Variables

Set in `.env`:

```env
# Enable optimization
CONTEXT_OPTIMIZATION_ENABLED=true

# Token budget
CONTEXT_MAX_TOKENS=200000

# Token counter (approximate|gpt4)
CONTEXT_TOKEN_COUNTER=approximate

# Strategy (proportional|hierarchical|relevance)
CONTEXT_OPTIMIZATION_STRATEGY=proportional

# Output format (markdown|json)
CONTEXT_OUTPUT_FORMAT=markdown
```

### Configuration File

Edit `config/context-pack.php`:

```php
return [
    'optimization' => [
        'enabled' => true,
        'max_tokens' => 200000,
        'token_counter' => 'approximate',
        'strategy' => 'proportional',

        // Per-generator allocation
        'generator_budgets' => [
            'schema' => 0.15,        // 15%
            'routes' => 0.25,        // 25%
            'models' => 0.30,        // 30%
            'static_analysis' => 0.20, // 20%
            'other' => 0.10,         // 10%
        ],
    ],

    'output' => [
        'format' => 'markdown',
        'markdown' => [
            'include_metadata' => true,
            'include_token_stats' => true,
        ],
    ],
];
```

## Troubleshooting

### Issue: Output still exceeds token budget

**Symptoms**: Generated context larger than `--max-tokens`
**Cause**: Token counter accuracy (~10-15% for approximate)

**Solutions**:
1. Use more accurate counter: `--token-counter=gpt4`
2. Reduce budget by 10-20%: `--max-tokens=160000` instead of 200000
3. Use more aggressive strategy: `--optimization-strategy=relevance`

### Issue: Important content truncated

**Symptoms**: Key information missing from output
**Cause**: Strategy prioritization

**Solutions**:
1. Increase token budget: `--max-tokens=250000`
2. Switch strategy: `--optimization-strategy=hierarchical` (preserves summary)
3. Disable optimization for specific run
4. Adjust generator budgets in `config/context-pack.php`

### Issue: JSON parsing errors

**Symptoms**: Invalid JSON output
**Cause**: Special characters in content

**Solutions**:
1. Check for unescaped quotes in generated content
2. Validate with: `jq . optimized-context.json`
3. Report issue with sample content

### Issue: Slow generation with GPT-4 counter

**Symptoms**: Long execution time
**Cause**: GPT-4 tokenizer overhead

**Solutions**:
1. Use approximate counter: `--token-counter=approximate`
2. Reduce codebase size (exclude tests, vendor)
3. Use proportional strategy (single-pass)

### Issue: Different token counts across runs

**Symptoms**: Token counts vary between runs
**Cause**: Content changes, counter variations

**Solutions**:
1. Use same counter consistently
2. Lock dependencies (composer.lock)
3. Check for dynamic content (timestamps)

## Best Practices

### 1. Development vs. Production

**Development**:
```bash
# Fast iteration, approximate counts
--token-counter=approximate --optimization-strategy=proportional
```

**Production**:
```bash
# Accurate counts, optimal strategy
--token-counter=gpt4 --optimization-strategy=relevance
```

### 2. Token Budget Sizing

- **Start conservative**: Use 70-80% of LLM context window
- **Measure actual usage**: Check `optimizedTokens` in output
- **Adjust iteratively**: Increase/decrease budget based on needs

### 3. Strategy Selection

1. **First time analyzing codebase**: Use `hierarchical` (get summary first)
2. **Focused task (e.g., API docs)**: Use `relevance` (prioritize routes)
3. **General context**: Use `proportional` (balanced view)

### 4. Output Format

- **Human review**: Use `markdown` format
- **API integration**: Use `json` format
- **Both**: Generate twice with different `--format` values

### 5. Performance Optimization

- **Large codebases**: Use `approximate` counter
- **CI/CD pipelines**: Cache generated context
- **Frequent regeneration**: Disable optimization (`--max-tokens` not set)

## Additional Resources

- [Configuration Guide](configuration.md)
- [Generator Documentation](generators.md)
- [API Reference](api.md)
- [GitHub Issues](https://github.com/chrispian/laravel-context-pack/issues)

## Support

For questions and support:

- 📧 Email: support@example.com
- 💬 Discord: [Join our community](https://discord.gg/example)
- 🐛 Bug Reports: [GitHub Issues](https://github.com/chrispian/laravel-context-pack/issues)
