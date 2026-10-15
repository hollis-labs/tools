<?php

declare(strict_types=1);

namespace LaravelContextPack\Optimizer;

/**
 * Interface for counting tokens in text.
 *
 * Different LLMs use different tokenization schemes, so implementations
 * provide approximations or exact counts for specific models.
 */
interface TokenCounter
{
    /**
     * Count tokens in the given text.
     */
    public function count(string $text): int;

    /**
     * Get the name of this token counter.
     */
    public function getName(): string;
}
