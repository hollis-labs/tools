<?php

declare(strict_types=1);

use LaravelContextPack\Quality\Scoring\QualityScorer;
use LaravelContextPack\Quality\Scoring\Metric\GeneratorCoverageMetric;
use LaravelContextPack\Quality\Scoring\Metric\ModelCoverageMetric;

test('it calculates overall score with default weights', function () {
    $scorer = new QualityScorer();
    $scorer->addMetric(new GeneratorCoverageMetric());
    $scorer->addMetric(new ModelCoverageMetric());

    $context = [
        'generators' => [
            ['status' => 'success'],
            ['status' => 'success'],
        ],
        'models' => [
            ['properties' => ['id', 'name']],
        ],
    ];

    $score = $scorer->score($context);

    expect($score->getOverallScore())->toBeGreaterThan(0.0);
    expect($score->getOverallScore())->toBeLessThanOrEqual(1.0);
    expect($score->getMetricScores())->toHaveKeys(['generator_coverage', 'model_coverage']);
});

test('it applies custom weights correctly', function () {
    $scorer = new QualityScorer();
    $scorer->addMetric(new GeneratorCoverageMetric());
    $scorer->setWeight('generator_coverage', 0.8);

    $context = [
        'generators' => [
            ['status' => 'success'],
        ],
    ];

    $score = $scorer->score($context);

    expect($score->getWeights()['generator_coverage'])->toBe(0.8);
});

test('it throws exception when no metrics are registered', function () {
    $scorer = new QualityScorer();

    $scorer->score([]);
})->throws(RuntimeException::class);

test('it validates weight bounds', function () {
    $scorer = new QualityScorer();

    $scorer->setWeight('test', 1.5);
})->throws(InvalidArgumentException::class);

test('it handles zero total weight gracefully', function () {
    $scorer = new QualityScorer();
    $scorer->addMetric(new GeneratorCoverageMetric());
    $scorer->setWeight('generator_coverage', 0.0);

    $score = $scorer->score(['generators' => []]);

    expect($score->getOverallScore())->toBe(0.0);
});
