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
        $projectPath . '/app/Models',
        $projectPath . '/app/Http/Controllers/Api',
        $projectPath . '/vendor/bin',
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

    file_put_contents($projectPath . '/.env', "DB_CONNECTION=mysql\nDB_DATABASE=testing\nDB_USERNAME=root\n");

    file_put_contents($projectPath . '/phpunit.xml', '<phpunit/>' . PHP_EOL);

    $pintStub = <<<'BASH'
    #!/usr/bin/env bash
    echo "Pint OK"
    exit 0
    BASH;
    file_put_contents($projectPath . '/vendor/bin/pint', $pintStub);
    chmod($projectPath . '/vendor/bin/pint', 0755);

    $pestStub = <<<'BASH'
    #!/usr/bin/env bash
    echo "Pest OK"
    exit 0
    BASH;
    file_put_contents($projectPath . '/vendor/bin/pest', $pestStub);
    chmod($projectPath . '/vendor/bin/pest', 0755);

    $modelStub = <<<'PHP'
    <?php

    namespace App\Models;

    use Illuminate\Database\Eloquent\Model;
    use Illuminate\Database\Eloquent\SoftDeletes;

    class User extends Model
    {
        use SoftDeletes;

        protected $table = 'users';

        protected $fillable = ['name', 'email'];

        protected $casts = [
            'email_verified_at' => 'datetime',
        ];

        public function getNameAttribute($value)
        {
            return strtoupper($value);
        }

        public function setEmailAttribute($value)
        {
            $this->attributes['email'] = strtolower($value);
        }

        public function posts()
        {
            return $this->hasMany(Post::class);
        }
    }
    PHP;
    file_put_contents($projectPath . '/app/Models/User.php', $modelStub);

    $controllerStub = <<<'PHP'
    <?php

    namespace App\Http\Controllers\Api;

    class UserController
    {
        public function index(): void
        {
        }
    }
    PHP;
    file_put_contents($projectPath . '/app/Http/Controllers/Api/UserController.php', $controllerStub);

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
            '%s %s generate --project=%s --output=%s --force',
            escapeshellarg(PHP_BINARY),
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
    expect(is_file($outputPath . '/02-route-summary.json'))->toBeTrue();
    expect(is_file($outputPath . '/03-model-overview.md'))->toBeTrue();

    $metadata = json_decode(file_get_contents($outputPath . '/metadata.json') ?: 'null', true);
    expect($metadata['project']['name'])->toBe('acme/example-app');
    expect($metadata['database']['driver'])->toBe('mysql');
    expect($metadata['detectedTools']['pint']['available'])->toBeTrue();
    expect($metadata['artifacts']['schemaDump']['status'])->toBe('success');
    expect($metadata['artifacts']['routeSummary']['status'])->toBe('success');
    expect($metadata['artifacts']['modelOverview']['status'])->toBe('success');
    expect($metadata['artifacts']['staticAnalysis']['status'])->toBe('success');

    $schemaContents = file_get_contents($outputPath . '/01-schema-dump.sql');
    expect($schemaContents)->toContain('-- Schema dump');

    $logContents = file_get_contents($outputPath . '/logs/schema-dump.log');
    expect($logContents)->toContain('Schema dump copied to');

    $routeSummary = file_get_contents($outputPath . '/02-route-summary.md');
    expect($routeSummary)->toContain('| Method | URI | Name | Action | Middleware | Source |');
    expect($routeSummary)->toContain('## Metrics');

    $routeJson = json_decode(file_get_contents($outputPath . '/02-route-summary.json') ?: 'null', true);
    expect($routeJson[0]['method'])->toBe(['GET', 'HEAD']);
    expect($routeJson[0]['uri'])->toBe('api/users');
    expect($routeJson[0]['controllerPath'])->toEndWith('UserController.php');
    expect($routeJson[0]['controllerMethod'])->toBe('index');

    $routeLog = file_get_contents($outputPath . '/logs/route-summary.log');
    expect($routeLog)->toContain('route:list --json');

    $modelOverview = file_get_contents($outputPath . '/03-model-overview.md');
    expect($modelOverview)->toContain('App\\Models\\User');
    expect($modelOverview)->toContain('Soft Deletes: yes');
    expect($modelOverview)->toContain('Casts: `email_verified_at: datetime`');
    expect($modelOverview)->toContain('Accessor: `name`');
    expect($modelOverview)->toContain('Mutator: `email`');
    expect($modelOverview)->toContain('| HASMANY | Post |');

    $modelLog = file_get_contents($outputPath . '/logs/model-overview.log');
    expect($modelLog)->toContain('Model overview generated');

    $staticAnalysis = file_get_contents($outputPath . '/04-static-analysis.md');
    expect($staticAnalysis)->toContain('# Static Analysis Summary');
    expect($staticAnalysis)->toContain('Laravel Pint');

    $staticLog = file_get_contents($outputPath . '/logs/static-analysis.log');
    expect($staticLog)->toContain('Running Laravel Pint');

    $filesystem->remove($workspace);
});

