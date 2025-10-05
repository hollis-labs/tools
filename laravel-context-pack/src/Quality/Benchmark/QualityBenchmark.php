<?php

declare(strict_types=1);

namespace LaravelContextPack\Quality\Benchmark;

use LaravelContextPack\Quality\Scoring\QualityScore;

/**
 * Manages quality benchmarks for regression detection.
 */
final class QualityBenchmark
{
    private const REGRESSION_THRESHOLD = 0.05; // 5% drop

    /**
     * Save a baseline benchmark.
     */
    public function saveBaseline(QualityScore $score, string $path): void
    {
        $data = [
            'timestamp' => date('c'),
            'overall_score' => $score->getOverallScore(),
            'percentage' => $score->getPercentage(),
            'grade' => $score->getGrade(),
            'metrics' => $score->getMetricScores(),
            'weights' => $score->getWeights(),
        ];

        $directory = dirname($path);
        if (!is_dir($directory)) {
            if (!mkdir($directory, 0755, true) && !is_dir($directory)) {
                throw new \RuntimeException("Failed to create directory: {$directory}");
            }
        }

        $json = json_encode($data, JSON_PRETTY_PRINT | JSON_UNESCAPED_SLASHES);

        if ($json === false) {
            throw new \RuntimeException('Failed to encode baseline: ' . json_last_error_msg());
        }

        if (file_put_contents($path, $json) === false) {
            throw new \RuntimeException("Failed to write baseline to: {$path}");
        }
    }

    /**
     * Load a baseline benchmark.
     *
     * @return array<string, mixed>
     */
    public function loadBaseline(string $path): array
    {
        if (!file_exists($path)) {
            throw new \RuntimeException("Baseline file not found: {$path}");
        }

        $content = file_get_contents($path);

        if ($content === false) {
            throw new \RuntimeException("Failed to read baseline from: {$path}");
        }

        $data = json_decode($content, true);

        if ($data === null) {
            throw new \RuntimeException('Failed to decode baseline: ' . json_last_error_msg());
        }

        return $data;
    }

    /**
     * Compare current score against baseline.
     *
     * @param array<string, mixed> $baseline
     */
    public function compare(QualityScore $current, array $baseline): BenchmarkComparison
    {
        $baselineScore = $baseline['overall_score'] ?? 0.0;
        $currentScore = $current->getOverallScore();

        $difference = $currentScore - $baselineScore;
        $percentageChange = $baselineScore > 0.0
            ? ($difference / $baselineScore) * 100
            : 0.0;

        $hasRegression = $difference < -self::REGRESSION_THRESHOLD;
        $hasImprovement = $difference > self::REGRESSION_THRESHOLD;

        // Compare individual metrics
        $metricComparisons = [];
        $baselineMetrics = $baseline['metrics'] ?? [];
        $currentMetrics = $current->getMetricScores();

        foreach ($currentMetrics as $metricName => $currentMetricScore) {
            $baselineMetricScore = $baselineMetrics[$metricName] ?? 0.0;
            $metricDifference = $currentMetricScore - $baselineMetricScore;

            $metricComparisons[$metricName] = [
                'baseline' => $baselineMetricScore,
                'current' => $currentMetricScore,
                'difference' => $metricDifference,
                'percentage_change' => $baselineMetricScore > 0.0
                    ? ($metricDifference / $baselineMetricScore) * 100
                    : 0.0,
                'has_regression' => $metricDifference < -self::REGRESSION_THRESHOLD,
            ];
        }

        return new BenchmarkComparison(
            baselineScore: $baselineScore,
            currentScore: $currentScore,
            difference: $difference,
            percentageChange: $percentageChange,
            hasRegression: $hasRegression,
            hasImprovement: $hasImprovement,
            metricComparisons: $metricComparisons,
            baselineTimestamp: $baseline['timestamp'] ?? null
        );
    }

    /**
     * Detect regressions in quality score.
     */
    public function detectRegressions(QualityScore $current, string $baselinePath): bool
    {
        $baseline = $this->loadBaseline($baselinePath);
        $comparison = $this->compare($current, $baseline);

        return $comparison->hasRegression();
    }

    /**
     * Get the regression threshold.
     */
    public function getRegressionThreshold(): float
    {
        return self::REGRESSION_THRESHOLD;
    }
}
