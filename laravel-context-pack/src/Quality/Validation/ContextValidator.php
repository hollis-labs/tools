<?php

declare(strict_types=1);

namespace LaravelContextPack\Quality\Validation;

/**
 * Validates generated context packages against quality rules.
 */
final class ContextValidator
{
    /**
     * @var list<ValidationRule>
     */
    private array $rules = [];

    /**
     * Add a validation rule.
     */
    public function addRule(ValidationRule $rule): void
    {
        $this->rules[] = $rule;
    }

    /**
     * Add multiple validation rules.
     *
     * @param list<ValidationRule> $rules
     */
    public function addRules(array $rules): void
    {
        foreach ($rules as $rule) {
            $this->addRule($rule);
        }
    }

    /**
     * Validate the given output against all rules.
     *
     * @param array<string, mixed> $output
     */
    public function validate(array $output): ValidationReport
    {
        $results = [];

        foreach ($this->rules as $rule) {
            $results[] = $rule->validate($output);
        }

        return new ValidationReport($results);
    }

    /**
     * Get all registered rules.
     *
     * @return list<ValidationRule>
     */
    public function getRules(): array
    {
        return $this->rules;
    }

    /**
     * Clear all registered rules.
     */
    public function clearRules(): void
    {
        $this->rules = [];
    }
}
