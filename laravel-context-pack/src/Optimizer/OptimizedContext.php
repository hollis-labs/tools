<?php

declare(strict_types=1);

namespace LaravelContextPack\Optimizer;

/**
 * Data Transfer Object representing optimized context output.
 *
 * Contains the optimized generator outputs along with metadata about
 * the optimization process (token usage, truncation, etc.).
 */
final class OptimizedContext
{
    /**
     * @param list<GeneratorOutput> $outputs Optimized generator outputs
     * @param array{originalTokens: int, optimizedTokens: int, strategy: string, truncated: bool} $metadata
     */
    public function __construct(
        public readonly array $outputs,
        public readonly array $metadata,
    ) {
    }

    /**
     * Get total token count of optimized content.
     */
    public function getTotalTokens(): int
    {
        return $this->metadata['optimizedTokens'];
    }

    /**
     * Check if content was truncated during optimization.
     */
    public function wasTruncated(): bool
    {
        return $this->metadata['truncated'];
    }

    /**
     * Get the optimization strategy used.
     */
    public function getStrategy(): string
    {
        return $this->metadata['strategy'];
    }

    /**
     * Get all outputs as an associative array keyed by generator name.
     *
     * @return array<string, GeneratorOutput>
     */
    public function getOutputsByName(): array
    {
        $result = [];
        foreach ($this->outputs as $output) {
            $result[$output->generatorName] = $output;
        }
        return $result;
    }

    /**
     * Convert to array representation.
     *
     * @return array<string, mixed>
     */
    public function toArray(): array
    {
        return [
            'outputs' => array_map(fn (GeneratorOutput $output) => $output->toArray(), $this->outputs),
            'metadata' => $this->metadata,
        ];
    }
}
