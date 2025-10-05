<?php

declare(strict_types=1);

namespace LaravelContextPack\Optimizer;

/**
 * Approximate token counter using character-based heuristic.
 *
 * Uses a simple approximation: ~4 characters per token.
 * This is a rough estimate that works reasonably well for English text.
 *
 * Accuracy: ±10-15% compared to actual tokenizers.
 */
final class ApproximateTokenCounter implements TokenCounter
{
    private const CHARS_PER_TOKEN = 4.0;

    public function count(string $text): int
    {
        $charCount = mb_strlen($text, 'UTF-8');

        return (int) ceil($charCount / self::CHARS_PER_TOKEN);
    }

    public function getName(): string
    {
        return 'approximate';
    }
}
