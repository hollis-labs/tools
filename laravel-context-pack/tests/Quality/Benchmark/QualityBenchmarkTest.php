<?php

declare(strict_types=1);

use LaravelContextPack\Quality\Benchmark\QualityBenchmark;
use LaravelContextPack\Quality\Scoring\QualityScore;

test('it saves and loads baseline correctly', function () {
    $benchmark = new QualityBenchmark();
    $score = new QualityScore(
        0.85,
        ['test_metric' => 0.85],
        ['test_metric' => 0.5]
    );

    $tempFile = sys_get_temp_dir() . '/test-baseline-' . uniqid() . '.json';

    try {
        $benchmark->saveBaseline($score, $tempFile);

        expect(file_exists($tempFile))->toBeTrue();

        $baseline = $benchmark->loadBaseline($tempFile);

        expect($baseline)->toHaveKey('overall_score');
        expect($baseline['overall_score'])->toBe(0.85);
        expect($baseline)->toHaveKey('metrics');
        expect($baseline['metrics']['test_metric'])->toBe(0.85);
    } finally {
        if (file_exists($tempFile)) {
            unlink($tempFile);
        }
    }
});

test('it detects regressions correctly', function () {
    $benchmark = new QualityBenchmark();

    $baselineScore = new QualityScore(0.8, [], []);
    $currentScore = new QualityScore(0.7, [], []);

    $tempFile = sys_get_temp_dir() . '/test-baseline-' . uniqid() . '.json';

    try {
        $benchmark->saveBaseline($baselineScore, $tempFile);

        $hasRegression = $benchmark->detectRegressions($currentScore, $tempFile);

        expect($hasRegression)->toBeTrue();
    } finally {
        if (file_exists($tempFile)) {
            unlink($tempFile);
        }
    }
});

test('it detects improvements correctly', function () {
    $benchmark = new QualityBenchmark();

    $baseline = ['overall_score' => 0.7, 'metrics' => []];
    $currentScore = new QualityScore(0.85, [], []);

    $comparison = $benchmark->compare($currentScore, $baseline);

    expect($comparison->hasImprovement())->toBeTrue();
    expect($comparison->hasRegression())->toBeFalse();
});

test('it compares individual metrics', function () {
    $benchmark = new QualityBenchmark();

    $baseline = [
        'overall_score' => 0.8,
        'metrics' => [
            'test_metric' => 0.9,
            'another_metric' => 0.7,
        ],
    ];

    $currentScore = new QualityScore(
        0.8,
        [
            'test_metric' => 0.85,
            'another_metric' => 0.75,
        ],
        []
    );

    $comparison = $benchmark->compare($currentScore, $baseline);
    $metricComparisons = $comparison->getMetricComparisons();

    expect($metricComparisons)->toHaveKey('test_metric');
    expect($metricComparisons['test_metric']['baseline'])->toBe(0.9);
    expect($metricComparisons['test_metric']['current'])->toBe(0.85);
});

test('it throws exception when baseline file not found', function () {
    $benchmark = new QualityBenchmark();

    $benchmark->loadBaseline('/nonexistent/path.json');
})->throws(RuntimeException::class);
