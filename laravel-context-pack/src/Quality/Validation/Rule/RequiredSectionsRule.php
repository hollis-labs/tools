<?php

declare(strict_types=1);

namespace LaravelContextPack\Quality\Validation\Rule;

use LaravelContextPack\Quality\Validation\ValidationResult;
use LaravelContextPack\Quality\Validation\ValidationRule;

/**
 * Validates that markdown files contain required sections.
 */
final class RequiredSectionsRule implements ValidationRule
{
    /**
     * @param list<string> $requiredSections
     */
    public function __construct(
        private string $outputPath,
        private string $fileName,
        private array $requiredSections
    ) {
    }

    public function validate(array $output): ValidationResult
    {
        $filePath = $this->outputPath . '/' . $this->fileName;

        if (!file_exists($filePath)) {
            return ValidationResult::pass($this->getName());
        }

        $content = file_get_contents($filePath);
        if ($content === false) {
            return ValidationResult::pass($this->getName());
        }

        $missingSections = [];

        foreach ($this->requiredSections as $section) {
            // Look for markdown headers (# Section or ## Section)
            $pattern = '/^#{1,6}\s+' . preg_quote($section, '/') . '\s*$/m';
            if (preg_match($pattern, $content) !== 1) {
                $missingSections[] = $section;
            }
        }

        if ($missingSections !== []) {
            return ValidationResult::warning(
                $this->getName(),
                sprintf(
                    'Missing required sections in %s: %s',
                    $this->fileName,
                    implode(', ', $missingSections)
                )
            );
        }

        return ValidationResult::pass($this->getName());
    }

    /**
     * @return 'warning'
     */
    public function getSeverity(): string
    {
        return 'warning';
    }

    public function getMessage(): string
    {
        return sprintf(
            'File must contain required sections: %s',
            implode(', ', $this->requiredSections)
        );
    }

    public function getName(): string
    {
        return 'required-sections';
    }
}
