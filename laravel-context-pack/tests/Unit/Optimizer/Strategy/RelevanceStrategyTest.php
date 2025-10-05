<?php

declare(strict_types=1);

use LaravelContextPack\Optimizer\ApproximateTokenCounter;
use LaravelContextPack\Optimizer\GeneratorOutput;
use LaravelContextPack\Optimizer\Strategy\RelevanceStrategy;

test('relevance strategy returns unchanged outputs when within budget', function () {
    $strategy = new RelevanceStrategy();
    $counter = new ApproximateTokenCounter();

    $outputs = [
        new GeneratorOutput('schema', 'Short content', 1.0),
        new GeneratorOutput('routes', 'Another short piece', 0.8),
    ];

    $result = $strategy->summarize($outputs, 1000, $counter);

    expect($result->outputs)->toHaveCount(2)
        ->and($result->wasTruncated())->toBeFalse()
        ->and($result->getStrategy())->toBe('relevance');
});

test('relevance strategy prioritizes high-relevance content', function () {
    $strategy = new RelevanceStrategy();
    $counter = new ApproximateTokenCounter();

    $outputs = [
        new GeneratorOutput('low', str_repeat('Low priority content. ', 50), 0.3),
        new GeneratorOutput('high', str_repeat('High priority content. ', 50), 0.9),
        new GeneratorOutput('medium', str_repeat('Medium priority content. ', 50), 0.6),
    ];

    // Budget allows only ~2 outputs
    $result = $strategy->summarize($outputs, 400, $counter);

    expect($result->wasTruncated())->toBeTrue();

    // High relevance should be included first
    $generatorNames = array_map(fn ($o) => $o->generatorName, $result->outputs);
    expect($generatorNames[0])->toBe('high');
});

test('relevance strategy excludes lowest priority content when budget exceeded', function () {
    $strategy = new RelevanceStrategy();
    $counter = new ApproximateTokenCounter();

    $outputs = [
        new GeneratorOutput('essential', str_repeat('Essential. ', 100), 1.0),
        new GeneratorOutput('important', str_repeat('Important. ', 100), 0.8),
        new GeneratorOutput('optional', str_repeat('Optional. ', 100), 0.2),
    ];

    // Budget for ~2 outputs
    $result = $strategy->summarize($outputs, 700, $counter);

    $includedNames = array_map(fn ($o) => $o->generatorName, $result->outputs);

    expect($includedNames)->toContain('essential')
        ->and($includedNames)->toContain('important')
        ->and($result->metadata)->toHaveKey('excludedGenerators');
});

test('relevance strategy handles empty outputs array', function () {
    $strategy = new RelevanceStrategy();
    $counter = new ApproximateTokenCounter();

    $result = $strategy->summarize([], 1000, $counter);

    expect($result->outputs)->toBeEmpty()
        ->and($result->wasTruncated())->toBeFalse()
        ->and($result->getTotalTokens())->toBe(0);
});

test('relevance strategy tracks excluded outputs in metadata', function () {
    $strategy = new RelevanceStrategy();
    $counter = new ApproximateTokenCounter();

    $outputs = [
        new GeneratorOutput('gen1', str_repeat('Content. ', 200), 0.9),
        new GeneratorOutput('gen2', str_repeat('Content. ', 200), 0.5),
        new GeneratorOutput('gen3', str_repeat('Content. ', 200), 0.1),
    ];

    $result = $strategy->summarize($outputs, 600, $counter);

    expect($result->metadata)->toHaveKey('excludedOutputs')
        ->and($result->metadata)->toHaveKey('excludedGenerators')
        ->and($result->metadata['excludedGenerators'])->toBeArray();
});

test('relevance strategy partially includes content when budget allows', function () {
    $strategy = new RelevanceStrategy();
    $counter = new ApproximateTokenCounter();

    $outputs = [
        new GeneratorOutput('gen1', str_repeat('High priority. ', 50), 1.0),
        new GeneratorOutput('gen2', str_repeat('Medium priority. ', 150), 0.5),
    ];

    // Budget allows gen1 + partial gen2
    $result = $strategy->summarize($outputs, 400, $counter);

    expect($result->outputs)->toHaveCount(2);

    $secondOutput = $result->outputs[1];
    expect($secondOutput->metadata)->toHaveKey('truncated')
        ->and($secondOutput->metadata['truncated'])->toBeTrue();
});
