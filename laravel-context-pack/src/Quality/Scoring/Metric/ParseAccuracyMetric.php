<?php

declare(strict_types=1);

namespace LaravelContextPack\Quality\Scoring\Metric;

use LaravelContextPack\Quality\Scoring\QualityMetric;

/**
 * Measures the percentage of files parsed without errors.
 */
final class ParseAccuracyMetric implements QualityMetric
{
    public function calculate(array $context): float
    {
        $parseErrors = $context['parse_errors'] ?? [];
        $totalFiles = $context['total_files_parsed'] ?? 0;

        if ($totalFiles === 0) {
            // If we have parse errors but no total, that's bad
            if (count($parseErrors) > 0) {
                return 0.0;
            }

            // If no files and no errors, consider it perfect
            return 1.0;
        }

        $errorCount = count($parseErrors);
        $successfullyParsed = $totalFiles - $errorCount;

        return $successfullyParsed / $totalFiles;
    }

    public function getName(): string
    {
        return 'parse_accuracy';
    }

    public function getDescription(): string
    {
        return 'Percentage of files parsed without errors';
    }

    public function getDefaultWeight(): float
    {
        return 0.20;
    }
}
