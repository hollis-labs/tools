<?php

declare(strict_types=1);

use LaravelContextPack\Quality\Report\JsonReportFormatter;
use LaravelContextPack\Quality\Scoring\QualityScore;
use LaravelContextPack\Quality\Validation\ValidationReport;
use LaravelContextPack\Quality\Validation\ValidationResult;

test('it formats a complete report in json', function () {
    $formatter = new JsonReportFormatter();

    $validationReport = new ValidationReport([
        ValidationResult::pass('test_rule'),
    ]);

    $qualityScore = new QualityScore(
        0.85,
        ['test_metric' => 0.85],
        ['test_metric' => 0.5]
    );

    $report = $formatter->format($validationReport, $qualityScore, []);

    $data = json_decode($report, true);

    expect($data)->toBeArray();
    expect($data)->toHaveKey('overall_score');
    expect($data)->toHaveKey('validation');
    expect($data)->toHaveKey('metrics');
    expect($data)->toHaveKey('recommendations');
    expect($data['overall_score']['percentage'])->toBe(85);
});

test('it includes validation results in json', function () {
    $formatter = new JsonReportFormatter();

    $validationReport = new ValidationReport([
        ValidationResult::error('error_rule', 'Error message'),
        ValidationResult::warning('warning_rule', 'Warning message'),
    ]);

    $qualityScore = new QualityScore(0.5, [], []);

    $report = $formatter->format($validationReport, $qualityScore, []);

    $data = json_decode($report, true);

    expect($data['validation']['errors'])->toBe(1);
    expect($data['validation']['warnings'])->toBe(1);
    expect($data['validation']['valid'])->toBeFalse();
});

test('it returns correct format metadata', function () {
    $formatter = new JsonReportFormatter();

    expect($formatter->getFormatName())->toBe('json');
    expect($formatter->getFileExtension())->toBe('json');
});

test('it can disable pretty print', function () {
    $formatter = new JsonReportFormatter(prettyPrint: false);

    $validationReport = new ValidationReport([]);
    $qualityScore = new QualityScore(0.8, [], []);

    $report = $formatter->format($validationReport, $qualityScore, []);

    expect($report)->not->toContain("\n    ");
});
