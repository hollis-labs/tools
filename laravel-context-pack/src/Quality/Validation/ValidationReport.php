<?php

declare(strict_types=1);

namespace LaravelContextPack\Quality\Validation;

/**
 * Aggregated report of all validation results.
 */
final class ValidationReport
{
    /**
     * @param list<ValidationResult> $results
     */
    public function __construct(
        private array $results
    ) {
    }

    /**
     * Check if all validations passed.
     */
    public function isValid(): bool
    {
        foreach ($this->results as $result) {
            if ($result->isFailing() && $result->isError()) {
                return false;
            }
        }

        return true;
    }

    /**
     * Get all validation results.
     *
     * @return list<ValidationResult>
     */
    public function getResults(): array
    {
        return $this->results;
    }

    /**
     * Get only failing results.
     *
     * @return list<ValidationResult>
     */
    public function getFailures(): array
    {
        return array_values(array_filter(
            $this->results,
            static fn (ValidationResult $result): bool => $result->isFailing()
        ));
    }

    /**
     * Get count of errors.
     */
    public function getErrorCount(): int
    {
        return count(array_filter(
            $this->results,
            static fn (ValidationResult $result): bool => $result->isError()
        ));
    }

    /**
     * Get count of warnings.
     */
    public function getWarningCount(): int
    {
        return count(array_filter(
            $this->results,
            static fn (ValidationResult $result): bool => $result->isWarning()
        ));
    }

    /**
     * Get count of passed validations.
     */
    public function getPassCount(): int
    {
        return count(array_filter(
            $this->results,
            static fn (ValidationResult $result): bool => $result->isPassing()
        ));
    }
}
