<?php

declare(strict_types=1);

use LaravelContextPack\Generator\StaticAnalysisGenerator;
use Symfony\Component\Filesystem\Filesystem;

beforeEach(function (): void {
    $this->filesystem = new Filesystem;
    $this->workspace = sys_get_temp_dir() . '/lcp-tools-test-' . bin2hex(random_bytes(5));
    $this->projectPath = $this->workspace . '/project';
    $this->outputPath = $this->workspace . '/output';

    $this->filesystem->mkdir([
        $this->workspace,
        $this->projectPath . '/vendor/bin',
        $this->outputPath . '/logs',
    ]);
});

afterEach(function (): void {
    $this->filesystem->remove($this->workspace);
});

test('all tools available scenario', function (): void {
    $detectedTools = [
        'pint' => ['available' => true],
        'phpstan' => ['available' => true],
        'pest' => ['available' => true],
        'rector' => ['available' => true],
        'larastan' => ['available' => false],
    ];

    foreach (['pint', 'phpstan', 'pest', 'rector'] as $tool) {
        file_put_contents($this->projectPath . '/vendor/bin/' . $tool, <<<'BASH'
        #!/usr/bin/env bash
        echo "Tool OK"
        exit 0
        BASH);
    }

    file_put_contents($this->projectPath . '/phpunit.xml', '<phpunit/>');

    $generator = new StaticAnalysisGenerator;
    $result = $generator->generate($this->projectPath, $this->outputPath, $detectedTools);

    expect($result['status'])->toBe('success');
    expect($result['tools']['pint']['status'])->toBe('success');
    expect($result['tools']['phpstan']['status'])->toBe('success');
    expect($result['tools']['pest']['status'])->toBe('success');
    expect($result['tools']['rector']['status'])->toBe('success');
    expect($result['tools']['larastan']['status'])->toBe('skipped');
});

test('partial tools available scenario', function (): void {
    $detectedTools = [
        'pint' => ['available' => true],
        'phpstan' => ['available' => true],
        'pest' => ['available' => false],
        'rector' => ['available' => false],
        'larastan' => ['available' => false],
    ];

    file_put_contents($this->projectPath . '/vendor/bin/pint', <<<'BASH'
    #!/usr/bin/env bash
    exit 0
    BASH);

    file_put_contents($this->projectPath . '/vendor/bin/phpstan', <<<'BASH'
    #!/usr/bin/env bash
    exit 0
    BASH);

    $generator = new StaticAnalysisGenerator;
    $result = $generator->generate($this->projectPath, $this->outputPath, $detectedTools);

    expect($result['status'])->toBe('success');
    expect($result['tools']['pint']['status'])->toBe('success');
    expect($result['tools']['phpstan']['status'])->toBe('success');
    expect($result['tools']['pest']['status'])->toBe('skipped');
    expect($result['tools']['rector']['status'])->toBe('skipped');
});

test('no tools available scenario', function (): void {
    $detectedTools = [
        'pint' => ['available' => false],
        'phpstan' => ['available' => false],
        'pest' => ['available' => false],
        'rector' => ['available' => false],
        'larastan' => ['available' => false],
    ];

    $generator = new StaticAnalysisGenerator;
    $result = $generator->generate($this->projectPath, $this->outputPath, $detectedTools);

    expect($result['status'])->toBe('skipped');
    expect($result['message'])->toContain('No static analysis tools');
});

test('some tools fail scenario', function (): void {
    $detectedTools = [
        'pint' => ['available' => true],
        'phpstan' => ['available' => true],
        'pest' => ['available' => false],
        'rector' => ['available' => false],
        'larastan' => ['available' => false],
    ];

    file_put_contents($this->projectPath . '/vendor/bin/pint', <<<'BASH'
    #!/usr/bin/env bash
    exit 0
    BASH);

    file_put_contents($this->projectPath . '/vendor/bin/phpstan', <<<'BASH'
    #!/usr/bin/env bash
    echo "Found 5 errors"
    exit 1
    BASH);

    $generator = new StaticAnalysisGenerator;
    $result = $generator->generate($this->projectPath, $this->outputPath, $detectedTools);

    expect($result['status'])->toBe('partial');
    expect($result['message'])->toContain('Some static analysis tools failed');
    expect($result['tools']['pint']['status'])->toBe('success');
    expect($result['tools']['phpstan']['status'])->toBe('failed');
});

test('both PHPStan and Larastan available prefers configuration', function (): void {
    $detectedTools = [
        'phpstan' => ['available' => true],
        'larastan' => ['available' => true],
        'pint' => ['available' => false],
        'pest' => ['available' => false],
        'rector' => ['available' => false],
    ];

    file_put_contents($this->projectPath . '/phpstan.neon', 'parameters:');

    file_put_contents($this->projectPath . '/vendor/bin/phpstan', <<<'BASH'
    #!/usr/bin/env bash
    echo "PHPStan OK"
    exit 0
    BASH);

    $generator = new StaticAnalysisGenerator;
    $result = $generator->generate($this->projectPath, $this->outputPath, $detectedTools);

    // Both should run since they're treated as separate tools
    expect($result['tools']['phpstan']['status'])->toBe('success');
    expect($result['tools']['larastan']['status'])->toBe('success');
});
