<?php

declare(strict_types=1);

use LaravelContextPack\Quality\Report\MarkdownReportFormatter;
use LaravelContextPack\Quality\Scoring\QualityScore;
use LaravelContextPack\Quality\Validation\ValidationReport;
use LaravelContextPack\Quality\Validation\ValidationResult;

test('it formats a complete report in markdown', function () {
    $formatter = new MarkdownReportFormatter();

    $validationReport = new ValidationReport([
        ValidationResult::pass('test_rule'),
        ValidationResult::error('error_rule', 'Something is wrong'),
    ]);

    $qualityScore = new QualityScore(
        0.85,
        ['test_metric' => 0.85],
        ['test_metric' => 0.5]
    );

    $report = $formatter->format($validationReport, $qualityScore, []);

    expect($report)->toContain('# Context Quality Report');
    expect($report)->toContain('85.0%');
    expect($report)->toContain('Grade: B');
    expect($report)->toContain('## Validation Results');
    expect($report)->toContain('## Quality Metrics');
});

test('it includes recommendations for failing validations', function () {
    $formatter = new MarkdownReportFormatter();

    $validationReport = new ValidationReport([
        ValidationResult::error('critical_rule', 'Critical issue found'),
    ]);

    $qualityScore = new QualityScore(0.5, [], []);

    $report = $formatter->format($validationReport, $qualityScore, []);

    expect($report)->toContain('## Recommendations');
    expect($report)->toContain('Critical issue found');
});

test('it returns correct format metadata', function () {
    $formatter = new MarkdownReportFormatter();

    expect($formatter->getFormatName())->toBe('markdown');
    expect($formatter->getFileExtension())->toBe('md');
});
