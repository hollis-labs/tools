<?php

declare(strict_types=1);

use LaravelContextPack\Optimizer\ApproximateTokenCounter;
use LaravelContextPack\Optimizer\GeneratorOutput;
use LaravelContextPack\Optimizer\Strategy\ProportionalStrategy;

test('proportional strategy returns unchanged outputs when within budget', function () {
    $strategy = new ProportionalStrategy();
    $counter = new ApproximateTokenCounter();

    $outputs = [
        new GeneratorOutput('schema', 'Short content', 1.0),
        new GeneratorOutput('routes', 'Another short piece', 1.0),
    ];

    $result = $strategy->summarize($outputs, 1000, $counter);

    expect($result->outputs)->toHaveCount(2)
        ->and($result->wasTruncated())->toBeFalse()
        ->and($result->getStrategy())->toBe('proportional');
});

test('proportional strategy allocates budget proportionally', function () {
    $strategy = new ProportionalStrategy();
    $counter = new ApproximateTokenCounter();

    // Create outputs with different sizes
    $largeContent = str_repeat('Lorem ipsum dolor sit amet. ', 200); // ~1400 chars = ~350 tokens
    $smallContent = str_repeat('Short. ', 20); // ~140 chars = ~35 tokens

    $outputs = [
        new GeneratorOutput('large', $largeContent, 1.0),
        new GeneratorOutput('small', $smallContent, 1.0),
    ];

    // Total ~385 tokens, set budget to 200 tokens
    $result = $strategy->summarize($outputs, 200, $counter);

    expect($result->outputs)->toHaveCount(2)
        ->and($result->wasTruncated())->toBeTrue()
        ->and($result->getTotalTokens())->toBeLessThanOrEqual(200);

    // Large output should get more tokens than small
    $largeOutput = $result->outputs[0];
    $smallOutput = $result->outputs[1];

    expect($counter->count($largeOutput->content))->toBeGreaterThan($counter->count($smallOutput->content));
});

test('proportional strategy maintains minimum content per generator', function () {
    $strategy = new ProportionalStrategy();
    $counter = new ApproximateTokenCounter();

    $outputs = [
        new GeneratorOutput('gen1', str_repeat('Content. ', 500), 1.0),
        new GeneratorOutput('gen2', str_repeat('Content. ', 500), 1.0),
        new GeneratorOutput('gen3', str_repeat('Content. ', 500), 1.0),
    ];

    // Very restrictive budget
    $result = $strategy->summarize($outputs, 400, $counter);

    expect($result->outputs)->toHaveCount(3)
        ->and($result->wasTruncated())->toBeTrue();

    // Each should have at least some content (minimum 100 tokens enforced)
    foreach ($result->outputs as $output) {
        expect($counter->count($output->content))->toBeGreaterThan(50);
    }
});

test('proportional strategy handles empty outputs array', function () {
    $strategy = new ProportionalStrategy();
    $counter = new ApproximateTokenCounter();

    $result = $strategy->summarize([], 1000, $counter);

    expect($result->outputs)->toBeEmpty()
        ->and($result->wasTruncated())->toBeFalse()
        ->and($result->getTotalTokens())->toBe(0);
});

test('proportional strategy includes metadata about truncation', function () {
    $strategy = new ProportionalStrategy();
    $counter = new ApproximateTokenCounter();

    $outputs = [
        new GeneratorOutput('gen1', str_repeat('Content. ', 300), 1.0),
    ];

    $result = $strategy->summarize($outputs, 200, $counter);

    $output = $result->outputs[0];
    expect($output->metadata)->toHaveKey('truncated')
        ->and($output->metadata['truncated'])->toBeTrue()
        ->and($output->metadata)->toHaveKey('originalTokens')
        ->and($output->metadata)->toHaveKey('allocatedTokens');
});
