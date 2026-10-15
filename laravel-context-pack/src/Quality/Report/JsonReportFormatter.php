<?php

declare(strict_types=1);

namespace LaravelContextPack\Quality\Report;

use LaravelContextPack\Quality\Scoring\QualityScore;
use LaravelContextPack\Quality\Validation\ValidationReport;

/**
 * Formats quality reports as machine-parseable JSON.
 */
final class JsonReportFormatter implements QualityReportFormatter
{
    public function __construct(
        private bool $prettyPrint = true
    ) {
    }

    public function format(
        ValidationReport $validationReport,
        QualityScore $qualityScore,
        array $context
    ): string {
        $report = [
            'timestamp' => date('c'),
            'overall_score' => [
                'score' => $qualityScore->getOverallScore(),
                'percentage' => $qualityScore->getPercentage(),
                'grade' => $qualityScore->getGrade(),
                'passing' => $qualityScore->isPassing(),
            ],
            'validation' => [
                'valid' => $validationReport->isValid(),
                'passed' => $validationReport->getPassCount(),
                'errors' => $validationReport->getErrorCount(),
                'warnings' => $validationReport->getWarningCount(),
                'results' => array_map(
                    static fn ($result) => [
                        'rule' => $result->getRuleName(),
                        'status' => $result->getStatus(),
                        'severity' => $result->getSeverity(),
                        'message' => $result->getMessage(),
                    ],
                    $validationReport->getResults()
                ),
            ],
            'metrics' => $this->formatMetrics($qualityScore),
            'recommendations' => $this->generateRecommendations($validationReport, $qualityScore),
        ];

        $flags = $this->prettyPrint
            ? JSON_PRETTY_PRINT | JSON_UNESCAPED_SLASHES
            : JSON_UNESCAPED_SLASHES;

        $json = json_encode($report, $flags);

        if ($json === false) {
            throw new \RuntimeException('Failed to encode report as JSON: ' . json_last_error_msg());
        }

        return $json;
    }

    public function getFormatName(): string
    {
        return 'json';
    }

    public function getFileExtension(): string
    {
        return 'json';
    }

    /**
     * Format metrics for JSON output.
     *
     * @return list<array<string, mixed>>
     */
    private function formatMetrics(QualityScore $qualityScore): array
    {
        $metrics = [];
        $weights = $qualityScore->getWeights();

        foreach ($qualityScore->getMetricScores() as $metricName => $score) {
            $weight = $weights[$metricName] ?? 0.0;

            $metrics[] = [
                'name' => $metricName,
                'score' => $score,
                'percentage' => $score * 100,
                'weight' => $weight,
                'contribution' => $score * $weight,
            ];
        }

        return $metrics;
    }

    /**
     * Generate actionable recommendations.
     *
     * @return list<array<string, string>>
     */
    private function generateRecommendations(
        ValidationReport $validationReport,
        QualityScore $qualityScore
    ): array {
        $recommendations = [];

        // Add recommendations based on validation failures
        foreach ($validationReport->getFailures() as $result) {
            $priority = $result->isError() ? 'critical' : 'normal';

            $recommendations[] = [
                'type' => 'validation',
                'priority' => $priority,
                'message' => sprintf(
                    'Fix %s: %s',
                    $result->getRuleName(),
                    $result->getMessage()
                ),
            ];
        }

        // Add recommendations based on low metric scores
        foreach ($qualityScore->getMetricScores() as $metricName => $score) {
            if ($score < 0.5) {
                $recommendations[] = [
                    'type' => 'metric',
                    'priority' => 'normal',
                    'message' => sprintf(
                        'Improve %s (currently %.1f%%)',
                        str_replace('_', ' ', $metricName),
                        $score * 100
                    ),
                ];
            }
        }

        // Overall score recommendation
        if ($qualityScore->getPercentage() < 70.0) {
            $recommendations[] = [
                'type' => 'overall',
                'priority' => 'critical',
                'message' => 'Overall quality is below passing threshold (70%)',
            ];
        }

        return $recommendations;
    }
}
