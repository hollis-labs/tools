<?php

declare(strict_types=1);

namespace LaravelContextPack\Quality\Scoring;

/**
 * Result object containing quality scores.
 */
final class QualityScore
{
    /**
     * @param float $overallScore Overall weighted score (0.0-1.0)
     * @param array<string, float> $metricScores Individual metric scores
     * @param array<string, float> $weights Weights used for each metric
     */
    public function __construct(
        private float $overallScore,
        private array $metricScores,
        private array $weights
    ) {
    }

    /**
     * Get the overall quality score.
     *
     * @return float Score between 0.0 and 1.0
     */
    public function getOverallScore(): float
    {
        return $this->overallScore;
    }

    /**
     * Get the overall score as a percentage.
     *
     * @return float Score between 0.0 and 100.0
     */
    public function getPercentage(): float
    {
        return $this->overallScore * 100.0;
    }

    /**
     * Get individual metric scores.
     *
     * @return array<string, float> Metric name => score
     */
    public function getMetricScores(): array
    {
        return $this->metricScores;
    }

    /**
     * Get score for a specific metric.
     *
     * @return float|null Score or null if metric doesn't exist
     */
    public function getMetricScore(string $metricName): ?float
    {
        return $this->metricScores[$metricName] ?? null;
    }

    /**
     * Get weights used for scoring.
     *
     * @return array<string, float> Metric name => weight
     */
    public function getWeights(): array
    {
        return $this->weights;
    }

    /**
     * Get a letter grade based on the overall score.
     *
     * @return 'A'|'B'|'C'|'D'|'F'
     */
    public function getGrade(): string
    {
        $percentage = $this->getPercentage();

        return match (true) {
            $percentage >= 90.0 => 'A',
            $percentage >= 80.0 => 'B',
            $percentage >= 70.0 => 'C',
            $percentage >= 60.0 => 'D',
            default => 'F',
        };
    }

    /**
     * Check if the score meets a minimum threshold.
     */
    public function meetsThreshold(float $minScore): bool
    {
        return $this->overallScore >= $minScore;
    }

    /**
     * Check if the score is passing (>= 0.7 or 70%).
     */
    public function isPassing(): bool
    {
        return $this->meetsThreshold(0.7);
    }
}
