<?php

declare(strict_types=1);

namespace LaravelContextPack\Quality\Report;

use LaravelContextPack\Quality\Scoring\QualityScore;
use LaravelContextPack\Quality\Validation\ValidationReport;
use LaravelContextPack\Quality\Validation\ValidationResult;

/**
 * Formats quality reports as human-readable Markdown.
 */
final class MarkdownReportFormatter implements QualityReportFormatter
{
    public function format(
        ValidationReport $validationReport,
        QualityScore $qualityScore,
        array $context
    ): string {
        $output = [];

        $output[] = '# Context Quality Report';
        $output[] = '';
        $output[] = 'Generated: ' . date('Y-m-d H:i:s');
        $output[] = '';

        // Overall Score Section
        $output[] = '## Overall Quality Score';
        $output[] = '';
        $output[] = sprintf(
            '**Score:** %.1f%% (Grade: %s)',
            $qualityScore->getPercentage(),
            $qualityScore->getGrade()
        );
        $output[] = '';
        $output[] = $this->getScoreBar($qualityScore->getOverallScore());
        $output[] = '';

        // Validation Results Section
        $output[] = '## Validation Results';
        $output[] = '';
        $output[] = sprintf(
            '- **Passed:** %d',
            $validationReport->getPassCount()
        );
        $output[] = sprintf(
            '- **Errors:** %d',
            $validationReport->getErrorCount()
        );
        $output[] = sprintf(
            '- **Warnings:** %d',
            $validationReport->getWarningCount()
        );
        $output[] = sprintf(
            '- **Status:** %s',
            $validationReport->isValid() ? '✓ VALID' : '✗ INVALID'
        );
        $output[] = '';

        // Validation Details
        if (count($validationReport->getFailures()) > 0) {
            $output[] = '### Validation Failures';
            $output[] = '';

            foreach ($validationReport->getFailures() as $result) {
                $output[] = sprintf(
                    '- **[%s]** %s: %s',
                    strtoupper($result->getSeverity()),
                    $result->getRuleName(),
                    $result->getMessage()
                );
            }

            $output[] = '';
        }

        // Quality Metrics Section
        $output[] = '## Quality Metrics';
        $output[] = '';
        $output[] = '| Metric | Score | Weight | Contribution |';
        $output[] = '|--------|-------|--------|--------------|';

        $weights = $qualityScore->getWeights();

        foreach ($qualityScore->getMetricScores() as $metricName => $score) {
            $weight = $weights[$metricName] ?? 0.0;
            $contribution = $score * $weight;

            $output[] = sprintf(
                '| %s | %.1f%% | %.0f%% | %.1f%% |',
                $this->formatMetricName($metricName),
                $score * 100,
                $weight * 100,
                $contribution * 100
            );
        }

        $output[] = '';

        // Recommendations Section
        $output[] = '## Recommendations';
        $output[] = '';
        $recommendations = $this->generateRecommendations($validationReport, $qualityScore);

        if (count($recommendations) > 0) {
            foreach ($recommendations as $recommendation) {
                $output[] = '- ' . $recommendation;
            }
        } else {
            $output[] = '- No issues found. Quality is excellent!';
        }

        $output[] = '';

        return implode("\n", $output);
    }

    public function getFormatName(): string
    {
        return 'markdown';
    }

    public function getFileExtension(): string
    {
        return 'md';
    }

    /**
     * Generate a visual score bar.
     */
    private function getScoreBar(float $score): string
    {
        $percentage = (int) ($score * 100);
        $filledBlocks = (int) ($score * 20);
        $emptyBlocks = 20 - $filledBlocks;

        $bar = str_repeat('█', $filledBlocks) . str_repeat('░', $emptyBlocks);

        return sprintf('`%s` %d%%', $bar, $percentage);
    }

    /**
     * Format metric name for display.
     */
    private function formatMetricName(string $name): string
    {
        return ucwords(str_replace('_', ' ', $name));
    }

    /**
     * Generate actionable recommendations.
     *
     * @return list<string>
     */
    private function generateRecommendations(
        ValidationReport $validationReport,
        QualityScore $qualityScore
    ): array {
        $recommendations = [];

        // Add recommendations based on validation failures
        foreach ($validationReport->getFailures() as $result) {
            if ($result->isError()) {
                $recommendations[] = sprintf(
                    '**Critical:** Fix %s - %s',
                    $result->getRuleName(),
                    $result->getMessage()
                );
            }
        }

        // Add recommendations based on low metric scores
        foreach ($qualityScore->getMetricScores() as $metricName => $score) {
            if ($score < 0.5) {
                $recommendations[] = sprintf(
                    'Improve %s (currently %.1f%%)',
                    $this->formatMetricName($metricName),
                    $score * 100
                );
            }
        }

        // Overall score recommendation
        if ($qualityScore->getPercentage() < 70.0) {
            $recommendations[] = '**Overall quality is below passing threshold (70%). Focus on addressing critical issues first.**';
        } elseif ($qualityScore->getPercentage() < 85.0) {
            $recommendations[] = 'Quality is passing but could be improved. Consider addressing warnings.';
        }

        return $recommendations;
    }
}
