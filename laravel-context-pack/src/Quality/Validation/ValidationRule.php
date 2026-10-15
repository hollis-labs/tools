<?php

declare(strict_types=1);

namespace LaravelContextPack\Quality\Validation;

/**
 * Contract for validation rules that check context quality.
 *
 * Each rule validates a specific aspect of generated context and returns
 * a result indicating pass/fail with severity and message.
 */
interface ValidationRule
{
    /**
     * Validate the given output.
     *
     * @param array<string, mixed> $output Generator output data
     */
    public function validate(array $output): ValidationResult;

    /**
     * Get the severity level of failures for this rule.
     *
     * @return 'error'|'warning'|'info'
     */
    public function getSeverity(): string;

    /**
     * Get a human-readable description of what this rule checks.
     */
    public function getMessage(): string;

    /**
     * Get the unique name of this rule.
     */
    public function getName(): string;
}
