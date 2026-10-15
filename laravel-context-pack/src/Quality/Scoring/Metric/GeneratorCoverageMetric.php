<?php

declare(strict_types=1);

namespace LaravelContextPack\Quality\Scoring\Metric;

use LaravelContextPack\Quality\Scoring\QualityMetric;

/**
 * Measures the percentage of generators that successfully completed.
 */
final class GeneratorCoverageMetric implements QualityMetric
{
    public function calculate(array $context): float
    {
        $generators = $context['generators'] ?? [];

        if (count($generators) === 0) {
            return 0.0;
        }

        $successful = 0;
        $total = count($generators);

        foreach ($generators as $generator) {
            if (isset($generator['status']) && $generator['status'] === 'success') {
                $successful++;
            }
        }

        return $successful / $total;
    }

    public function getName(): string
    {
        return 'generator_coverage';
    }

    public function getDescription(): string
    {
        return 'Percentage of generators that successfully completed execution';
    }

    public function getDefaultWeight(): float
    {
        return 0.25;
    }
}
