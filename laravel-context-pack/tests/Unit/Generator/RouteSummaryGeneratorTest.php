<?php

declare(strict_types=1);

use LaravelContextPack\Generator\RouteSummaryGenerator;
use Symfony\Component\Filesystem\Filesystem;

beforeEach(function (): void {
    $this->filesystem = new Filesystem;
    $this->workspace = sys_get_temp_dir() . '/lcp-route-test-' . bin2hex(random_bytes(5));
    $this->projectPath = $this->workspace . '/project';
    $this->outputPath = $this->workspace . '/output';

    $this->filesystem->mkdir([
        $this->workspace,
        $this->projectPath . '/app/Http/Controllers',
        $this->outputPath . '/logs',
    ]);
});

afterEach(function (): void {
    $this->filesystem->remove($this->workspace);
});

test('it generates route summary from JSON output', function (): void {
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

    file_put_contents($this->projectPath . '/artisan', sprintf(
        <<<'PHP'
        #!/usr/bin/env php
        <?php
        if (in_array('--json', $argv, true)) {
            echo %s;
        }
        exit(0);
        PHP,
        var_export(json_encode($routes), true)
    ));

    file_put_contents(
        $this->projectPath . '/app/Http/Controllers/UserController.php',
        '<?php namespace App\Http\Controllers; class UserController {}'
    );

    $generator = new RouteSummaryGenerator;
    $result = $generator->generate($this->projectPath, $this->outputPath);

    expect($result['status'])->toBe('success');
    expect($result['message'])->toContain('JSON');
    expect(file_exists($this->outputPath . '/02-route-summary.md'))->toBeTrue();
    expect(file_exists($this->outputPath . '/02-route-summary.json'))->toBeTrue();

    $markdown = file_get_contents($this->outputPath . '/02-route-summary.md');
    expect($markdown)->toContain('# Route Summary');
    expect($markdown)->toContain('api/users');
    expect($markdown)->toContain('api.users.index');
    expect($markdown)->toContain('## Metrics');
});

test('it falls back to text parsing when JSON fails', function (): void {
    file_put_contents($this->projectPath . '/artisan', <<<'PHP'
    #!/usr/bin/env php
    <?php
    if (in_array('--json', $argv, true)) {
        echo 'invalid json';
        exit(0);
    }
    echo "GET|HEAD        api/users        api.users.index        App\\Http\\Controllers\\UserController@index        api,auth\n";
    exit(0);
    PHP);

    $generator = new RouteSummaryGenerator;
    $result = $generator->generate($this->projectPath, $this->outputPath);

    expect($result['status'])->toBe('success');
    expect($result['message'])->toContain('text');
});

test('it resolves controller paths correctly', function (): void {
    $routes = [
        [
            'method' => ['POST'],
            'uri' => 'posts',
            'name' => 'posts.store',
            'action' => 'App\\Http\\Controllers\\PostController@store',
            'middleware' => [],
            'prefix' => null,
        ],
    ];

    file_put_contents($this->projectPath . '/artisan', sprintf(
        <<<'PHP'
        #!/usr/bin/env php
        <?php
        echo %s;
        exit(0);
        PHP,
        var_export(json_encode($routes), true)
    ));

    file_put_contents(
        $this->projectPath . '/app/Http/Controllers/PostController.php',
        '<?php namespace App\Http\Controllers; class PostController {}'
    );

    $generator = new RouteSummaryGenerator;
    $result = $generator->generate($this->projectPath, $this->outputPath);

    $json = json_decode(file_get_contents($this->outputPath . '/02-route-summary.json'), true);
    expect($json[0]['controllerPath'])->toContain('app/Http/Controllers/PostController.php');
    expect($json[0]['controllerMethod'])->toBe('store');
});

test('it handles closure routes', function (): void {
    $routes = [
        [
            'method' => ['GET'],
            'uri' => 'welcome',
            'name' => null,
            'action' => 'Closure',
            'middleware' => [],
            'prefix' => null,
        ],
    ];

    file_put_contents($this->projectPath . '/artisan', sprintf(
        <<<'PHP'
        #!/usr/bin/env php
        <?php
        echo %s;
        exit(0);
        PHP,
        var_export(json_encode($routes), true)
    ));

    $generator = new RouteSummaryGenerator;
    $result = $generator->generate($this->projectPath, $this->outputPath);

    expect($result['status'])->toBe('success');

    $json = json_decode(file_get_contents($this->outputPath . '/02-route-summary.json'), true);
    expect($json[0]['controller'])->toBeNull();
    expect($json[0]['controllerPath'])->toBeNull();

    $markdown = file_get_contents($this->outputPath . '/02-route-summary.md');
    expect($markdown)->toContain('## Warnings');
    expect($markdown)->toContain('Closure');
});

