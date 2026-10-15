<?php

declare(strict_types=1);

namespace LaravelContextPack\Optimizer;

/**
 * Optimizes generated context to fit within token budgets.
 *
 * Manages token counting, budget enforcement, and content summarization
 * to ensure context fits within LLM context windows.
 */
final class ContextOptimizer
{
    private int $maxTokens;

    public function __construct(
        private TokenCounter $counter,
        private ?SummarizationStrategy $strategy = null,
        int $maxTokens = 200000
    ) {
        $this->maxTokens = $maxTokens;
    }

    /**
     * Optimize generator outputs to fit within token budget.
     *
     * @param list<GeneratorOutput> $outputs
     */
    public function optimize(array $outputs): OptimizedContext
    {
        if ($this->strategy === null) {
            // No strategy - just return outputs with metadata
            $totalTokens = 0;
            foreach ($outputs as $output) {
                $totalTokens += $this->counter->count($output->content);
            }

            return new OptimizedContext($outputs, [
                'originalTokens' => $totalTokens,
                'optimizedTokens' => $totalTokens,
                'strategy' => 'none',
                'truncated' => false,
            ]);
        }

        return $this->strategy->summarize($outputs, $this->maxTokens, $this->counter);
    }

    /**
     * Estimate total tokens for given content.
     */
    public function estimateTokens(string $content): int
    {
        return $this->counter->count($content);
    }

    /**
     * Check if content fits within budget.
     */
    public function fitsWithinBudget(string $content): bool
    {
        return $this->estimateTokens($content) <= $this->maxTokens;
    }

    /**
     * Get remaining token budget after accounting for content.
     */
    public function getRemainingBudget(string $content): int
    {
        $used = $this->estimateTokens($content);
        return max(0, $this->maxTokens - $used);
    }

    /**
     * Get the current maximum token budget.
     */
    public function getMaxTokens(): int
    {
        return $this->maxTokens;
    }

    /**
     * Set a new maximum token budget.
     */
    public function setMaxTokens(int $maxTokens): void
    {
        $this->maxTokens = $maxTokens;
    }

    /**
     * Get the token counter being used.
     */
    public function getCounter(): TokenCounter
    {
        return $this->counter;
    }

    /**
     * Set the summarization strategy.
     */
    public function setStrategy(?SummarizationStrategy $strategy): void
    {
        $this->strategy = $strategy;
    }

    /**
     * Truncate content to fit within token budget.
     *
     * Simple truncation by characters, aiming for target token count.
     */
    public function truncate(string $content, int $targetTokens): string
    {
        $currentTokens = $this->estimateTokens($content);

        if ($currentTokens <= $targetTokens) {
            return $content;
        }

        // Estimate character budget
        $ratio = $targetTokens / $currentTokens;
        $charLimit = (int) (mb_strlen($content, 'UTF-8') * $ratio * 0.95); // 5% safety margin

        $truncated = mb_substr($content, 0, $charLimit, 'UTF-8');

        // Try to break at a sensible boundary
        $lastNewline = mb_strrpos($truncated, "\n");
        if ($lastNewline !== false && $lastNewline > $charLimit * 0.9) {
            $truncated = mb_substr($truncated, 0, $lastNewline);
        }

        return $truncated . "\n\n_[Content truncated to fit token budget]_";
    }
}
