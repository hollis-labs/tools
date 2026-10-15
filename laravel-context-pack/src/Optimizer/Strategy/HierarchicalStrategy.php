<?php

declare(strict_types=1);

namespace LaravelContextPack\Optimizer\Strategy;

use LaravelContextPack\Optimizer\GeneratorOutput;
use LaravelContextPack\Optimizer\OptimizedContext;
use LaravelContextPack\Optimizer\SummarizationStrategy;
use LaravelContextPack\Optimizer\TokenCounter;

/**
 * Hierarchical summarization strategy.
 *
 * Structures content in layers: summary (10%), insights (25%), details (65%).
 * When budget is exceeded, it progressively removes detail layers while
 * preserving the summary and key insights.
 */
final class HierarchicalStrategy implements SummarizationStrategy
{
    private const SUMMARY_ALLOCATION = 0.10;
    private const INSIGHTS_ALLOCATION = 0.25;
    private const DETAILS_ALLOCATION = 0.65;

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

        // If already within budget, still process to add hierarchical metadata
        $withinBudget = $totalTokens <= $tokenBudget;

        // Allocate budget across hierarchical levels (or use full content if within budget)
        $effectiveBudget = $withinBudget ? PHP_INT_MAX : $tokenBudget;
        $summaryBudget = (int) ($effectiveBudget * self::SUMMARY_ALLOCATION);
        $insightsBudget = (int) ($effectiveBudget * self::INSIGHTS_ALLOCATION);
        $detailsBudget = (int) ($effectiveBudget * self::DETAILS_ALLOCATION);

        $optimizedOutputs = [];
        $actualTokens = 0;

        foreach ($outputs as $output) {
            $sections = $this->extractSections($output->content);
            $optimizedContent = $this->buildHierarchicalContent(
                $sections,
                $summaryBudget,
                $insightsBudget,
                $detailsBudget,
                $counter
            );

            $outputMetadata = array_merge($output->metadata, [
                'hierarchical' => true,
                'sections' => array_keys($sections),
            ]);

            $optimizedOutputs[] = new GeneratorOutput(
                generatorName: $output->generatorName,
                content: $optimizedContent,
                relevanceScore: $output->relevanceScore,
                metadata: $outputMetadata,
            );

            $actualTokens += $counter->count($optimizedContent);
        }

        return new OptimizedContext($optimizedOutputs, [
            'originalTokens' => $totalTokens,
            'optimizedTokens' => $actualTokens,
            'strategy' => $this->getName(),
            'truncated' => ! $withinBudget && $actualTokens < $totalTokens,
        ]);
    }

    public function getName(): string
    {
        return 'hierarchical';
    }

    public function getDescription(): string
    {
        return 'Structures content in layers (summary → insights → details) with progressive detail removal';
    }

    /**
     * Extract sections from content.
     *
     * Looks for markdown headers to identify summary, insights, and detail sections.
     *
     * @return array<string, string>
     */
    private function extractSections(string $content): array
    {
        $sections = [
            'summary' => '',
            'insights' => '',
            'details' => '',
        ];

        // Try to extract based on common patterns
        $lines = explode("\n", $content);
        $currentSection = 'summary';
        $sectionContent = [];

        foreach ($lines as $line) {
            // Check for section headers
            if (preg_match('/^#{1,3}\s*(summary|overview|introduction)/i', $line)) {
                if ($sectionContent !== []) {
                    $sections[$currentSection] = implode("\n", $sectionContent);
                }
                $currentSection = 'summary';
                $sectionContent = [$line];
            } elseif (preg_match('/^#{1,3}\s*(key|insights|important|highlights)/i', $line)) {
                if ($sectionContent !== []) {
                    $sections[$currentSection] = implode("\n", $sectionContent);
                }
                $currentSection = 'insights';
                $sectionContent = [$line];
            } elseif (preg_match('/^#{1,3}\s*(details|full|complete|all)/i', $line)) {
                if ($sectionContent !== []) {
                    $sections[$currentSection] = implode("\n", $sectionContent);
                }
                $currentSection = 'details';
                $sectionContent = [$line];
            } else {
                $sectionContent[] = $line;
            }
        }

        // Add last section
        if (count($sectionContent) > 0) {
            $sections[$currentSection] = implode("\n", $sectionContent);
        }

        // If no structured sections found, extract smart summary
        if ($sections['summary'] === '' && $sections['insights'] === '' && $sections['details'] === '') {
            $sections = $this->createSyntheticSections($content);
        }

        return $sections;
    }

    /**
     * Create synthetic sections when content isn't pre-structured.
     *
     * @return array<string, string>
     */
    private function createSyntheticSections(string $content): array
    {
        $lines = explode("\n", $content);
        $totalLines = count($lines);

        // Summary: first 10% of lines (at least 5 lines)
        $summaryLines = max(5, (int) ($totalLines * 0.10));
        $summary = array_slice($lines, 0, $summaryLines);

        // Insights: next 25% of lines
        $insightsLines = (int) ($totalLines * 0.25);
        $insights = array_slice($lines, $summaryLines, $insightsLines);

        // Details: remaining lines
        $details = array_slice($lines, $summaryLines + $insightsLines);

        return [
            'summary' => implode("\n", $summary),
            'insights' => implode("\n", $insights),
            'details' => implode("\n", $details),
        ];
    }

    /**
     * Build hierarchical content fitting within budgets.
     *
     * @param array<string, string> $sections
     */
    private function buildHierarchicalContent(
        array $sections,
        int $summaryBudget,
        int $insightsBudget,
        int $detailsBudget,
        TokenCounter $counter
    ): string {
        $parts = [];

        // Always include summary (truncate if needed)
        if ($sections['summary'] !== '') {
            $summaryTokens = $counter->count($sections['summary']);
            if ($summaryTokens > $summaryBudget) {
                $parts[] = $this->truncateContent($sections['summary'], $summaryBudget, $counter);
            } else {
                $parts[] = $sections['summary'];
            }
        }

        // Include insights if budget allows
        if ($sections['insights'] !== '') {
            $insightsTokens = $counter->count($sections['insights']);
            if ($insightsTokens > $insightsBudget) {
                $parts[] = $this->truncateContent($sections['insights'], $insightsBudget, $counter);
            } else {
                $parts[] = $sections['insights'];
            }
        }

        // Include details if budget allows
        if ($sections['details'] !== '') {
            $detailsTokens = $counter->count($sections['details']);
            if ($detailsTokens > $detailsBudget) {
                $parts[] = $this->truncateContent($sections['details'], $detailsBudget, $counter);
            } else {
                $parts[] = $sections['details'];
            }
        }

        return implode("\n\n", array_filter($parts));
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

        $ratio = $targetTokens / $currentTokens;
        $charLimit = (int) (mb_strlen($content, 'UTF-8') * $ratio * 0.9);

        $truncated = mb_substr($content, 0, $charLimit, 'UTF-8');

        // Break at sensible boundary
        $lastNewline = mb_strrpos($truncated, "\n");
        if ($lastNewline !== false && $lastNewline > $charLimit * 0.8) {
            $truncated = mb_substr($truncated, 0, $lastNewline);
        }

        return $truncated . "\n\n_[Section truncated]_";
    }
}
