<?php

declare(strict_types=1);

namespace LaravelContextPack\Quality\Scoring\Metric;

use LaravelContextPack\Quality\Scoring\QualityMetric;

/**
 * Measures the percentage of models that have documentation generated.
 */
final class ModelCoverageMetric implements QualityMetric
{
    public function calculate(array $context): float
    {
        $models = $context['models'] ?? [];

        if (count($models) === 0) {
            return 1.0; // No models means 100% coverage
        }

        $documented = 0;
        $total = count($models);

        foreach ($models as $model) {
            // Consider a model documented if it has properties or relationships
            if (
                (isset($model['properties']) && count($model['properties']) > 0) ||
                (isset($model['relationships']) && count($model['relationships']) > 0) ||
                (isset($model['scopes']) && count($model['scopes']) > 0)
            ) {
                $documented++;
            }
        }

        return $documented / $total;
    }

    public function getName(): string
    {
        return 'model_coverage';
    }

    public function getDescription(): string
    {
        return 'Percentage of models that have complete documentation';
    }

    public function getDefaultWeight(): float
    {
        return 0.20;
    }
}
