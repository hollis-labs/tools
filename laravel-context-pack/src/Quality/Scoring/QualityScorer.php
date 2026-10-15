<?php

declare(strict_types=1);

namespace LaravelContextPack\Quality\Scoring;

/**
 * Calculates quality scores using weighted metrics.
 */
final class QualityScorer
{
    /**
     * @var list<QualityMetric>
     */
    private array $metrics = [];

    /**
     * @var array<string, float> Custom weights (overrides default)
     */
    private array $customWeights = [];

    /**
     * Add a quality metric.
     */
    public function addMetric(QualityMetric $metric): void
    {
        $this->metrics[] = $metric;
    }

    /**
     * Add multiple quality metrics.
     *
     * @param list<QualityMetric> $metrics
     */
    public function addMetrics(array $metrics): void
    {
        foreach ($metrics as $metric) {
            $this->addMetric($metric);
        }
    }

    /**
     * Set custom weight for a metric.
     */
    public function setWeight(string $metricName, float $weight): void
    {
        if ($weight < 0.0 || $weight > 1.0) {
            throw new \InvalidArgumentException(
                "Weight must be between 0.0 and 1.0, got {$weight}"
            );
        }

        $this->customWeights[$metricName] = $weight;
    }

    /**
     * Set custom weights for multiple metrics.
     *
     * @param array<string, float> $weights
     */
    public function setWeights(array $weights): void
    {
        foreach ($weights as $metricName => $weight) {
            $this->setWeight($metricName, $weight);
        }
    }

    /**
     * Calculate quality score for the given context.
     *
     * @param array<string, mixed> $context
     */
    public function score(array $context): QualityScore
    {
        if (count($this->metrics) === 0) {
            throw new \RuntimeException('No metrics registered. Add metrics before scoring.');
        }

        $metricScores = [];
        $weights = [];
        $totalWeight = 0.0;
        $weightedSum = 0.0;

        // Calculate individual metric scores
        foreach ($this->metrics as $metric) {
            $name = $metric->getName();
            $score = $metric->calculate($context);

            // Ensure score is within bounds
            $score = max(0.0, min(1.0, $score));

            $metricScores[$name] = $score;

            // Get weight (custom or default)
            $weight = $this->customWeights[$name] ?? $metric->getDefaultWeight();
            $weights[$name] = $weight;

            $weightedSum += $score * $weight;
            $totalWeight += $weight;
        }

        // Normalize if weights don't sum to 1.0
        $overallScore = $totalWeight > 0.0 ? $weightedSum / $totalWeight : 0.0;

        // Ensure overall score is within bounds
        $overallScore = max(0.0, min(1.0, $overallScore));

        return new QualityScore($overallScore, $metricScores, $weights);
    }

    /**
     * Get all registered metrics.
     *
     * @return list<QualityMetric>
     */
    public function getMetrics(): array
    {
        return $this->metrics;
    }

    /**
     * Clear all registered metrics.
     */
    public function clearMetrics(): void
    {
        $this->metrics = [];
    }

    /**
     * Clear all custom weights.
     */
    public function clearWeights(): void
    {
        $this->customWeights = [];
    }
}
