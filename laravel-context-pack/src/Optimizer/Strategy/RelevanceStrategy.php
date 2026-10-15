<?php

declare(strict_types=1);

namespace LaravelContextPack\Optimizer\Strategy;

use LaravelContextPack\Optimizer\GeneratorOutput;
use LaravelContextPack\Optimizer\OptimizedContext;
use LaravelContextPack\Optimizer\SummarizationStrategy;
use LaravelContextPack\Optimizer\TokenCounter;

/**
 * Relevance-based summarization strategy.
 *
 * Ranks outputs by relevance score and includes highest-scoring content first.
 * Continues adding content until token budget is exhausted. This ensures
 * the most important information is always included.
 */
final class RelevanceStrategy implements SummarizationStrategy
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

        // Calculate current token counts
        $totalTokens = 0;
        foreach ($outputs as $output) {
            $totalTokens += $counter->count($output->content);
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

        // Sort outputs by relevance score (highest first)
        $sortedOutputs = $outputs;
        usort($sortedOutputs, function (GeneratorOutput $a, GeneratorOutput $b): int {
            // Sort by relevance descending, then by name ascending (for stability)
            $relevanceDiff = $b->relevanceScore <=> $a->relevanceScore;
            if ($relevanceDiff !== 0) {
                return $relevanceDiff;
            }
            return $a->generatorName <=> $b->generatorName;
        });

        // Include outputs in order of relevance until budget exhausted
        $optimizedOutputs = [];
        $remainingBudget = $tokenBudget;
        $includedTokens = 0;

        foreach ($sortedOutputs as $output) {
            $outputTokens = $counter->count($output->content);

            if ($outputTokens <= $remainingBudget) {
                // Include full output
                $optimizedOutputs[] = $output;
                $remainingBudget -= $outputTokens;
                $includedTokens += $outputTokens;
            } elseif ($remainingBudget > 100) {
                // Partially include if we have reasonable budget left
                $truncatedContent = $this->truncateContent($output->content, $remainingBudget, $counter);
                $truncatedTokens = $counter->count($truncatedContent);

                $optimizedOutputs[] = new GeneratorOutput(
                    generatorName: $output->generatorName,
                    content: $truncatedContent,
                    relevanceScore: $output->relevanceScore,
                    metadata: array_merge($output->metadata, [
                        'truncated' => true,
                        'originalTokens' => $outputTokens,
                        'includedTokens' => $truncatedTokens,
                    ]),
                );

                $includedTokens += $truncatedTokens;
                $remainingBudget = 0;
                break;
            } else {
                // Not enough budget left for this output
                break;
            }
        }

        // Add metadata about excluded outputs
        $excludedCount = count($outputs) - count($optimizedOutputs);
        $excludedGenerators = array_diff(
            array_map(fn ($o) => $o->generatorName, $outputs),
            array_map(fn ($o) => $o->generatorName, $optimizedOutputs)
        );

        return new OptimizedContext($optimizedOutputs, [
            'originalTokens' => $totalTokens,
            'optimizedTokens' => $includedTokens,
            'strategy' => $this->getName(),
            'truncated' => true,
            'excludedOutputs' => $excludedCount,
            'excludedGenerators' => array_values($excludedGenerators),
        ]);
    }

    public function getName(): string
    {
        return 'relevance';
    }

    public function getDescription(): string
    {
        return 'Prioritizes content by relevance score, including highest-scoring outputs first';
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

        return $truncated . "\n\n_[Content truncated - lower priority content omitted]_";
    }
}
