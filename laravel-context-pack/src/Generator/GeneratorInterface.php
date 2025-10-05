<?php

declare(strict_types=1);

namespace LaravelContextPack\Generator;

/**
 * Contract for all context generators.
 *
 * Generators are responsible for analyzing specific aspects of a Laravel
 * codebase and producing documentation/context output.
 *
 * Example implementation:
 *
 * ```php
 * class ApiDocumentationGenerator implements GeneratorInterface
 * {
 *     public function getName(): string
 *     {
 *         return 'api-documentation';
 *     }
 *
 *     public function getDescription(): string
 *     {
 *         return 'Generate API endpoint documentation from routes and controllers';
 *     }
 *
 *     public function generate(string $projectPath, string $outputPath): array
 *     {
 *         // Implementation...
 *         return [
 *             'status' => 'success',
 *             'message' => 'API documentation generated',
 *             'endpoints' => 42,
 *         ];
 *     }
 *
 *     public function canRun(string $projectPath): bool
 *     {
 *         return is_dir($projectPath . '/app/Http/Controllers/Api');
 *     }
 * }
 * ```
 */
interface GeneratorInterface
{
    /**
     * Get the unique identifier for this generator.
     *
     * This should be a lowercase, hyphenated string (e.g., 'model-overview').
     * Used for registry lookups and CLI --generator option.
     */
    public function getName(): string;

    /**
     * Get a human-readable description of what this generator does.
     *
     * Displayed in help text and documentation.
     */
    public function getDescription(): string;

    /**
     * Execute the generator and produce output.
     *
     * @param string $projectPath Absolute path to the Laravel project root
     * @param string $outputPath Absolute path to the output directory
     *
     * @return array{status: string, message: string, ...} Result with status (success|skipped|failure) and message
     *
     * @throws \LaravelContextPack\Generator\Exception\GeneratorException on fatal errors
     */
    public function generate(string $projectPath, string $outputPath): array;

    /**
     * Check if this generator can run on the given project.
     *
     * Optional pre-check to determine if the generator should run.
     * Return false if required directories/files are missing.
     *
     * @param string $projectPath Absolute path to the Laravel project root
     */
    public function canRun(string $projectPath): bool;

    /**
     * Get the configuration schema for this generator.
     *
     * Optional configuration options that can be passed to the generator.
     * Returns an array describing expected configuration keys and types.
     *
     * Example:
     * ```php
     * return [
     *     'include_examples' => ['type' => 'boolean', 'default' => true],
     *     'format' => ['type' => 'string', 'enum' => ['openapi', 'markdown']],
     * ];
     * ```
     *
     * @return array<string, array<string, mixed>>
     */
    public function getConfigSchema(): array;
}
