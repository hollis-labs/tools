<?php

declare(strict_types=1);

namespace LaravelContextPack\Optimizer;

/**
 * Data Transfer Object representing generator output with metadata.
 *
 * Contains the generated content along with metadata for optimization,
 * including relevance scores, complexity metrics, and token counts.
 */
final class GeneratorOutput
{
    /**
     * @param array<string, mixed> $metadata Additional metadata (complexity, stats, etc.)
     */
    public function __construct(
        public readonly string $generatorName,
        public readonly string $content,
        public readonly float $relevanceScore = 1.0,
        public readonly array $metadata = [],
    ) {
    }

    /**
     * Get the token count for this output (if available in metadata).
     */
    public function getTokenCount(): ?int
    {
        return $this->metadata['tokenCount'] ?? null;
    }

    /**
     * Create from array representation.
     *
     * @param array{generatorName: string, content: string, relevanceScore?: float, metadata?: array<string, mixed>} $data
     */
    public static function fromArray(array $data): self
    {
        return new self(
            generatorName: $data['generatorName'],
            content: $data['content'],
            relevanceScore: $data['relevanceScore'] ?? 1.0,
            metadata: $data['metadata'] ?? [],
        );
    }

    /**
     * Convert to array representation.
     *
     * @return array{generatorName: string, content: string, relevanceScore: float, metadata: array<string, mixed>}
     */
    public function toArray(): array
    {
        return [
            'generatorName' => $this->generatorName,
            'content' => $this->content,
            'relevanceScore' => $this->relevanceScore,
            'metadata' => $this->metadata,
        ];
    }
}
