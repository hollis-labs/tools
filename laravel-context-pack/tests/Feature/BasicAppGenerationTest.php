<?php

declare(strict_types=1);

use Symfony\Component\Filesystem\Filesystem;
use Symfony\Component\Process\Process;

beforeEach(function (): void {
    $this->filesystem = new Filesystem;
    $this->fixturesPath = __DIR__ . '/../fixtures/basic-app';
    $this->outputPath = sys_get_temp_dir() . '/lcp-e2e-basic-' . bin2hex(random_bytes(5));
});

afterEach(function (): void {
    if (isset($this->outputPath) && is_dir($this->outputPath)) {
        $this->filesystem->remove($this->outputPath);
    }
});

test('it generates complete context pack for basic Laravel app', function (): void {
    $process = Process::fromShellCommandline(
        sprintf(
            '%s %s generate --project=%s --output=%s --force',
            escapeshellarg(PHP_BINARY),
            escapeshellarg(__DIR__ . '/../../bin/context-pack.php'),
            escapeshellarg($this->fixturesPath),
            escapeshellarg($this->outputPath)
        ),
        __DIR__ . '/../../'
    );

    $process->mustRun();

    expect($process->getExitCode())->toBe(0);
    expect(is_dir($this->outputPath))->toBeTrue();
    expect(is_dir($this->outputPath . '/logs'))->toBeTrue();
});

test('it creates all expected artifacts', function (): void {
    $process = Process::fromShellCommandline(
        sprintf(
            '%s %s generate --project=%s --output=%s --force',
            escapeshellarg(PHP_BINARY),
            escapeshellarg(__DIR__ . '/../../bin/context-pack.php'),
            escapeshellarg($this->fixturesPath),
            escapeshellarg($this->outputPath)
        ),
        __DIR__ . '/../../'
    );

    $process->mustRun();

    expect(file_exists($this->outputPath . '/metadata.json'))->toBeTrue();
    expect(file_exists($this->outputPath . '/01-schema-dump.sql'))->toBeTrue();
    expect(file_exists($this->outputPath . '/02-route-summary.md'))->toBeTrue();
    expect(file_exists($this->outputPath . '/02-route-summary.json'))->toBeTrue();
    expect(file_exists($this->outputPath . '/03-model-overview.md'))->toBeTrue();
});

test('it validates metadata.json structure', function (): void {
    $process = Process::fromShellCommandline(
        sprintf(
            '%s %s generate --project=%s --output=%s --force',
            escapeshellarg(PHP_BINARY),
            escapeshellarg(__DIR__ . '/../../bin/context-pack.php'),
            escapeshellarg($this->fixturesPath),
            escapeshellarg($this->outputPath)
        ),
        __DIR__ . '/../../'
    );

    $process->mustRun();

    $metadata = json_decode(file_get_contents($this->outputPath . '/metadata.json'), true);

    expect($metadata)->toHaveKey('generatedAt');
    expect($metadata)->toHaveKey('project');
    expect($metadata)->toHaveKey('laravel');
    expect($metadata)->toHaveKey('database');
    expect($metadata)->toHaveKey('artifacts');

    expect($metadata['project']['name'])->toBe('test/basic-app');
    expect($metadata['database']['driver'])->toBe('sqlite');
    expect($metadata['artifacts']['schemaDump']['status'])->toBe('success');
    expect($metadata['artifacts']['routeSummary']['status'])->toBe('success');
    expect($metadata['artifacts']['modelOverview']['status'])->toBe('success');
});

test('it validates schema dump content', function (): void {
    $process = Process::fromShellCommandline(
        sprintf(
            '%s %s generate --project=%s --output=%s --force',
            escapeshellarg(PHP_BINARY),
            escapeshellarg(__DIR__ . '/../../bin/context-pack.php'),
            escapeshellarg($this->fixturesPath),
            escapeshellarg($this->outputPath)
        ),
        __DIR__ . '/../../'
    );

    $process->mustRun();

    $schema = file_get_contents($this->outputPath . '/01-schema-dump.sql');

    expect($schema)->toContain('CREATE TABLE');
    expect($schema)->toContain('users');
    expect($schema)->toContain('posts');
});

