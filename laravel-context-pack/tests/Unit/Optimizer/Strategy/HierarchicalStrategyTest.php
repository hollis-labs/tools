<?php

declare(strict_types=1);

use LaravelContextPack\Optimizer\ApproximateTokenCounter;
use LaravelContextPack\Optimizer\GeneratorOutput;
use LaravelContextPack\Optimizer\Strategy\HierarchicalStrategy;

test('hierarchical strategy returns unchanged outputs when within budget', function () {
    $strategy = new HierarchicalStrategy();
    $counter = new ApproximateTokenCounter();

    $outputs = [
        new GeneratorOutput('schema', 'Short content', 1.0),
        new GeneratorOutput('routes', 'Another short piece', 1.0),
    ];

    $result = $strategy->summarize($outputs, 1000, $counter);

    expect($result->outputs)->toHaveCount(2)
        ->and($result->wasTruncated())->toBeFalse()
        ->and($result->getStrategy())->toBe('hierarchical');
});

test('hierarchical strategy structures content in layers', function () {
    $strategy = new HierarchicalStrategy();
    $counter = new ApproximateTokenCounter();

    $content = <<<'MD'
# Summary

This is the summary section with key information.

## Key Insights

These are the important insights that should be preserved.

## Details

This section contains all the detailed information that can be truncated if needed.
More details here. Even more details. And even more after that.
MD;

    $outputs = [
        new GeneratorOutput('test', $content, 1.0),
    ];

    // Restrictive budget that forces summarization
    $result = $strategy->summarize($outputs, 100, $counter);

    expect($result->outputs)->toHaveCount(1);

    $output = $result->outputs[0];
    expect($output->content)->toContain('Summary')
        ->and($output->metadata)->toHaveKey('hierarchical')
        ->and($output->metadata['hierarchical'])->toBeTrue();
});

test('hierarchical strategy creates synthetic sections for unstructured content', function () {
    $strategy = new HierarchicalStrategy();
    $counter = new ApproximateTokenCounter();

    // Content without clear section headers
    $content = str_repeat("Line of content.\n", 100);

    $outputs = [
        new GeneratorOutput('test', $content, 1.0),
    ];

    $result = $strategy->summarize($outputs, 200, $counter);

    expect($result->outputs)->toHaveCount(1)
        ->and($result->wasTruncated())->toBeTrue();
});

test('hierarchical strategy handles empty outputs array', function () {
    $strategy = new HierarchicalStrategy();
    $counter = new ApproximateTokenCounter();

    $result = $strategy->summarize([], 1000, $counter);

    expect($result->outputs)->toBeEmpty()
        ->and($result->wasTruncated())->toBeFalse()
        ->and($result->getTotalTokens())->toBe(0);
});

test('hierarchical strategy preserves summary over details when budget limited', function () {
    $strategy = new HierarchicalStrategy();
    $counter = new ApproximateTokenCounter();

    $content = <<<'MD'
# Summary

Critical summary information that must be preserved.

## Important Details

Very long details section that should be truncated.
MD . str_repeat(' Additional detail content.', 200);

    $outputs = [
        new GeneratorOutput('test', $content, 1.0),
    ];

    $result = $strategy->summarize($outputs, 150, $counter);
    $output = $result->outputs[0];

    // Summary should be included
    expect($output->content)->toContain('Critical summary information');
});