test('it logs schema fallback when binaries are missing', function (): void {
    $filesystem = new Filesystem;
    $workspace = sys_get_temp_dir() . '/lcp-test-' . bin2hex(random_bytes(5));
    $projectPath = $workspace . '/project';
    $outputPath = $workspace . '/output';

    $filesystem->mkdir([
        $workspace,
        $projectPath,
        $projectPath . '/storage/framework/cache',
        $projectPath . '/database',
    ]);

    file_put_contents($projectPath . '/composer.json', json_encode([
        'name' => 'acme/sqlite-app',
    ], JSON_PRETTY_PRINT));

    file_put_contents($projectPath . '/composer.lock', json_encode([
        'packages' => [
            [
                'name' => 'laravel/framework',
                'version' => 'v11.9.0',
            ],
        ],
    ], JSON_PRETTY_PRINT));

    $sqlitePath = $projectPath . '/database/database.sqlite';
    $filesystem->dumpFile($sqlitePath, '');

    file_put_contents($projectPath . '/.env', "DB_CONNECTION=sqlite\nDB_DATABASE=database/database.sqlite\n");

    $artisanStub = <<<'PHP'
    #!/usr/bin/env php
    <?php
    $command = $argv[1] ?? null;

    if ($command === null) {
        echo 'Laravel Framework 11.9.0';
        exit(0);
    }

    if ($command === 'schema:dump') {
        fwrite(STDERR, 'schema dump failed');
        exit(1);
    }

    if ($command === 'route:list') {
        echo '[]';
        exit(0);
    }

    echo 'Laravel Framework 11.9.0';
    PHP;
    file_put_contents($projectPath . '/artisan', $artisanStub);
    chmod($projectPath . '/artisan', 0755);

    $binSandbox = $workspace . '/bin';
    $filesystem->mkdir($binSandbox);
    symlink(PHP_BINARY, $binSandbox . '/php');

    $originalPath = getenv('PATH');
    putenv('PATH=' . $binSandbox);

    try {
        $process = Process::fromShellCommandline(
            sprintf(
                '%s %s generate --project=%s --output=%s --force',
                escapeshellarg(PHP_BINARY),
                escapeshellarg(__DIR__ . '/../../bin/context-pack.php'),
                escapeshellarg($projectPath),
                escapeshellarg($outputPath)
            ),
            __DIR__ . '/../../'
        );

        $process->mustRun();

        $metadata = json_decode(file_get_contents($outputPath . '/metadata.json') ?: 'null', true);
        expect($metadata['artifacts']['schemaDump']['status'])->toBe('failed');
        expect($metadata['artifacts']['schemaDump']['message'])->toContain('failed');

        $schemaLog = file_get_contents($outputPath . '/logs/schema-dump.log');
        expect($schemaLog)->toContain('sqlite3 binary not found');
    } finally {
        $filesystem->remove($workspace);
        if ($originalPath === false) {
            putenv('PATH');
        } else {
            putenv('PATH=' . $originalPath);
        }
    }
});
