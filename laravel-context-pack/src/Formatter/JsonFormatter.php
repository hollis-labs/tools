<?php

declare(strict_types=1);

namespace LaravelContextPack\Formatter;

use LaravelContextPack\Optimizer\OptimizedContext;

/**
 * JSON output formatter.
 *
 * Formats optimized context as structured JSON with full metadata.
 * Suitable for programmatic consumption and API integration.
 */
final class JsonFormatter implements OutputFormatter
{
    public function __construct(
        private bool $prettyPrint = true
    ) {
    }

    public function format(OptimizedContext $context, array $metadata = []): string
    {
        $data = [
            'metadata' => array_merge([
                'generatedAt' => date('c'),
                'formatVersion' => '1.0',
            ], $metadata),
            'optimization' => [
                'strategy' => $context->getStrategy(),
                'originalTokens' => $context->metadata['originalTokens'],
                'optimizedTokens' => $context->metadata['optimizedTokens'],
                'truncated' => $context->wasTruncated(),
                'tokensSaved' => $context->metadata['originalTokens'] - $context->metadata['optimizedTokens'],
            ],
            'outputs' => $this->formatOutputs($context),
        ];

        // Add strategy-specific metadata
        if (isset($context->metadata['excludedOutputs'])) {
            $data['optimization']['excludedOutputs'] = $context->metadata['excludedOutputs'];
            $data['optimization']['excludedGenerators'] = $context->metadata['excludedGenerators'];
        }

        $flags = JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE;
        if ($this->prettyPrint) {
            $flags |= JSON_PRETTY_PRINT;
        }

        $json = json_encode($data, $flags);

        if ($json === false) {
            throw new \RuntimeException('Failed to encode context as JSON: ' . json_last_error_msg());
        }

        return $json;
    }

    public function getExtension(): string
    {
        return 'json';
    }

    public function getName(): string
    {
        return 'json';
    }

    public function getMimeType(): string
    {
        return 'application/json';
    }

    /**
     * Format outputs for JSON structure.
     *
     * @return list<array<string, mixed>>
     */
    private function formatOutputs(OptimizedContext $context): array
    {
        $outputs = [];

        foreach ($context->outputs as $output) {
            $outputs[] = [
                'generator' => $output->generatorName,
                'relevanceScore' => $output->relevanceScore,
                'content' => $output->content,
                'metadata' => $output->metadata,
                'tokenCount' => $output->getTokenCount(),
            ];
        }

        return $outputs;
    }
}
