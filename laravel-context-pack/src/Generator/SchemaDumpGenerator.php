<?php

declare(strict_types=1);

namespace LaravelContextPack\Generator;

use Symfony\Component\Filesystem\Filesystem;
use Symfony\Component\Process\ExecutableFinder;
use Symfony\Component\Process\Process;

final class SchemaDumpGenerator extends AbstractGenerator
{
    private const ARTISAN_COMMAND = 'php artisan schema:dump --no-interaction --prune --schema-path=%s';

    /**
     * @var array<string, mixed>
     */
    private array $databaseConfig = [];

    public function __construct(?Filesystem $filesystem = null)
    {
        parent::__construct($filesystem);
    }

    public function getName(): string
    {
        return 'schema-dump';
    }

    public function getDescription(): string
    {
        return 'Generate database schema dump using Laravel schema:dump or fallback methods';
    }

    /**
     * @return array<string, array<string, mixed>>
     */
    public function getConfigSchema(): array
    {
        return [
            'database' => [
                'type' => 'array',
                'description' => 'Database configuration for fallback methods',
                'default' => [],
            ],
        ];
    }

    /**
     * Set database configuration for fallback methods.
     *
     * @param array<string, mixed> $database
     */
    public function setDatabaseConfig(array $database): void
    {
        $this->databaseConfig = $database;
    }

    /**
     * {@inheritdoc}
     *
     * @param array<string, mixed> $database Optional database config for backward compatibility
     */
    public function generate(string $projectPath, string $outputPath, array $database = []): array
    {
        // Merge provided database config with instance config
        $databaseConfig = array_merge($this->databaseConfig, $database);

        $logPath = $outputPath . '/logs/schema-dump.log';

        $tmpSchemaPath = $this->createTempSchemaPath($projectPath);
        $command = sprintf(self::ARTISAN_COMMAND, escapeshellarg($tmpSchemaPath));

        $process = Process::fromShellCommandline($command, $projectPath);
        $process->setTimeout(120);

        try {
            $this->appendLog($logPath, sprintf('[%s] Running: %s', $this->now(), $command));
            $process->run();
        } catch (\Throwable $throwable) {
            $this->appendLog($logPath, sprintf('[%s] Exception: %s', $this->now(), $throwable->getMessage()));

            return [
                'status' => 'failed',
                'message' => 'Schema dump command threw an exception. See log for details.',
            ];
        }

        if (! $process->isSuccessful()) {
            $this->appendLog($logPath, sprintf('[%s] Exit code %d', $this->now(), $process->getExitCode() ?? -1));
            $this->appendLog($logPath, $process->getOutput());
            $this->appendLog($logPath, $process->getErrorOutput());

            $fallback = $this->attemptFallback($databaseConfig, $projectPath, $outputPath, $logPath);
            if ($fallback !== null) {
                return $fallback;
            }

            return [
                'status' => 'failed',
                'message' => 'Schema dump command failed and no fallback was executed. Review logs/schema-dump.log.',
            ];
        }

        $this->appendLog($logPath, $process->getOutput());

        if (! $this->filesystem->exists($tmpSchemaPath)) {
            $this->appendLog($logPath, sprintf('[%s] Expected schema file not created: %s', $this->now(), $tmpSchemaPath));

            return [
                'status' => 'failed',
                'message' => 'Schema command succeeded but no schema file was produced.',
            ];
        }

        $destination = $outputPath . '/01-schema-dump.sql';
        $this->filesystem->copy($tmpSchemaPath, $destination, true);
        $this->appendLog($logPath, sprintf('[%s] Schema dump copied to %s', $this->now(), $destination));

        $this->cleanupTempSchema($tmpSchemaPath);

        return [
            'status' => 'success',
            'message' => 'Schema dump generated successfully.',
        ];
    }

    /**
     * @param  array<string, mixed>  $database
     * @return array{status: string, message: string}|null
     */
    private function attemptFallback(array $database, string $projectPath, string $outputPath, string $logPath): ?array
    {
        $driver = strtolower((string) ($database['driver'] ?? ''));

        return match ($driver) {
            'sqlite' => $this->dumpSqlite($database, $projectPath, $outputPath, $logPath),
            'mysql' => $this->dumpMysql($projectPath, $outputPath, $logPath),
            'pgsql', 'postgresql' => $this->dumpPostgres($projectPath, $outputPath, $logPath, $database),
            default => null,
        };
    }

