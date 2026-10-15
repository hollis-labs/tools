<?php

declare(strict_types=1);

use LaravelContextPack\Generator\SchemaDumpGenerator;
use Symfony\Component\Filesystem\Filesystem;

beforeEach(function (): void {
    $this->filesystem = new Filesystem;
    $this->workspace = sys_get_temp_dir() . '/lcp-schema-test-' . bin2hex(random_bytes(5));
    $this->projectPath = $this->workspace . '/project';
    $this->outputPath = $this->workspace . '/output';

    $this->filesystem->mkdir([
        $this->workspace,
        $this->projectPath . '/storage/framework/cache',
        $this->outputPath . '/logs',
    ]);
});

afterEach(function (): void {
    $this->filesystem->remove($this->workspace);
});

test('it generates schema dump successfully via artisan', function (): void {
    file_put_contents($this->projectPath . '/artisan', <<<'PHP'
    #!/usr/bin/env php
    <?php
    $schemaPath = null;
    foreach ($argv as $argument) {
        if (str_starts_with($argument, '--schema-path=')) {
            $schemaPath = trim(substr($argument, strlen('--schema-path=')), "'\"");
        }
    }
    file_put_contents($schemaPath, "CREATE TABLE users (id INT);\n");
    echo 'Schema dump created';
    exit(0);
    PHP);

    $generator = new SchemaDumpGenerator;
    $result = $generator->generate($this->projectPath, $this->outputPath);

    expect($result['status'])->toBe('success');
    expect($result['message'])->toContain('successfully');
    expect(file_exists($this->outputPath . '/01-schema-dump.sql'))->toBeTrue();
    expect(file_get_contents($this->outputPath . '/01-schema-dump.sql'))->toContain('CREATE TABLE users');
});

test('it falls back to sqlite3 when artisan fails', function (): void {
    $dbPath = $this->projectPath . '/database.sqlite';
    touch($dbPath);

    file_put_contents($this->projectPath . '/.env', "DB_CONNECTION=sqlite\nDB_DATABASE=database.sqlite\n");
    file_put_contents($this->projectPath . '/artisan', <<<'PHP'
    #!/usr/bin/env php
    <?php
    fwrite(STDERR, 'Schema dump failed');
    exit(1);
    PHP);

    // Create a mock sqlite3 binary in PATH
    $binDir = $this->workspace . '/bin';
    mkdir($binDir);
    file_put_contents($binDir . '/sqlite3', <<<'BASH'
    #!/usr/bin/env bash
    echo "CREATE TABLE test (id INT);"
    exit 0
    BASH);

    $originalPath = getenv('PATH');
    putenv('PATH=' . $binDir . ':' . $originalPath);

    try {
        $generator = new SchemaDumpGenerator;
        $result = $generator->generate(
            $this->projectPath,
            $this->outputPath,
            ['driver' => 'sqlite', 'database' => 'database.sqlite']
        );

        expect($result['status'])->toBe('success');
        expect($result['message'])->toContain('sqlite3 fallback');
        expect(file_exists($this->outputPath . '/01-schema-dump.sql'))->toBeTrue();
    } finally {
        putenv('PATH=' . $originalPath);
    }
})->skip('Requires dynamic PATH modification');

test('it handles missing schema file gracefully', function (): void {
    file_put_contents($this->projectPath . '/artisan', <<<'PHP'
    #!/usr/bin/env php
    <?php
    echo 'Success but no file';
    exit(0);
    PHP);

    $generator = new SchemaDumpGenerator;
    $result = $generator->generate($this->projectPath, $this->outputPath);

    expect($result['status'])->toBe('failed');
    expect($result['message'])->toContain('no schema file');
});

test('it logs errors when artisan fails', function (): void {
    file_put_contents($this->projectPath . '/artisan', <<<'PHP'
    #!/usr/bin/env php
    <?php
    fwrite(STDERR, 'Database connection error');
    exit(1);
    PHP);

    $generator = new SchemaDumpGenerator;
    $result = $generator->generate($this->projectPath, $this->outputPath);

    expect($result['status'])->toBe('failed');
    expect(file_exists($this->outputPath . '/logs/schema-dump.log'))->toBeTrue();
    $log = file_get_contents($this->outputPath . '/logs/schema-dump.log');
    expect($log)->toContain('Exit code');
});

test('it handles artisan command exceptions', function (): void {
    // Non-existent artisan
    $generator = new SchemaDumpGenerator;
    $result = $generator->generate('/nonexistent', $this->outputPath);

    expect($result['status'])->toBe('failed');
    expect($result['message'])->toContain('exception');
});

test('it creates temp schema path in storage/framework/cache', function (): void {
    file_put_contents($this->projectPath . '/artisan', <<<'PHP'
    #!/usr/bin/env php
    <?php
    $schemaPath = null;
    foreach ($argv as $argument) {
        if (str_starts_with($argument, '--schema-path=')) {
            $schemaPath = trim(substr($argument, strlen('--schema-path=')), "'\"");
        }
    }
    // Verify the path includes storage/framework/cache
    if (strpos($schemaPath, 'storage/framework/cache') === false) {
        fwrite(STDERR, 'Invalid schema path: ' . $schemaPath);
        exit(1);
    }
    file_put_contents($schemaPath, "CREATE TABLE test (id INT);\n");
    exit(0);
    PHP);

    $generator = new SchemaDumpGenerator;
    $result = $generator->generate($this->projectPath, $this->outputPath);

    expect($result['status'])->toBe('success');
    // Temp file should be cleaned up
    expect(file_exists($this->projectPath . '/storage/framework/cache/laravel-context-pack-schema.sql'))->toBeFalse();
});

test('it parses .env file for database credentials', function (): void {
    file_put_contents($this->projectPath . '/.env', <<<'ENV'
    DB_CONNECTION=mysql
    DB_HOST=127.0.0.1
    DB_PORT=3306
    DB_DATABASE=test_db
    DB_USERNAME=test_user
    DB_PASSWORD="secret123"
    # Comment line
    ENV);

    file_put_contents($this->projectPath . '/artisan', <<<'PHP'
    #!/usr/bin/env php
    <?php
    exit(1);
    PHP);

    $generator = new SchemaDumpGenerator;
    // This will fail but should attempt mysqldump fallback
    $result = $generator->generate(
        $this->projectPath,
        $this->outputPath,
        ['driver' => 'mysql']
    );

    // Check log mentions mysqldump
    $log = file_get_contents($this->outputPath . '/logs/schema-dump.log');
    expect($log)->toContain('mysqldump');
});
