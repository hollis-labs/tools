<?php

declare(strict_types=1);

use LaravelContextPack\Support\MetadataDetector;
use Symfony\Component\Filesystem\Filesystem;

beforeEach(function (): void {
    $this->filesystem = new Filesystem;
    $this->workspace = sys_get_temp_dir() . '/lcp-metadata-test-' . bin2hex(random_bytes(5));
    $this->projectPath = $this->workspace . '/project';

    $this->filesystem->mkdir([
        $this->workspace,
        $this->projectPath,
    ]);
});

afterEach(function (): void {
    $this->filesystem->remove($this->workspace);
});

test('it detects Laravel version from composer.lock', function (): void {
    file_put_contents($this->projectPath . '/composer.json', json_encode(['name' => 'test/app']));
    file_put_contents($this->projectPath . '/composer.lock', json_encode([
        'packages' => [
            ['name' => 'laravel/framework', 'version' => 'v11.9.0'],
        ],
    ]));

    $detector = new MetadataDetector;
    $metadata = $detector->detect($this->projectPath);

    expect($metadata['laravel']['version'])->toBe('v11.9.0');
});

test('it detects Lumen framework', function (): void {
    file_put_contents($this->projectPath . '/composer.json', json_encode(['name' => 'test/app']));
    file_put_contents($this->projectPath . '/composer.lock', json_encode([
        'packages' => [
            ['name' => 'laravel/lumen-framework', 'version' => 'v10.0.0'],
        ],
    ]));

    $detector = new MetadataDetector;
    $metadata = $detector->detect($this->projectPath);

    expect($metadata['laravel']['version'])->toBe('v10.0.0');
});

test('it detects Laravel version from artisan', function (): void {
    file_put_contents($this->projectPath . '/composer.json', json_encode(['name' => 'test/app']));
    file_put_contents($this->projectPath . '/composer.lock', json_encode(['packages' => []]));
    file_put_contents($this->projectPath . '/artisan', <<<'PHP'
    #!/usr/bin/env php
    <?php
    echo 'Laravel Framework 11.9.0';
    exit(0);
    PHP);

    $detector = new MetadataDetector;
    $metadata = $detector->detect($this->projectPath);

    expect($metadata['laravel']['artisanVersion'])->toBe('Laravel Framework 11.9.0');
});

test('it detects PHP version', function (): void {
    file_put_contents($this->projectPath . '/composer.json', json_encode(['name' => 'test/app']));

    $detector = new MetadataDetector;
    $metadata = $detector->detect($this->projectPath);

    expect($metadata['php']['version'])->toBe(PHP_VERSION);
});

test('it detects database configuration from .env', function (): void {
    file_put_contents($this->projectPath . '/composer.json', json_encode(['name' => 'test/app']));
    file_put_contents($this->projectPath . '/.env', <<<'ENV'
    DB_CONNECTION=mysql
    DB_HOST=127.0.0.1
    DB_PORT=3306
    DB_DATABASE=mydb
    DB_USERNAME=root
    DB_PASSWORD=secret
    ENV);

    $detector = new MetadataDetector;
    $metadata = $detector->detect($this->projectPath);

    expect($metadata['database']['driver'])->toBe('mysql');
    expect($metadata['database']['host'])->toBe('127.0.0.1');
    expect($metadata['database']['port'])->toBe('3306');
    expect($metadata['database']['database'])->toBe('mydb');
    expect($metadata['database']['username'])->toBe('root');
    expect($metadata['database']['hasPassword'])->toBeTrue();
});

test('it detects PostgreSQL schema', function (): void {
    file_put_contents($this->projectPath . '/composer.json', json_encode(['name' => 'test/app']));
    file_put_contents($this->projectPath . '/.env', <<<'ENV'
    DB_CONNECTION=pgsql
    DB_SCHEMA=public
    ENV);

    $detector = new MetadataDetector;
    $metadata = $detector->detect($this->projectPath);

    expect($metadata['database']['driver'])->toBe('pgsql');
    expect($metadata['database']['schema'])->toBe('public');
});

test('it detects available tools from composer.lock', function (): void {
    file_put_contents($this->projectPath . '/composer.json', json_encode(['name' => 'test/app']));
    file_put_contents($this->projectPath . '/composer.lock', json_encode([
        'packages' => [],
        'packages-dev' => [
            ['name' => 'laravel/pint', 'version' => 'v1.15.0'],
            ['name' => 'phpstan/phpstan', 'version' => '1.11.0'],
            ['name' => 'pestphp/pest', 'version' => 'v2.33.0'],
            ['name' => 'rector/rector', 'version' => 'v1.1.0'],
        ],
    ]));

    $detector = new MetadataDetector;
    $metadata = $detector->detect($this->projectPath);

    expect($metadata['detectedTools']['pint']['available'])->toBeTrue();
    expect($metadata['detectedTools']['phpstan']['available'])->toBeTrue();
    expect($metadata['detectedTools']['pest']['available'])->toBeTrue();
    expect($metadata['detectedTools']['rector']['available'])->toBeTrue();
});

