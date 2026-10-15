<?php

declare(strict_types=1);

use LaravelContextPack\Generator\SchemaDumpGenerator;
use Symfony\Component\Filesystem\Filesystem;

beforeEach(function (): void {
    $this->filesystem = new Filesystem;
    $this->workspace = sys_get_temp_dir() . '/lcp-db-test-' . bin2hex(random_bytes(5));
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

test('it handles SQLite database configuration', function (): void {
    file_put_contents($this->projectPath . '/.env', <<<'ENV'
    DB_CONNECTION=sqlite
    DB_DATABASE=database/database.sqlite
    ENV);

    touch($this->projectPath . '/database.sqlite');

    file_put_contents($this->projectPath . '/artisan', <<<'PHP'
    #!/usr/bin/env php
    <?php
    foreach ($argv as $argument) {
        if (str_starts_with($argument, '--schema-path=')) {
            $schemaPath = trim(substr($argument, strlen('--schema-path=')), "'\"");
            file_put_contents($schemaPath, "CREATE TABLE sqlite_test (id INT);\n");
            exit(0);
        }
    }
    exit(1);
    PHP);

    $generator = new SchemaDumpGenerator;
    $result = $generator->generate(
        $this->projectPath,
        $this->outputPath,
        ['driver' => 'sqlite', 'database' => 'database.sqlite']
    );

    expect($result['status'])->toBe('success');
    expect(file_get_contents($this->outputPath . '/01-schema-dump.sql'))->toContain('sqlite_test');
});

test('it handles MySQL database configuration', function (): void {
    file_put_contents($this->projectPath . '/.env', <<<'ENV'
    DB_CONNECTION=mysql
    DB_HOST=127.0.0.1
    DB_PORT=3306
    DB_DATABASE=test_db
    DB_USERNAME=root
    DB_PASSWORD=secret
    ENV);

    file_put_contents($this->projectPath . '/artisan', <<<'PHP'
    #!/usr/bin/env php
    <?php
    foreach ($argv as $argument) {
        if (str_starts_with($argument, '--schema-path=')) {
            $schemaPath = trim(substr($argument, strlen('--schema-path=')), "'\"");
            file_put_contents($schemaPath, "CREATE TABLE mysql_test (id INT);\n");
            exit(0);
        }
    }
    exit(1);
    PHP);

    $generator = new SchemaDumpGenerator;
    $result = $generator->generate(
        $this->projectPath,
        $this->outputPath,
        ['driver' => 'mysql']
    );

    expect($result['status'])->toBe('success');
    expect(file_get_contents($this->outputPath . '/01-schema-dump.sql'))->toContain('mysql_test');
});

test('it handles PostgreSQL database configuration', function (): void {
    file_put_contents($this->projectPath . '/.env', <<<'ENV'
    DB_CONNECTION=pgsql
    DB_HOST=127.0.0.1
    DB_PORT=5432
    DB_DATABASE=test_db
    DB_USERNAME=postgres
    DB_SCHEMA=public
    ENV);

    file_put_contents($this->projectPath . '/artisan', <<<'PHP'
    #!/usr/bin/env php
    <?php
    foreach ($argv as $argument) {
        if (str_starts_with($argument, '--schema-path=')) {
            $schemaPath = trim(substr($argument, strlen('--schema-path=')), "'\"");
            file_put_contents($schemaPath, "CREATE TABLE postgres_test (id SERIAL);\n");
            exit(0);
        }
    }
    exit(1);
    PHP);

    $generator = new SchemaDumpGenerator;
    $result = $generator->generate(
        $this->projectPath,
        $this->outputPath,
        ['driver' => 'pgsql', 'schema' => 'public']
    );

    expect($result['status'])->toBe('success');
    expect(file_get_contents($this->outputPath . '/01-schema-dump.sql'))->toContain('postgres_test');
});

test('it gracefully skips when no database is configured', function (): void {
    file_put_contents($this->projectPath . '/artisan', <<<'PHP'
    #!/usr/bin/env php
    <?php
    fwrite(STDERR, 'No database configured');
    exit(1);
    PHP);

    $generator = new SchemaDumpGenerator;
    $result = $generator->generate(
        $this->projectPath,
        $this->outputPath,
        []
    );

    expect($result['status'])->toBe('failed');
    expect($result['message'])->toContain('failed');

    $log = file_get_contents($this->outputPath . '/logs/schema-dump.log');
    expect($log)->toContain('Exit code');
});
