<?php

declare(strict_types=1);

namespace LaravelContextPack\Quality\Scoring\Metric;

use LaravelContextPack\Quality\Scoring\QualityMetric;

/**
 * Measures the percentage of routes that have complete documentation.
 */
final class RouteCoverageMetric implements QualityMetric
{
    public function calculate(array $context): float
    {
        $routes = $context['routes'] ?? [];

        if (count($routes) === 0) {
            return 1.0; // No routes means 100% coverage
        }

        $documented = 0;
        $total = count($routes);

        foreach ($routes as $route) {
            // Consider a route documented if it has method, uri, and action
            if (
                isset($route['method']) &&
                isset($route['uri']) &&
                isset($route['action'])
            ) {
                $documented++;
            }
        }

        return $documented / $total;
    }

    public function getName(): string
    {
        return 'route_coverage';
    }

    public function getDescription(): string
    {
        return 'Percentage of routes that have complete documentation';
    }

    public function getDefaultWeight(): float
    {
        return 0.20;
    }
}
