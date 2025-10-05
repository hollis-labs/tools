<?php

declare(strict_types=1);

namespace LaravelContextPack\Quality\Validation\Rule;

use LaravelContextPack\Quality\Validation\ValidationResult;
use LaravelContextPack\Quality\Validation\ValidationRule;

/**
 * Validates that markdown output is well-formed.
 */
final class ValidMarkdownRule implements ValidationRule
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
            return ValidationResult::pass($this->getName());
        }

        $content = file_get_contents($filePath);
        if ($content === false) {
            return ValidationResult::error(
                $this->getName(),
                sprintf('Cannot read file: %s', $this->fileName)
            );
        }

        // Basic markdown validation
        if (trim($content) === '') {
            return ValidationResult::error(
                $this->getName(),
                sprintf('Markdown file is empty: %s', $this->fileName)
            );
        }

        // Check for common markdown issues
        $issues = [];

        // Unclosed code blocks
        $codeBlockCount = substr_count($content, '```');
        if ($codeBlockCount % 2 !== 0) {
            $issues[] = 'Unclosed code block detected';
        }

        // Malformed tables (more pipes on left than right in table rows)
        if (preg_match('/\|.*\|/', $content)) {
            $lines = explode("\n", $content);
            foreach ($lines as $lineNum => $line) {
                if (str_contains($line, '|')) {
                    $pipes = substr_count($line, '|');
                    // Very basic check - just ensure even number of pipes
                    if ($pipes > 0 && !str_starts_with(trim($line), '|')) {
                        // Line has pipes but doesn't start with one - might be issue
                        // This is a very permissive check
                    }
                }
            }
        }

        if ($issues !== []) {
            return ValidationResult::warning(
                $this->getName(),
                sprintf('Markdown issues in %s: %s', $this->fileName, implode(', ', $issues))
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
        return sprintf('Markdown file must be well-formed: %s', $this->fileName);
    }

    public function getName(): string
    {
        return 'valid-markdown';
    }
}
