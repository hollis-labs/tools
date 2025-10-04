<?php

declare(strict_types=1);

use Symfony\Component\Filesystem\Filesystem;
use Symfony\Component\Process\Process;

test('it prepares the context pack workspace', function (): void {
    $filesystem = new Filesystem;
    $workspace = sys_get_temp_dir() . '/lcp-test-' . bin2hex(random_bytes(5));
    $projectPath = $workspace . '/project';
    $outputPath = $workspace . '/output';

    $filesystem->mkdir([
        $workspace,
        $projectPath,
        $projectPath . '/storage/framework/cache',
    ]);

    file_put_contents($projectPath . '/composer.json', json_encode([
        'name' => 'acme/example-app',
    ], JSON_PRETTY_PRINT));

    file_put_contents($projectPath . '/composer.lock', json_encode([
        'packages' => [
            [
                'name' => 'laravel/framework',
                'version' => 'v11.9.0',
            ],
            [
                'name' => 'laravel/pint',
                'version' => 'v1.6.0',
            ],
            [
                'name' => 'pestphp/pest',
                'version' => 'v2.0.0',
            ],
        ],
    ], JSON_PRETTY_PRINT));

    file_put_contents($projectPath . '/.env', "DB_CONNECTION=mysql\n");

    $artisanStub = <<<'PHP'
    #!/usr/bin/env php
    <?php
    $schemaPath = null;
    foreach ($argv as $argument) {
        if (str_starts_with($argument, '--schema-path=')) {
            $schemaPath = trim(substr($argument, strlen('--schema-path=')), "'\"");
        }
    }

    if ($schemaPath === null) {
        fwrite(STDERR, 'Missing schema path argument.');
        exit(1);
    }

    file_put_contents($schemaPath, "-- Schema dump\nCREATE TABLE users (...);\n");
    echo 'Schema dump written.';
    PHP;
    file_put_contents($projectPath . '/artisan', $artisanStub);
    chmod($projectPath . '/artisan', 0755);

    $process = Process::fromShellCommandline(
        sprintf(
            'php %s generate --project=%s --output=%s --force',
            escapeshellarg(__DIR__ . '/../../bin/context-pack.php'),
            escapeshellarg($projectPath),
            escapeshellarg($outputPath)
        ),
        __DIR__ . '/../../'
    );

    $process->mustRun();

    expect(is_dir($outputPath))->toBeTrue();
    expect(is_dir($outputPath . '/logs'))->toBeTrue();
    expect(is_file($outputPath . '/metadata.json'))->toBeTrue();
    expect(is_file($outputPath . '/01-schema-dump.sql'))->toBeTrue();

    $metadata = json_decode(file_get_contents($outputPath . '/metadata.json') ?: 'null', true);
    expect($metadata['project']['name'])->toBe('acme/example-app');
    expect($metadata['database']['driver'])->toBe('mysql');
    expect($metadata['detectedTools']['pint']['available'])->toBeTrue();
    expect($metadata['artifacts']['schemaDump']['status'])->toBe('success');

    $schemaContents = file_get_contents($outputPath . '/01-schema-dump.sql');
    expect($schemaContents)->toContain('-- Schema dump');

    $logContents = file_get_contents($outputPath . '/logs/schema-dump.log');
    expect($logContents)->toContain('Schema dump copied to');

    $filesystem->remove($workspace);
});
