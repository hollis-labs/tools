<?php

declare(strict_types=1);

namespace LaravelContextPack\Optimizer\Strategy;

use LaravelContextPack\Optimizer\GeneratorOutput;
use LaravelContextPack\Optimizer\OptimizedContext;
use LaravelContextPack\Optimizer\SummarizationStrategy;
use LaravelContextPack\Optimizer\TokenCounter;

/**
 * Proportional summarization strategy.
 *
 * Allocates token budget proportionally across generators based on their
 * original size. Larger outputs get more tokens, smaller outputs get fewer.
 * This maintains the relative importance of each generator's output.
 */
final class ProportionalStrategy implements SummarizationStrategy
{
    public function summarize(array $outputs, int $tokenBudget, TokenCounter $counter): OptimizedContext
    {
        if ($outputs === []) {
            return new OptimizedContext([], [
                'originalTokens' => 0,
                'optimizedTokens' => 0,
                'strategy' => $this->getName(),
                'truncated' => false,
            ]);
        }

        // Calculate current token counts for each output
        $tokenCounts = [];
        $totalTokens = 0;
        foreach ($outputs as $output) {
            $tokens = $counter->count($output->content);
            $tokenCounts[$output->generatorName] = $tokens;
            $totalTokens += $tokens;
        }

        // If already within budget, return as-is
        if ($totalTokens <= $tokenBudget) {
            return new OptimizedContext($outputs, [
                'originalTokens' => $totalTokens,
                'optimizedTokens' => $totalTokens,
                'strategy' => $this->getName(),
                'truncated' => false,
            ]);
        }

        // Calculate proportional allocation
        $optimizedOutputs = [];
        $actualTotalTokens = 0;

        // First pass: calculate ideal allocations
        $allocations = [];
        foreach ($outputs as $output) {
            $originalTokens = $tokenCounts[$output->generatorName];
            $proportion = $originalTokens / $totalTokens;
            $allocatedTokens = (int) floor($tokenBudget * $proportion);
            $allocations[$output->generatorName] = max(100, $allocatedTokens);
        }

        // Adjust if total exceeds budget (can happen with min 100 tokens per generator)
        $allocationTotal = array_sum($allocations);
        if ($allocationTotal > $tokenBudget) {
            $scaleFactor = $tokenBudget / $allocationTotal;
            foreach ($allocations as $name => $tokens) {
                $allocations[$name] = (int) floor($tokens * $scaleFactor);
            }
        }

        // Second pass: create optimized outputs
        foreach ($outputs as $output) {
            $originalTokens = $tokenCounts[$output->generatorName];
            $allocatedTokens = $allocations[$output->generatorName];

            if ($originalTokens <= $allocatedTokens) {
                // Keep original content
                $optimizedOutputs[] = $output;
                $actualTotalTokens += $originalTokens;
            } else {
                // Truncate to allocated budget
                $truncatedContent = $this->truncateContent($output->content, $allocatedTokens, $counter);
                $optimizedOutputs[] = new GeneratorOutput(
                    generatorName: $output->generatorName,
                    content: $truncatedContent,
                    relevanceScore: $output->relevanceScore,
                    metadata: array_merge($output->metadata, [
                        'truncated' => true,
                        'originalTokens' => $originalTokens,
                        'allocatedTokens' => $allocatedTokens,
                    ]),
                );
                $actualTotalTokens += $counter->count($truncatedContent);
            }
        }

        return new OptimizedContext($optimizedOutputs, [
            'originalTokens' => $totalTokens,
            'optimizedTokens' => $actualTotalTokens,
            'strategy' => $this->getName(),
            'truncated' => true,
        ]);
    }

    public function getName(): string
    {
        return 'proportional';
    }

    public function getDescription(): string
    {
        return 'Allocates token budget proportionally based on original content size';
    }

    /**
     * Truncate content to fit target token count.
     */
    private function truncateContent(string $content, int $targetTokens, TokenCounter $counter): string
    {
        $currentTokens = $counter->count($content);

        if ($currentTokens <= $targetTokens) {
            return $content;
        }

        // Estimate character budget with safety margin
        $ratio = $targetTokens / $currentTokens;
        $charLimit = (int) (mb_strlen($content, 'UTF-8') * $ratio * 0.9); // 10% safety margin

        $truncated = mb_substr($content, 0, $charLimit, 'UTF-8');

        // Try to break at a sensible boundary (newline)
        $lastNewline = mb_strrpos($truncated, "\n");
        if ($lastNewline !== false && $lastNewline > $charLimit * 0.8) {
            $truncated = mb_substr($truncated, 0, $lastNewline);
        }

        return $truncated . "\n\n_[Content truncated to fit token budget]_";
    }
}
