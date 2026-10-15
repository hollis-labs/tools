<?php

declare(strict_types=1);

use LaravelContextPack\Quality\Scoring\QualityScore;

test('it calculates percentage correctly', function () {
    $score = new QualityScore(0.85, [], []);

    expect($score->getPercentage())->toBe(85.0);
});

test('it assigns correct letter grades', function () {
    expect((new QualityScore(0.95, [], []))->getGrade())->toBe('A');
    expect((new QualityScore(0.85, [], []))->getGrade())->toBe('B');
    expect((new QualityScore(0.75, [], []))->getGrade())->toBe('C');
    expect((new QualityScore(0.65, [], []))->getGrade())->toBe('D');
    expect((new QualityScore(0.55, [], []))->getGrade())->toBe('F');
});

test('it checks threshold correctly', function () {
    $score = new QualityScore(0.75, [], []);

    expect($score->meetsThreshold(0.7))->toBeTrue();
    expect($score->meetsThreshold(0.8))->toBeFalse();
});

test('it identifies passing scores', function () {
    expect((new QualityScore(0.7, [], []))->isPassing())->toBeTrue();
    expect((new QualityScore(0.69, [], []))->isPassing())->toBeFalse();
});

test('it retrieves metric scores', function () {
    $metricScores = [
        'generator_coverage' => 0.8,
        'model_coverage' => 0.9,
    ];

    $score = new QualityScore(0.85, $metricScores, []);

    expect($score->getMetricScores())->toBe($metricScores);
    expect($score->getMetricScore('generator_coverage'))->toBe(0.8);
    expect($score->getMetricScore('nonexistent'))->toBeNull();
});