    /**
     * @param  array<string, mixed>  $database
     * @return array{status: string, message: string}|null
     */
    private function dumpSqlite(array $database, string $projectPath, string $outputPath, string $logPath): ?array
    {
        $databasePath = $database['database'] ?? $projectPath . '/database/database.sqlite';
        if ($databasePath === null || $databasePath === '') {
            return null;
        }

        if (! str_starts_with((string) $databasePath, DIRECTORY_SEPARATOR)) {
            $databasePath = $projectPath . '/' . ltrim((string) $databasePath, '/');
        }

        if (! is_file($databasePath)) {
            $this->appendLog($logPath, sprintf('[%s] SQLite database not found at %s', $this->now(), $databasePath));

            return null;
        }

        if (! $this->isBinaryAvailable('sqlite3')) {
            $this->appendLog($logPath, sprintf('[%s] sqlite3 binary not found in PATH.', $this->now()));

            return null;
        }

        $command = sprintf('sqlite3 %s ".schema"', escapeshellarg($databasePath));
        $process = Process::fromShellCommandline($command);
        $process->setTimeout(20);

        $this->appendLog($logPath, sprintf('[%s] Fallback: %s', $this->now(), $command));
        $process->run();

        if (! $process->isSuccessful()) {
            $this->appendLog($logPath, sprintf('[%s] SQLite fallback exit code %d', $this->now(), $process->getExitCode() ?? -1));
            $this->appendLog($logPath, trim($process->getErrorOutput()));

            return null;
        }

        $schema = trim($process->getOutput());
        if ($schema === '') {
            $this->appendLog($logPath, sprintf('[%s] SQLite fallback produced empty output.', $this->now()));

            return null;
        }

        $destination = $outputPath . '/01-schema-dump.sql';
        $this->filesystem->dumpFile($destination, $schema . PHP_EOL);
        $this->appendLog($logPath, sprintf('[%s] Schema dump generated via sqlite3 fallback.', $this->now()));

        return [
            'status' => 'success',
            'message' => 'Schema dump generated via sqlite3 fallback.',
        ];
    }

    /**
     * @return array{status: string, message: string}|null
     */
    private function dumpMysql(string $projectPath, string $outputPath, string $logPath): ?array
    {
        $credentials = $this->readDatabaseCredentials($projectPath);
        $required = ['DB_DATABASE', 'DB_USERNAME'];
        foreach ($required as $key) {
            if (! isset($credentials[$key]) || $credentials[$key] === '') {
                $this->appendLog($logPath, sprintf('[%s] mysqldump fallback skipped (missing %s in .env).', $this->now(), $key));

                return null;
            }
        }

        $host = $credentials['DB_HOST'] ?? '127.0.0.1';
        $port = $credentials['DB_PORT'] ?? '3306';
        $databaseName = $credentials['DB_DATABASE'];
        $username = $credentials['DB_USERNAME'];

        if (! $this->isBinaryAvailable('mysqldump')) {
            $this->appendLog($logPath, sprintf('[%s] mysqldump binary not found in PATH.', $this->now()));

            return null;
        }

        $command = sprintf(
            'mysqldump --no-data --host=%s --port=%s --user=%s %s',
            escapeshellarg($host),
            escapeshellarg($port),
            escapeshellarg($username),
            escapeshellarg($databaseName)
        );

        $env = [];
        if (($credentials['DB_PASSWORD'] ?? '') !== '') {
            $env['MYSQL_PWD'] = (string) $credentials['DB_PASSWORD'];
        }

        if (($credentials['DB_SOCKET'] ?? '') !== '') {
            $command .= sprintf(' --socket=%s', escapeshellarg((string) $credentials['DB_SOCKET']));
        }

        $process = Process::fromShellCommandline($command, null, $env ?: null);
        $process->setTimeout(20);

        $this->appendLog($logPath, sprintf('[%s] Fallback: %s', $this->now(), $command));
        $process->run();

        if (! $process->isSuccessful()) {
            $this->appendLog($logPath, sprintf('[%s] mysqldump exit code %d', $this->now(), $process->getExitCode() ?? -1));
            $this->appendLog($logPath, trim($process->getErrorOutput()));

            return null;
        }

        $schema = trim($process->getOutput());
        if ($schema === '') {
            return null;
        }

        $destination = $outputPath . '/01-schema-dump.sql';
        $this->filesystem->dumpFile($destination, $schema . PHP_EOL);
        $this->appendLog($logPath, sprintf('[%s] Schema dump generated via mysqldump fallback.', $this->now()));

        return [
            'status' => 'success',
            'message' => 'Schema dump generated via mysqldump fallback.',
        ];
    }

