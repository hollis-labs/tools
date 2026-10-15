<?php

declare(strict_types=1);

namespace LaravelContextPack\Quality\Scoring;

/**
 * Contract for quality metrics that calculate specific scores.
 *
 * Each metric measures a specific aspect of context quality and returns
 * a normalized score between 0.0 and 1.0.
 */
interface QualityMetric
{
    /**
     * Calculate the metric score.
     *
     * @param array<string, mixed> $context Generated context data to analyze
     * @return float Score between 0.0 (worst) and 1.0 (best)
     */
    public function calculate(array $context): float;

    /**
     * Get the unique name of this metric.
     */
    public function getName(): string;

    /**
     * Get a human-readable description of what this metric measures.
     */
    public function getDescription(): string;

    /**
     * Get the default weight for this metric in overall scoring.
     *
     * @return float Weight between 0.0 and 1.0
     */
    public function getDefaultWeight(): float;
}
