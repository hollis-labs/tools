<?php

declare(strict_types=1);

namespace LaravelContextPack\Formatter;

use LaravelContextPack\Optimizer\OptimizedContext;

/**
 * Interface for output formatters.
 *
 * Formatters convert optimized context into different output formats
 * suitable for different consumption patterns (human-readable, machine-parseable, etc.).
 */
interface OutputFormatter
{
    /**
     * Format the optimized context for output.
     *
     * @param array<string, mixed> $metadata Additional metadata to include
     */
    public function format(OptimizedContext $context, array $metadata = []): string;

    /**
     * Get the file extension for this format.
     */
    public function getExtension(): string;

    /**
     * Get the format name.
     */
    public function getName(): string;

    /**
     * Get the MIME type for this format.
     */
    public function getMimeType(): string;
}
