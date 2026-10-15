<?php

declare(strict_types=1);

namespace LaravelContextPack\Quality\Scoring\Metric;

use LaravelContextPack\Quality\Scoring\QualityMetric;

/**
 * Measures how many optional metadata fields are populated.
 */
final class MetadataRichnessMetric implements QualityMetric
{
    /**
     * @var list<string> Optional fields to check
     */
    private const OPTIONAL_FIELDS = [
        'app_name',
        'app_version',
        'laravel_version',
        'php_version',
        'environment',
        'timezone',
        'locale',
        'composer_packages',
        'config_summary',
        'middleware',
        'service_providers',
        'database_connections',
    ];

    public function calculate(array $context): float
    {
        $metadata = $context['metadata'] ?? [];

        $populated = 0;
        $total = count(self::OPTIONAL_FIELDS);

        foreach (self::OPTIONAL_FIELDS as $field) {
            if (isset($metadata[$field]) && $this->isPopulated($metadata[$field])) {
                $populated++;
            }
        }

        return $populated / $total;
    }

    /**
     * Check if a value is considered populated.
     *
     * @param mixed $value
     */
    private function isPopulated(mixed $value): bool
    {
        if ($value === null || $value === '' || $value === []) {
            return false;
        }

        return true;
    }

    public function getName(): string
    {
        return 'metadata_richness';
    }

    public function getDescription(): string
    {
        return 'Percentage of optional metadata fields that are populated';
    }

    public function getDefaultWeight(): float
    {
        return 0.15;
    }
}