    /**
     * @param  array<string, mixed>  $database
     * @return array{status: string, message: string}|null
     */
    private function dumpPostgres(string $projectPath, string $outputPath, string $logPath, array $database): ?array
    {
        $credentials = $this->readDatabaseCredentials($projectPath);
        $databaseName = $credentials['DB_DATABASE'] ?? null;
        $username = $credentials['DB_USERNAME'] ?? null;

        if ($databaseName === null || $username === null) {
            $this->appendLog($logPath, sprintf('[%s] pg_dump fallback skipped (missing DB_DATABASE or DB_USERNAME).', $this->now()));

            return null;
        }

        $host = $credentials['DB_HOST'] ?? '127.0.0.1';
        $port = $credentials['DB_PORT'] ?? '5432';
        $schema = $database['schema'] ?? 'public';

        if (! $this->isBinaryAvailable('pg_dump')) {
            $this->appendLog($logPath, sprintf('[%s] pg_dump binary not found in PATH.', $this->now()));

            return null;
        }

        $command = sprintf(
            'pg_dump --schema-only --no-privileges --no-owner --host=%s --port=%s --username=%s --schema=%s %s',
            escapeshellarg($host),
            escapeshellarg($port),
            escapeshellarg($username),
            escapeshellarg((string) $schema),
            escapeshellarg($databaseName)
        );

        $env = [];
        if (($credentials['DB_PASSWORD'] ?? '') !== '') {
            $env['PGPASSWORD'] = (string) $credentials['DB_PASSWORD'];
        }

        $process = Process::fromShellCommandline($command, null, $env ?: null);
        $process->setTimeout(20);

        $this->appendLog($logPath, sprintf('[%s] Fallback: %s', $this->now(), $command));
        $process->run();

        if (! $process->isSuccessful()) {
            $this->appendLog($logPath, sprintf('[%s] pg_dump exit code %d', $this->now(), $process->getExitCode() ?? -1));
            $this->appendLog($logPath, trim($process->getErrorOutput()));

            return null;
        }

        $schemaDump = trim($process->getOutput());
        if ($schemaDump === '') {
            return null;
        }

        $destination = $outputPath . '/01-schema-dump.sql';
        $this->filesystem->dumpFile($destination, $schemaDump . PHP_EOL);
        $this->appendLog($logPath, sprintf('[%s] Schema dump generated via pg_dump fallback.', $this->now()));

        return [
            'status' => 'success',
            'message' => 'Schema dump generated via pg_dump fallback.',
        ];
    }

    /**
     * @return array<string, string>
     */
    private function readDatabaseCredentials(string $projectPath): array
    {
        $path = $projectPath . '/.env';
        if (! is_file($path)) {
            return [];
        }

        $contents = (string) file_get_contents($path);
        $lines = preg_split('/\r?\n/', $contents) ?: [];
        $values = [];

        foreach ($lines as $line) {
            $line = trim($line);
            if ($line === '' || str_starts_with($line, '#')) {
                continue;
            }

            if (strpos($line, '=') === false) {
                continue;
            }

            [$key, $value] = explode('=', $line, 2);
            $key = trim($key);
            $value = trim($value);
            if ($key === '') {
                continue;
            }

            $values[$key] = trim($value, "'\"");
        }

        return $values;
    }

    private function isBinaryAvailable(string $binary): bool
    {
        $finder = new ExecutableFinder;

        return $finder->find($binary) !== null;
    }

    private function createTempSchemaPath(string $projectPath): string
    {
        $directory = $projectPath . '/storage/framework/cache';
        if (! $this->filesystem->exists($directory)) {
            $this->filesystem->mkdir($directory);
        }

        $path = $directory . '/laravel-context-pack-schema.sql';
        $this->filesystem->remove($path);

        return $path;
    }

    private function cleanupTempSchema(string $path): void
    {
        if ($this->filesystem->exists($path)) {
            $this->filesystem->remove($path);
        }
    }
}