test('it detects Larastan', function (): void {
    file_put_contents($this->projectPath . '/composer.json', json_encode(['name' => 'test/app']));
    file_put_contents($this->projectPath . '/composer.lock', json_encode([
        'packages-dev' => [
            ['name' => 'nunomaduro/larastan', 'version' => 'v2.0.0'],
        ],
    ]));

    $detector = new MetadataDetector;
    $metadata = $detector->detect($this->projectPath);

    expect($metadata['detectedTools']['larastan']['available'])->toBeTrue();
    expect($metadata['detectedTools']['phpstan']['available'])->toBeTrue();
});

test('it detects Pest from Pest.php file', function (): void {
    file_put_contents($this->projectPath . '/composer.json', json_encode(['name' => 'test/app']));
    file_put_contents($this->projectPath . '/composer.lock', json_encode(['packages-dev' => []]));
    file_put_contents($this->projectPath . '/tests/Pest.php', '<?php');

    $detector = new MetadataDetector;
    $metadata = $detector->detect($this->projectPath);

    expect($metadata['detectedTools']['pest']['available'])->toBeTrue();
});

test('it collects all packages with versions', function (): void {
    file_put_contents($this->projectPath . '/composer.json', json_encode(['name' => 'test/app']));
    file_put_contents($this->projectPath . '/composer.lock', json_encode([
        'packages' => [
            ['name' => 'laravel/framework', 'version' => 'v11.0.0'],
            ['name' => 'symfony/console', 'version' => 'v7.1.0'],
        ],
        'packages-dev' => [
            ['name' => 'phpunit/phpunit', 'version' => '10.0.0'],
        ],
    ]));

    $detector = new MetadataDetector;
    $metadata = $detector->detect($this->projectPath);

    expect($metadata['packages'])->toHaveCount(3);
    expect($metadata['packages']['laravel/framework'])->toBe('v11.0.0');
    expect($metadata['packages']['symfony/console'])->toBe('v7.1.0');
    expect($metadata['packages']['phpunit/phpunit'])->toBe('10.0.0');
});

test('it uses project name from composer.json', function (): void {
    file_put_contents($this->projectPath . '/composer.json', json_encode([
        'name' => 'acme/my-app',
    ]));

    $detector = new MetadataDetector;
    $metadata = $detector->detect($this->projectPath);

    expect($metadata['project']['name'])->toBe('acme/my-app');
});

test('it falls back to directory name when composer.json has no name', function (): void {
    file_put_contents($this->projectPath . '/composer.json', json_encode([]));

    $detector = new MetadataDetector;
    $metadata = $detector->detect($this->projectPath);

    expect($metadata['project']['name'])->toBe('project');
});

test('it includes project path', function (): void {
    file_put_contents($this->projectPath . '/composer.json', json_encode(['name' => 'test/app']));

    $detector = new MetadataDetector;
    $metadata = $detector->detect($this->projectPath);

    expect($metadata['project']['path'])->toBe($this->projectPath);
});

test('it includes generation timestamp', function (): void {
    file_put_contents($this->projectPath . '/composer.json', json_encode(['name' => 'test/app']));

    $detector = new MetadataDetector;
    $metadata = $detector->detect($this->projectPath);

    expect($metadata['generatedAt'])->toBeString();
    expect($metadata['generatedAt'])->toMatch('/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}/');
});

test('it handles missing composer.json gracefully', function (): void {
    $detector = new MetadataDetector;
    expect(fn () => $detector->detect($this->projectPath))->toThrow(RuntimeException::class);
});

test('it parses .env with comments and empty lines', function (): void {
    file_put_contents($this->projectPath . '/composer.json', json_encode(['name' => 'test/app']));
    file_put_contents($this->projectPath . '/.env', <<<'ENV'
    # Database configuration
    DB_CONNECTION=sqlite

    # Empty line above
    DB_DATABASE=database.sqlite
    # Another comment
    ENV);

    $detector = new MetadataDetector;
    $metadata = $detector->detect($this->projectPath);

    expect($metadata['database']['driver'])->toBe('sqlite');
    expect($metadata['database']['database'])->toBe('database.sqlite');
});

test('it handles .env values with quotes', function (): void {
    file_put_contents($this->projectPath . '/composer.json', json_encode(['name' => 'test/app']));
    file_put_contents($this->projectPath . '/.env', <<<'ENV'
    DB_DATABASE="my database"
    DB_PASSWORD='secret123'
    ENV);

    $detector = new MetadataDetector;
    $metadata = $detector->detect($this->projectPath);

    expect($metadata['database']['database'])->toBe('my database');
    expect($metadata['database']['hasPassword'])->toBeTrue();
});

test('it handles missing .env file', function (): void {
    file_put_contents($this->projectPath . '/composer.json', json_encode(['name' => 'test/app']));

    $detector = new MetadataDetector;
    $metadata = $detector->detect($this->projectPath);

    expect($metadata['database']['driver'])->toBeNull();
});

test('it resolves driver fallback from packages', function (): void {
    file_put_contents($this->projectPath . '/composer.json', json_encode(['name' => 'test/app']));
    file_put_contents($this->projectPath . '/composer.lock', json_encode([
        'packages' => [
            ['name' => 'laravel/framework', 'version' => 'v11.0.0'],
        ],
    ]));

    $detector = new MetadataDetector;
    $metadata = $detector->detect($this->projectPath);

    // Should fallback to mysql when no .env
    expect($metadata['database']['driver'])->toBe('mysql');
});