test('it detects routes missing names', function (): void {
    $routes = [
        [
            'method' => ['POST'],
            'uri' => 'api/data',
            'name' => '',
            'action' => 'SomeController@method',
            'middleware' => [],
            'prefix' => 'api',
        ],
    ];

    file_put_contents($this->projectPath . '/artisan', sprintf(
        <<<'PHP'
        #!/usr/bin/env php
        <?php
        echo %s;
        exit(0);
        PHP,
        var_export(json_encode($routes), true)
    ));

    $generator = new RouteSummaryGenerator;
    $result = $generator->generate($this->projectPath, $this->outputPath);

    $markdown = file_get_contents($this->outputPath . '/02-route-summary.md');
    expect($markdown)->toContain('missing a name');
});

test('it groups routes by prefix', function (): void {
    $routes = [
        [
            'method' => ['GET'],
            'uri' => 'api/users',
            'name' => 'api.users',
            'action' => 'UserController@index',
            'middleware' => [],
            'prefix' => 'api',
        ],
        [
            'method' => ['GET'],
            'uri' => 'admin/dashboard',
            'name' => 'admin.dashboard',
            'action' => 'DashboardController@index',
            'middleware' => [],
            'prefix' => 'admin',
        ],
        [
            'method' => ['GET'],
            'uri' => 'home',
            'name' => 'home',
            'action' => 'HomeController@index',
            'middleware' => [],
            'prefix' => null,
        ],
    ];

    file_put_contents($this->projectPath . '/artisan', sprintf(
        <<<'PHP'
        #!/usr/bin/env php
        <?php
        echo %s;
        exit(0);
        PHP,
        var_export(json_encode($routes), true)
    ));

    $generator = new RouteSummaryGenerator;
    $result = $generator->generate($this->projectPath, $this->outputPath);

    $markdown = file_get_contents($this->outputPath . '/02-route-summary.md');
    expect($markdown)->toContain('### Prefix: api');
    expect($markdown)->toContain('### Prefix: admin');
    expect($markdown)->toContain('### Prefix: none');
});

test('it calculates route metrics', function (): void {
    $routes = [
        ['method' => ['GET'], 'uri' => 'a', 'name' => 'a', 'action' => 'A', 'middleware' => [], 'prefix' => null],
        ['method' => ['POST'], 'uri' => 'b', 'name' => 'b', 'action' => 'B', 'middleware' => [], 'prefix' => null],
        ['method' => ['GET', 'HEAD'], 'uri' => 'c', 'name' => 'c', 'action' => 'C', 'middleware' => [], 'prefix' => null],
    ];

    file_put_contents($this->projectPath . '/artisan', sprintf(
        <<<'PHP'
        #!/usr/bin/env php
        <?php
        echo %s;
        exit(0);
        PHP,
        var_export(json_encode($routes), true)
    ));

    $generator = new RouteSummaryGenerator;
    $result = $generator->generate($this->projectPath, $this->outputPath);

    $markdown = file_get_contents($this->outputPath . '/02-route-summary.md');
    expect($markdown)->toContain('Total routes: 3');
    expect($markdown)->toContain('GET routes:');
    expect($markdown)->toContain('POST routes: 1');
});

test('it handles artisan command failure', function (): void {
    file_put_contents($this->projectPath . '/artisan', <<<'PHP'
    #!/usr/bin/env php
    <?php
    exit(1);
    PHP);

    $generator = new RouteSummaryGenerator;
    $result = $generator->generate($this->projectPath, $this->outputPath);

    expect($result['status'])->toBe('failed');
    expect($result['message'])->toContain('Unable to parse');
});

test('it escapes pipe characters in markdown', function (): void {
    $routes = [
        [
            'method' => ['GET'],
            'uri' => 'test|pipe',
            'name' => 'test|name',
            'action' => 'Controller|Action',
            'middleware' => [],
            'prefix' => null,
        ],
    ];

    file_put_contents($this->projectPath . '/artisan', sprintf(
        <<<'PHP'
        #!/usr/bin/env php
        <?php
        echo %s;
        exit(0);
        PHP,
        var_export(json_encode($routes), true)
    ));

    $generator = new RouteSummaryGenerator;
    $result = $generator->generate($this->projectPath, $this->outputPath);

    $markdown = file_get_contents($this->outputPath . '/02-route-summary.md');
    expect($markdown)->toContain('test\\|pipe');
    expect($markdown)->toContain('test\\|name');
});
