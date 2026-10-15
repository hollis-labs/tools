<?php

declare(strict_types=1);

namespace LaravelContextPack\Quality\Report;

use LaravelContextPack\Quality\Scoring\QualityScore;
use LaravelContextPack\Quality\Scoring\QualityScorer;
use LaravelContextPack\Quality\Validation\ContextValidator;
use LaravelContextPack\Quality\Validation\ValidationReport;

/**
 * Generates complete quality reports combining validation and scoring.
 */
final class QualityReportGenerator
{
    public function __construct(
        private ContextValidator $validator,
        private QualityScorer $scorer
    ) {
    }

    /**
     * Generate a quality report for the given context.
     *
     * @param array<string, mixed> $context
     */
    public function generate(array $context, QualityReportFormatter $formatter): string
    {
        $validationReport = $this->validator->validate($context);
        $qualityScore = $this->scorer->score($context);

        return $formatter->format($validationReport, $qualityScore, $context);
    }

    /**
     * Generate and save a quality report to a file.
     *
     * @param array<string, mixed> $context
     */
    public function generateToFile(
        array $context,
        QualityReportFormatter $formatter,
        string $outputPath
    ): void {
        $report = $this->generate($context, $formatter);

        $directory = dirname($outputPath);
        if (!is_dir($directory)) {
            if (!mkdir($directory, 0755, true) && !is_dir($directory)) {
                throw new \RuntimeException("Failed to create directory: {$directory}");
            }
        }

        if (file_put_contents($outputPath, $report) === false) {
            throw new \RuntimeException("Failed to write report to: {$outputPath}");
        }
    }

    /**
     * Get the validation report only.
     *
     * @param array<string, mixed> $context
     */
    public function getValidationReport(array $context): ValidationReport
    {
        return $this->validator->validate($context);
    }

    /**
     * Get the quality score only.
     *
     * @param array<string, mixed> $context
     */
    public function getQualityScore(array $context): QualityScore
    {
        return $this->scorer->score($context);
    }
}
