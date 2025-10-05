<?php

declare(strict_types=1);

namespace LaravelContextPack\Quality\Validation;

/**
 * Result of a validation rule execution.
 */
final class ValidationResult
{
    /**
     * @param 'pass'|'fail' $status
     * @param 'error'|'warning'|'info' $severity
     */
    private function __construct(
        private string $status,
        private string $severity,
        private string $message,
        private string $ruleName
    ) {
    }

    public static function pass(string $ruleName): self
    {
        return new self('pass', 'info', 'Validation passed', $ruleName);
    }

    public static function fail(string $ruleName, string $severity, string $message): self
    {
        return new self('fail', $severity, $message, $ruleName);
    }

    public static function error(string $ruleName, string $message): self
    {
        return new self('fail', 'error', $message, $ruleName);
    }

    public static function warning(string $ruleName, string $message): self
    {
        return new self('fail', 'warning', $message, $ruleName);
    }

    public static function info(string $ruleName, string $message): self
    {
        return new self('pass', 'info', $message, $ruleName);
    }

    public function isPassing(): bool
    {
        return $this->status === 'pass';
    }

    public function isFailing(): bool
    {
        return $this->status === 'fail';
    }

    public function isError(): bool
    {
        return $this->severity === 'error';
    }

    public function isWarning(): bool
    {
        return $this->severity === 'warning';
    }

    public function getStatus(): string
    {
        return $this->status;
    }

    public function getSeverity(): string
    {
        return $this->severity;
    }

    public function getMessage(): string
    {
        return $this->message;
    }

    public function getRuleName(): string
    {
        return $this->ruleName;
    }
}