test('it validates route summary content', function (): void {
    $process = Process::fromShellCommandline(
        sprintf(
            '%s %s generate --project=%s --output=%s --force',
            escapeshellarg(PHP_BINARY),
            escapeshellarg(__DIR__ . '/../../bin/context-pack.php'),
            escapeshellarg($this->fixturesPath),
            escapeshellarg($this->outputPath)
        ),
        __DIR__ . '/../../'
    );

    $process->mustRun();

    $markdown = file_get_contents($this->outputPath . '/02-route-summary.md');
    $json = json_decode(file_get_contents($this->outputPath . '/02-route-summary.json'), true);

    expect($markdown)->toContain('# Route Summary');
    expect($markdown)->toContain('## Metrics');
    expect($markdown)->toContain('Total routes:');

    expect($json)->toBeArray();
    expect(count($json))->toBeGreaterThan(0);
    expect($json[0])->toHaveKey('method');
    expect($json[0])->toHaveKey('uri');
    expect($json[0])->toHaveKey('name');
});

test('it validates model overview content', function (): void {
    $process = Process::fromShellCommandline(
        sprintf(
            '%s %s generate --project=%s --output=%s --force',
            escapeshellarg(PHP_BINARY),
            escapeshellarg(__DIR__ . '/../../bin/context-pack.php'),
            escapeshellarg($this->fixturesPath),
            escapeshellarg($this->outputPath)
        ),
        __DIR__ . '/../../'
    );

    $process->mustRun();

    $content = file_get_contents($this->outputPath . '/03-model-overview.md');

    expect($content)->toContain('# Model Overview');
    expect($content)->toContain('App\\Models\\User');
    expect($content)->toContain('App\\Models\\Post');
    expect($content)->toContain('App\\Models\\Comment');
    expect($content)->toContain('```mermaid');
    expect($content)->toContain('erDiagram');
});

test('it validates Mermaid ERD syntax', function (): void {
    $process = Process::fromShellCommandline(
        sprintf(
            '%s %s generate --project=%s --output=%s --force',
            escapeshellarg(PHP_BINARY),
            escapeshellarg(__DIR__ . '/../../bin/context-pack.php'),
            escapeshellarg($this->fixturesPath),
            escapeshellarg($this->outputPath)
        ),
        __DIR__ . '/../../'
    );

    $process->mustRun();

    $content = file_get_contents($this->outputPath . '/03-model-overview.md');

    // Extract Mermaid diagram
    preg_match('/```mermaid\n(.*?)\n```/s', $content, $matches);
    expect($matches)->toHaveCount(2);

    $diagram = $matches[1];
    expect($diagram)->toContain('erDiagram');
    expect($diagram)->toContain('%% Legend:');

    // Validate relationship syntax
    expect($diagram)->toMatch('/\w+\s+(\|\|--o\{|\|\|--\|\||o\{--\|\|)\s+\w+/');
});

test('it creates log files', function (): void {
    $process = Process::fromShellCommandline(
        sprintf(
            '%s %s generate --project=%s --output=%s --force',
            escapeshellarg(PHP_BINARY),
            escapeshellarg(__DIR__ . '/../../bin/context-pack.php'),
            escapeshellarg($this->fixturesPath),
            escapeshellarg($this->outputPath)
        ),
        __DIR__ . '/../../'
    );

    $process->mustRun();

    expect(file_exists($this->outputPath . '/logs/schema-dump.log'))->toBeTrue();
    expect(file_exists($this->outputPath . '/logs/route-summary.log'))->toBeTrue();
    expect(file_exists($this->outputPath . '/logs/model-overview.log'))->toBeTrue();
});

test('it overwrites existing output with --force flag', function (): void {
    // First generation
    $process1 = Process::fromShellCommandline(
        sprintf(
            '%s %s generate --project=%s --output=%s',
            escapeshellarg(PHP_BINARY),
            escapeshellarg(__DIR__ . '/../../bin/context-pack.php'),
            escapeshellarg($this->fixturesPath),
            escapeshellarg($this->outputPath)
        ),
        __DIR__ . '/../../'
    );

    $process1->mustRun();

    $firstMetadata = file_get_contents($this->outputPath . '/metadata.json');

    // Wait a second to ensure different timestamp
    sleep(1);

    // Second generation with --force
    $process2 = Process::fromShellCommandline(
        sprintf(
            '%s %s generate --project=%s --output=%s --force',
            escapeshellarg(PHP_BINARY),
            escapeshellarg(__DIR__ . '/../../bin/context-pack.php'),
            escapeshellarg($this->fixturesPath),
            escapeshellarg($this->outputPath)
        ),
        __DIR__ . '/../../'
    );

    $process2->mustRun();

    $secondMetadata = file_get_contents($this->outputPath . '/metadata.json');

    expect($firstMetadata)->not->toBe($secondMetadata);
});
