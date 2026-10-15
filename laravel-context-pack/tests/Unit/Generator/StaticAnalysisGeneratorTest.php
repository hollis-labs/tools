<?php

declare(strict_types=1);

use LaravelContextPack\Generator\StaticAnalysisGenerator;
use Symfony\Component\Filesystem\Filesystem;

beforeEach(function (): void {
    $this->filesystem = new Filesystem;
    $this->workspace = sys_get_temp_dir() . '/lcp-static-test-' . bin2hex(random_bytes(5));
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

test('it skips tools that are not available', function (): void {
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
    expect($result['tools']['pint']['status'])->toBe('skipped');
    expect($result['tools']['phpstan']['status'])->toBe('skipped');
});

test('it runs available tools successfully', function (): void {
    $detectedTools = [
        'pint' => ['available' => true],
        'phpstan' => ['available' => false],
        'pest' => ['available' => false],
        'rector' => ['available' => false],
        'larastan' => ['available' => false],
    ];

    file_put_contents($this->projectPath . '/vendor/bin/pint', <<<'BASH'
    #!/usr/bin/env bash
    echo "Pint OK - no issues found"
    exit 0
    BASH);

    $generator = new StaticAnalysisGenerator;
    $result = $generator->generate($this->projectPath, $this->outputPath, $detectedTools);

    expect($result['status'])->toBe('success');
    expect($result['message'])->toContain('successfully');
    expect($result['tools']['pint']['status'])->toBe('success');
    expect($result['tools']['pint']['issues'])->toBe(0);
    expect($result['tools']['phpstan']['status'])->toBe('skipped');
});

test('it handles tool failures', function (): void {
    $detectedTools = [
        'pint' => ['available' => true],
        'phpstan' => ['available' => false],
        'pest' => ['available' => false],
        'rector' => ['available' => false],
        'larastan' => ['available' => false],
    ];

    file_put_contents($this->projectPath . '/vendor/bin/pint', <<<'BASH'
    #!/usr/bin/env bash
    echo "Pint found 5 errors"
    exit 1
    BASH);

    $generator = new StaticAnalysisGenerator;
    $result = $generator->generate($this->projectPath, $this->outputPath, $detectedTools);

    expect($result['status'])->toBe('failed');
    expect($result['tools']['pint']['status'])->toBe('failed');
    expect($result['tools']['pint']['issues'])->toBeGreaterThan(0);
});

test('it counts issues from tool output', function (): void {
    $detectedTools = [
        'pest' => ['available' => true],
        'pint' => ['available' => false],
        'phpstan' => ['available' => false],
        'rector' => ['available' => false],
        'larastan' => ['available' => false],
    ];

    file_put_contents($this->projectPath . '/vendor/bin/pest', <<<'BASH'
    #!/usr/bin/env bash
    echo "Tests: 5 failed, 10 passed"
    exit 1
    BASH);

    file_put_contents($this->projectPath . '/phpunit.xml', '<phpunit/>');

    $generator = new StaticAnalysisGenerator;
    $result = $generator->generate($this->projectPath, $this->outputPath, $detectedTools);

    expect($result['tools']['pest']['issues'])->toBeGreaterThan(0);
});

test('it adds PHPStan configuration when available', function (): void {
    $detectedTools = [
        'phpstan' => ['available' => true],
        'pint' => ['available' => false],
        'pest' => ['available' => false],
        'rector' => ['available' => false],
        'larastan' => ['available' => false],
    ];

    file_put_contents($this->projectPath . '/phpstan.neon', 'parameters:');
    file_put_contents($this->projectPath . '/vendor/bin/phpstan', <<<'BASH'
    #!/usr/bin/env bash
    # Check if --configuration flag is present
    if [[ "$*" == *"--configuration"* ]]; then
        echo "PHPStan with config - OK"
        exit 0
    fi
    echo "PHPStan without config"
    exit 1
    BASH);

    $generator = new StaticAnalysisGenerator;
    $result = $generator->generate($this->projectPath, $this->outputPath, $detectedTools);

    expect($result['tools']['phpstan']['status'])->toBe('success');

    $log = file_get_contents($this->outputPath . '/logs/static-analysis.log');
    expect($log)->toContain('--configuration');
});

test('it writes markdown summary', function (): void {
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

    expect(file_exists($this->outputPath . '/04-static-analysis.md'))->toBeTrue();

    $markdown = file_get_contents($this->outputPath . '/04-static-analysis.md');
    expect($markdown)->toContain('# Static Analysis Summary');
    expect($markdown)->toContain('| Tool | Status | Issues | Details |');
    expect($markdown)->toContain('Laravel Pint');
    expect($markdown)->toContain('PHPStan');
    expect($markdown)->toContain('SUCCESS');
});

test('it handles partial success status', function (): void {
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
    echo "Found 3 errors"
    exit 1
    BASH);

    $generator = new StaticAnalysisGenerator;
    $result = $generator->generate($this->projectPath, $this->outputPath, $detectedTools);

    expect($result['status'])->toBe('partial');
    expect($result['message'])->toContain('Some static analysis tools failed');
});

test('it logs tool execution', function (): void {
    $detectedTools = [
        'pint' => ['available' => true],
        'phpstan' => ['available' => false],
        'pest' => ['available' => false],
        'rector' => ['available' => false],
        'larastan' => ['available' => false],
    ];

    file_put_contents($this->projectPath . '/vendor/bin/pint', <<<'BASH'
    #!/usr/bin/env bash
    echo "Pint output here"
    exit 0
    BASH);

    $generator = new StaticAnalysisGenerator;
    $result = $generator->generate($this->projectPath, $this->outputPath, $detectedTools);

    expect(file_exists($this->outputPath . '/logs/static-analysis.log'))->toBeTrue();

    $log = file_get_contents($this->outputPath . '/logs/static-analysis.log');
    expect($log)->toContain('Running Laravel Pint');
    expect($log)->toContain('Pint output here');
});

test('it runs all available tools', function (): void {
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
