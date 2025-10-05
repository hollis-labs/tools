<?php

declare(strict_types=1);

namespace LaravelContextPack\Quality\Validation\Rule;

use LaravelContextPack\Quality\Validation\ValidationResult;
use LaravelContextPack\Quality\Validation\ValidationRule;

/**
 * Validates that expected output files were generated.
 */
final class OutputFileExistsRule implements ValidationRule
{
    public function __construct(
        private string $outputPath,
        private string $fileName
    ) {
    }

    public function validate(array $output): ValidationResult
    {
        $filePath = $this->outputPath . '/' . $this->fileName;

        if (!file_exists($filePath)) {
            return ValidationResult::error(
                $this->getName(),
                sprintf('Required output file missing: %s', $this->fileName)
            );
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
        return sprintf('Output file must exist: %s', $this->fileName);
    }

    public function getName(): string
    {
        return 'output-file-exists';
    }
}
