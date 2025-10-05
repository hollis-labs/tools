<?php

declare(strict_types=1);

use LaravelContextPack\Quality\Scoring\Metric\GeneratorCoverageMetric;

test('it calculates perfect coverage when all generators succeed', function () {
    $metric = new GeneratorCoverageMetric();

    $context = [
        'generators' => [
            ['status' => 'success'],
            ['status' => 'success'],
            ['status' => 'success'],
        ],
    ];

    expect($metric->calculate($context))->toBe(1.0);
});

test('it calculates partial coverage when some generators fail', function () {
    $metric = new GeneratorCoverageMetric();

    $context = [
        'generators' => [
            ['status' => 'success'],
            ['status' => 'failed'],
            ['status' => 'success'],
            ['status' => 'failed'],
        ],
    ];

    expect($metric->calculate($context))->toBe(0.5);
});

test('it returns zero when all generators fail', function () {
    $metric = new GeneratorCoverageMetric();

    $context = [
        'generators' => [
            ['status' => 'failed'],
            ['status' => 'failed'],
        ],
    ];

    expect($metric->calculate($context))->toBe(0.0);
});

test('it returns zero when no generators exist', function () {
    $metric = new GeneratorCoverageMetric();

    expect($metric->calculate([]))->toBe(0.0);
    expect($metric->calculate(['generators' => []]))->toBe(0.0);
});

test('it has correct metadata', function () {
    $metric = new GeneratorCoverageMetric();

    expect($metric->getName())->toBe('generator_coverage');
    expect($metric->getDescription())->toBeString();
    expect($metric->getDefaultWeight())->toBeGreaterThan(0.0);
    expect($metric->getDefaultWeight())->toBeLessThanOrEqual(1.0);
});
