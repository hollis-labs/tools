<?php

declare(strict_types=1);

namespace LaravelContextPack\Optimizer;

/**
 * Interface for context summarization strategies.
 *
 * Strategies determine how to reduce context size when it exceeds token budgets.
 * Different strategies prioritize different aspects of the content.
 */
interface SummarizationStrategy
{
    /**
     * Summarize generator outputs to fit within token budget.
     *
     * @param list<GeneratorOutput> $outputs Generator outputs to optimize
     * @param int $tokenBudget Maximum tokens allowed
     * @param TokenCounter $counter Token counter to use for estimation
     * @return OptimizedContext Optimized context fitting within budget
     */
    public function summarize(array $outputs, int $tokenBudget, TokenCounter $counter): OptimizedContext;

    /**
     * Get the strategy name.
     */
    public function getName(): string;

    /**
     * Get a description of this strategy.
     */
    public function getDescription(): string;
}
