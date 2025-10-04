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
    $command = $argv[1] ?? null;

    if ($command === null) {
        echo 'Laravel Framework 11.9.0';
        exit(0);
    }

    if ($command === 'schema:dump') {
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
        exit(0);
    }

    if ($command === 'route:list') {
        $routes = [
            [
                'method' => ['GET', 'HEAD'],
                'uri' => 'api/users',
                'name' => 'api.users.index',
                'action' => 'App\\Http\\Controllers\\Api\\UserController@index',
                'middleware' => ['api', 'auth:sanctum'],
                'prefix' => 'api',
            ],
        ];

        if (in_array('--json', $argv, true)) {
            echo json_encode($routes, JSON_PRETTY_PRINT);
        } else {
            echo "GET|HEAD        api/users        api.users.index        App\\\\Http\\\\Controllers\\\\Api\\\\UserController@index        api,auth:sanctum\n";
        }

        exit(0);
    }

    echo 'Laravel Framework 11.9.0';
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
    expect(is_file($outputPath . '/02-route-summary.md'))->toBeTrue();

    $metadata = json_decode(file_get_contents($outputPath . '/metadata.json') ?: 'null', true);
    expect($metadata['project']['name'])->toBe('acme/example-app');
    expect($metadata['database']['driver'])->toBe('mysql');
    expect($metadata['detectedTools']['pint']['available'])->toBeTrue();
    expect($metadata['artifacts']['schemaDump']['status'])->toBe('success');
    expect($metadata['artifacts']['routeSummary']['status'])->toBe('success');

    $schemaContents = file_get_contents($outputPath . '/01-schema-dump.sql');
    expect($schemaContents)->toContain('-- Schema dump');

    $logContents = file_get_contents($outputPath . '/logs/schema-dump.log');
    expect($logContents)->toContain('Schema dump copied to');

    $routeSummary = file_get_contents($outputPath . '/02-route-summary.md');
    expect($routeSummary)->toContain('| GET,HEAD | api/users');

    $routeLog = file_get_contents($outputPath . '/logs/route-summary.log');
    expect($routeLog)->toContain('route:list --json');

    $filesystem->remove($workspace);
});
