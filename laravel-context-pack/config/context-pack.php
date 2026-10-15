<?php

declare(strict_types=1);

/**
 * Laravel Context Pack Configuration
 *
 * This file contains configuration options for the Laravel Context Pack
 * generator, including optimization settings, token budgets, and output formats.
 */

return [

    /*
    |--------------------------------------------------------------------------
    | Optimization Settings
    |--------------------------------------------------------------------------
    |
    | Configure token-aware optimization and summarization for generated context.
    | These settings help ensure the generated context fits within LLM token limits.
    |
    */

    'optimization' => [

        /*
        | Enable or disable optimization globally.
        | When false, optimization features are skipped regardless of other settings.
        */
        'enabled' => env('CONTEXT_OPTIMIZATION_ENABLED', false),

        /*
        | Maximum token budget for the entire generated context.
        | Common values:
        | - Claude 3.5 Sonnet: 200000
        | - GPT-4 Turbo: 128000
        | - GPT-3.5 Turbo: 16000
        */
        'max_tokens' => env('CONTEXT_MAX_TOKENS', 200000),

        /*
        | Token counter to use for estimation.
        | Options:
        | - 'approximate': Simple 4 chars/token heuristic (fast, ±10-15% accuracy)
        | - 'gpt4': GPT-4 tokenizer approximation (slower, ±5% accuracy)
        */
        'token_counter' => env('CONTEXT_TOKEN_COUNTER', 'approximate'),

        /*
        | Summarization strategy when content exceeds token budget.
        | Options:
        | - 'proportional': Allocate budget proportionally based on content size
        | - 'hierarchical': Structure content in layers (summary → insights → details)
        | - 'relevance': Prioritize by relevance score (highest first)
        | - null: No summarization (truncate if needed)
        */
        'strategy' => env('CONTEXT_OPTIMIZATION_STRATEGY', 'proportional'),

        /*
        | Generator-specific token budget allocation (percentages).
        | Must sum to ≤ 1.0. Remaining budget is distributed proportionally.
        */
        'generator_budgets' => [
            'schema' => 0.15,        // 15% for database schema
            'routes' => 0.25,        // 25% for route summary
            'models' => 0.30,        // 30% for model overview
            'static_analysis' => 0.20, // 20% for static analysis
            'other' => 0.10,         // 10% buffer for other generators
        ],

        /*
        | Hierarchical strategy settings.
        | Configure the allocation across summary levels.
        */
        'hierarchical_levels' => [
            'summary' => 0.10,    // 10% for executive summary
            'insights' => 0.25,   // 25% for key insights
            'details' => 0.65,    // 65% for detailed content
        ],

    ],

    /*
    |--------------------------------------------------------------------------
    | Output Format Settings
    |--------------------------------------------------------------------------
    |
    | Configure output format and formatting options.
    |
    */

    'output' => [

        /*
        | Default output format.
        | Options: 'markdown', 'json'
        */
        'format' => env('CONTEXT_OUTPUT_FORMAT', 'markdown'),

        /*
        | Markdown formatter options
        */
        'markdown' => [
            'include_metadata' => true,
            'include_token_stats' => true,
        ],

        /*
        | JSON formatter options
        */
        'json' => [
            'pretty_print' => true,
        ],

    ],

    /*
    |--------------------------------------------------------------------------
    | Generator Settings
    |--------------------------------------------------------------------------
    |
    | Configure which generators are enabled and their specific settings.
    |
    */

    'generators' => [

        'schema_dump' => [
            'enabled' => true,
            'relevance_score' => 0.9,
        ],

        'route_summary' => [
            'enabled' => true,
            'relevance_score' => 1.0,
        ],

        'model_overview' => [
            'enabled' => true,
            'relevance_score' => 1.0,
        ],

        'static_analysis' => [
            'enabled' => true,
            'relevance_score' => 0.7,
        ],

    ],

    /*
    |--------------------------------------------------------------------------
    | Performance Settings
    |--------------------------------------------------------------------------
    |
    | Configure performance-related options.
    |
    */

    'performance' => [

        /*
        | Maximum execution time for generators (seconds)
        | 0 = no limit
        */
        'max_execution_time' => 300,

        /*
        | Memory limit for generation process
        | null = use PHP default
        */
        'memory_limit' => null,

    ],

];
