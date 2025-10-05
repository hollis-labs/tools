<?php

declare(strict_types=1);

namespace LaravelContextPack\Quality\Benchmark;

/**
 * Result of comparing current score against a baseline.
 */
final class BenchmarkComparison
{
    /**
     * @param array<string, array<string, mixed>> $metricComparisons
     */
    public function __construct(
        private float $baselineScore,
        private float $currentScore,
        private float $difference,
        private float $percentageChange,
        private bool $hasRegression,
        private bool $hasImprovement,
        private array $metricComparisons,
        private ?string $baselineTimestamp = null
    ) {
    }

    public function getBaselineScore(): float
    {
        return $this->baselineScore;
    }

    public function getCurrentScore(): float
    {
        return $this->currentScore;
    }

    public function getDifference(): float
    {
        return $this->difference;
    }

    public function getPercentageChange(): float
    {
        return $this->percentageChange;
    }

    public function hasRegression(): bool
    {
        return $this->hasRegression;
    }

    public function hasImprovement(): bool
    {
        return $this->hasImprovement;
    }

    /**
     * @return array<string, array<string, mixed>>
     */
    public function getMetricComparisons(): array
    {
        return $this->metricComparisons;
    }

    public function getBaselineTimestamp(): ?string
    {
        return $this->baselineTimestamp;
    }

    /**
     * Get metrics that have regressed.
     *
     * @return list<string>
     */
    public function getRegressedMetrics(): array
    {
        $regressed = [];

        foreach ($this->metricComparisons as $metricName => $comparison) {
            if ($comparison['has_regression'] ?? false) {
                $regressed[] = $metricName;
            }
        }

        return $regressed;
    }

    /**
     * Get a summary message.
     */
    public function getSummary(): string
    {
        if ($this->hasRegression) {
            return sprintf(
                'REGRESSION DETECTED: Score dropped %.1f%% (%.3f → %.3f)',
                abs($this->percentageChange),
                $this->baselineScore,
                $this->currentScore
            );
        }

        if ($this->hasImprovement) {
            return sprintf(
                'IMPROVEMENT: Score increased %.1f%% (%.3f → %.3f)',
                $this->percentageChange,
                $this->baselineScore,
                $this->currentScore
            );
        }

        return sprintf(
            'STABLE: Score changed %.1f%% (%.3f → %.3f)',
            $this->percentageChange,
            $this->baselineScore,
            $this->currentScore
        );
    }
}
