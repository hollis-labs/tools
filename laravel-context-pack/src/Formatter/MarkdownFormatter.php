<?php

declare(strict_types=1);

namespace LaravelContextPack\Formatter;

use LaravelContextPack\Optimizer\OptimizedContext;

/**
 * Markdown output formatter.
 *
 * Formats optimized context as human-readable Markdown with
 * metadata embedded as comments or front-matter.
 */
final class MarkdownFormatter implements OutputFormatter
{
    public function __construct(
        private bool $includeMetadata = true,
        private bool $includeTokenStats = true
    ) {
    }

    public function format(OptimizedContext $context, array $metadata = []): string
    {
        $parts = [];

        // Add metadata header if requested
        if ($this->includeMetadata) {
            $parts[] = $this->formatMetadataHeader($context, $metadata);
        }

        // Add token usage statistics if requested
        if ($this->includeTokenStats) {
            $parts[] = $this->formatTokenStats($context);
        }

        // Add generator outputs
        foreach ($context->outputs as $output) {
            $parts[] = $this->formatGeneratorOutput($output);
        }

        return implode("\n\n---\n\n", array_filter($parts));
    }

    public function getExtension(): string
    {
        return 'md';
    }

    public function getName(): string
    {
        return 'markdown';
    }

    public function getMimeType(): string
    {
        return 'text/markdown';
    }

    /**
     * Format metadata header.
     *
     * @param array<string, mixed> $metadata
     */
    private function formatMetadataHeader(OptimizedContext $context, array $metadata): string
    {
        $lines = [];
        $lines[] = '<!-- Context Pack Metadata';
        $lines[] = 'Generated: ' . ($metadata['generatedAt'] ?? date('c'));
        $lines[] = 'Optimization Strategy: ' . $context->getStrategy();
        $lines[] = 'Truncated: ' . ($context->wasTruncated() ? 'Yes' : 'No');
        $lines[] = '-->';

        return implode("\n", $lines);
    }

    /**
     * Format token usage statistics.
     */
    private function formatTokenStats(OptimizedContext $context): string
    {
        $lines = [];
        $lines[] = '## Token Usage Statistics';
        $lines[] = '';
        $lines[] = sprintf('- **Original Tokens**: %s', number_format($context->metadata['originalTokens']));
        $lines[] = sprintf('- **Optimized Tokens**: %s', number_format($context->metadata['optimizedTokens']));

        if ($context->wasTruncated()) {
            $saved = $context->metadata['originalTokens'] - $context->metadata['optimizedTokens'];
            $percentage = ($saved / $context->metadata['originalTokens']) * 100;
            $lines[] = sprintf('- **Tokens Saved**: %s (%.1f%%)', number_format($saved), $percentage);
        }

        $lines[] = sprintf('- **Strategy**: %s', $context->getStrategy());

        return implode("\n", $lines);
    }

    /**
     * Format a single generator output.
     */
    private function formatGeneratorOutput(\LaravelContextPack\Optimizer\GeneratorOutput $output): string
    {
        $lines = [];

        // Generator header
        $lines[] = sprintf('## %s', ucfirst($output->generatorName));

        // Add relevance score if not default
        if ($output->relevanceScore !== 1.0) {
            $lines[] = sprintf('_Relevance Score: %.2f_', $output->relevanceScore);
        }

        // Add truncation notice if applicable
        if (isset($output->metadata['truncated']) && $output->metadata['truncated']) {
            $lines[] = '';
            $lines[] = '> **Note**: This output was truncated to fit within token budget.';
        }

        $lines[] = '';
        $lines[] = $output->content;

        return implode("\n", $lines);
    }
}
