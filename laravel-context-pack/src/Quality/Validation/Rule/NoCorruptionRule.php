<?php

declare(strict_types=1);

namespace LaravelContextPack\Quality\Validation\Rule;

use LaravelContextPack\Quality\Validation\ValidationResult;
use LaravelContextPack\Quality\Validation\ValidationRule;

/**
 * Validates that output files are not corrupted or truncated.
 */
final class NoCorruptionRule implements ValidationRule
{
    private const MIN_FILE_SIZE = 10; // bytes

    public function __construct(
        private string $outputPath,
        private string $fileName
    ) {
    }

    public function validate(array $output): ValidationResult
    {
        $filePath = $this->outputPath . '/' . $this->fileName;

        if (!file_exists($filePath)) {
            return ValidationResult::pass($this->getName());
        }

        $size = filesize($filePath);
        if ($size === false || $size < self::MIN_FILE_SIZE) {
            return ValidationResult::error(
                $this->getName(),
                sprintf('File appears corrupted or empty: %s (size: %d bytes)', $this->fileName, $size ?: 0)
            );
        }

        $content = file_get_contents($filePath);
        if ($content === false) {
            return ValidationResult::error(
                $this->getName(),
                sprintf('Cannot read file (may be corrupted): %s', $this->fileName)
            );
        }

        // Check for binary data in what should be text files
        if (str_ends_with($this->fileName, '.md') || str_ends_with($this->fileName, '.json')) {
            if (!mb_check_encoding($content, 'UTF-8')) {
                return ValidationResult::warning(
                    $this->getName(),
                    sprintf('File contains non-UTF-8 data: %s', $this->fileName)
                );
            }
        }

        return ValidationResult::pass($this->getName());
    }

    /**
     * @return 'error'
     */
    public function getSeverity(): string
    {
        return 'error';
    }

    public function getMessage(): string
    {
        return sprintf('File must not be corrupted or truncated: %s', $this->fileName);
    }

    public function getName(): string
    {
        return 'no-corruption';
    }
}
