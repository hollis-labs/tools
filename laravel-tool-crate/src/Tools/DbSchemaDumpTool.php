<?php

namespace HollisLabs\ToolCrate\Tools;

use Illuminate\JsonSchema\JsonSchema;
use Laravel\Mcp\Request;
use Laravel\Mcp\Response;
use Laravel\Mcp\Server\Tool;
use HollisLabs\ToolCrate\Support\Exec;
use HollisLabs\ToolCrate\Tools\Contracts\SummarizesTool;

class DbSchemaDumpTool extends Tool implements SummarizesTool
{
    protected string $name = 'db_schema_dump';
    protected string $title = 'Dump database schema';
    protected string $description = 'Generate SQL schema dump (structure only, no data).';

    public function schema(JsonSchema $s): array
    {
        return [
            'connection' => $s->string()
                ->default('default')
                ->description('Database connection name'),
            'output_path' => $s->string()
                ->description('Output file path (optional, returns content if omitted)'),
            'method' => $s->string()
                ->enum(['auto', 'artisan', 'native'])
                ->default('auto')
                ->description('Dump method: auto (try artisan, fallback native), artisan, or native'),
            'cwd' => $s->string()
                ->description('Laravel project directory (for artisan method)'),
        ];
    }

    public function handle(Request $r): Response
    {
        $connection = (string) $r->get('connection', 'default');
        $outputPath = $r->get('output_path');
        $method = (string) $r->get('method', 'auto');
        $cwd = $r->get('cwd') ?: getcwd();

        // Check if we're in a Laravel context for config
        if (!function_exists('config')) {
            return Response::error('This tool requires a Laravel application context.');
        }

        // Handle 'default' connection name
        if ($connection === 'default') {
            $connection = config('database.default');
        }

        $config = config("database.connections.{$connection}");
        if (!$config) {
            return Response::error("Database connection '{$connection}' not found in config.");
        }

        $driver = $config['driver'] ?? null;
        if (!$driver) {
            return Response::error("No driver specified for connection '{$connection}'.");
        }

        // Try methods based on preference
        $result = match ($method) {
            'artisan' => $this->dumpViaArtisan($cwd),
            'native' => $this->dumpNative($driver, $config, $cwd),
            'auto' => $this->dumpAuto($driver, $config, $cwd),
            default => ['success' => false, 'error' => 'Invalid method'],
        };

        if (!$result['success']) {
            return Response::error($result['error'] ?? 'Schema dump failed');
        }

        // Handle output
        if ($outputPath) {
            try {
                file_put_contents($outputPath, $result['schema']);
                return Response::json([
                    'success' => true,
                    'method' => $result['method'],
                    'output_path' => $outputPath,
                    'size' => strlen($result['schema']),
                ]);
            } catch (\Exception $e) {
                return Response::error('Failed to write output: ' . $e->getMessage());
            }
        }

        return Response::json([
            'success' => true,
            'method' => $result['method'],
            'schema' => $result['schema'],
            'size' => strlen($result['schema']),
        ]);
    }

    private function dumpAuto(string $driver, array $config, string $cwd): array
    {
        // Try artisan first
        $artisan = $this->dumpViaArtisan($cwd);
        if ($artisan['success']) {
            return $artisan;
        }

        // Fallback to native
        return $this->dumpNative($driver, $config, $cwd);
    }

    private function dumpViaArtisan(string $cwd): array
    {
        // Create temp path for schema
        $tempPath = sys_get_temp_dir() . '/laravel-tool-crate-schema-' . uniqid() . '.sql';

        $cmd = [
            'php',
            'artisan',
            'schema:dump',
            '--no-interaction',
            '--prune',
            '--schema-path=' . $tempPath,
        ];

        $result = Exec::run($cmd, null, 120.0, $cwd);

        if (!$result->ok) {
            return [
                'success' => false,
                'error' => 'artisan schema:dump failed: ' . trim($result->stderr ?: $result->stdout),
            ];
        }

        if (!file_exists($tempPath)) {
            return [
                'success' => false,
                'error' => 'artisan schema:dump succeeded but no file was created',
            ];
        }

        $schema = file_get_contents($tempPath);
        @unlink($tempPath);

        return [
            'success' => true,
            'method' => 'artisan',
            'schema' => $schema,
        ];
    }

    private function dumpNative(string $driver, array $config, string $cwd): array
    {
        return match ($driver) {
            'sqlite' => $this->dumpSqlite($config, $cwd),
            'mysql' => $this->dumpMysql($config, $cwd),
            'pgsql' => $this->dumpPostgres($config, $cwd),
            default => [
                'success' => false,
                'error' => "Unsupported driver: {$driver}",
            ],
        };
    }

    private function dumpSqlite(array $config, string $cwd): array
    {
        $database = $config['database'] ?? null;
        if (!$database) {
            return ['success' => false, 'error' => 'No database path configured'];
        }

        // Resolve relative paths
        if (!str_starts_with($database, '/')) {
            $database = $cwd . '/' . ltrim($database, '/');
        }

        if (!file_exists($database)) {
            return ['success' => false, 'error' => "SQLite database not found: {$database}"];
        }

        $result = Exec::run(['sqlite3', $database, '.schema'], null, 30.0);

        if (!$result->ok) {
            return [
                'success' => false,
                'error' => 'sqlite3 .schema failed: ' . trim($result->stderr ?: $result->stdout),
            ];
        }

        return [
            'success' => true,
            'method' => 'sqlite3',
            'schema' => $result->stdout,
        ];
    }

    private function dumpMysql(array $config, string $cwd): array
    {
        $host = $config['host'] ?? '127.0.0.1';
        $port = $config['port'] ?? '3306';
        $database = $config['database'] ?? null;
        $username = $config['username'] ?? null;

        if (!$database || !$username) {
            return ['success' => false, 'error' => 'Missing database or username in config'];
        }

        $cmd = [
            'mysqldump',
            '--no-data',
            '--host=' . $host,
            '--port=' . $port,
            '--user=' . $username,
            $database,
        ];

        $env = [];
        if (!empty($config['password'])) {
            $env['MYSQL_PWD'] = $config['password'];
        }

        if (!empty($config['unix_socket'])) {
            $cmd[] = '--socket=' . $config['unix_socket'];
        }

        $result = Exec::run($cmd, null, 60.0, $cwd);

        if (!$result->ok) {
            return [
                'success' => false,
                'error' => 'mysqldump failed: ' . trim($result->stderr ?: $result->stdout),
            ];
        }

        return [
            'success' => true,
            'method' => 'mysqldump',
            'schema' => $result->stdout,
        ];
    }

    private function dumpPostgres(array $config, string $cwd): array
    {
        $host = $config['host'] ?? '127.0.0.1';
        $port = $config['port'] ?? '5432';
        $database = $config['database'] ?? null;
        $username = $config['username'] ?? null;
        $schema = $config['schema'] ?? 'public';

        if (!$database || !$username) {
            return ['success' => false, 'error' => 'Missing database or username in config'];
        }

        $cmd = [
            'pg_dump',
            '--schema-only',
            '--no-privileges',
            '--no-owner',
            '--host=' . $host,
            '--port=' . $port,
            '--username=' . $username,
            '--schema=' . $schema,
            $database,
        ];

        $env = [];
        if (!empty($config['password'])) {
            $env['PGPASSWORD'] = $config['password'];
        }

        $result = Exec::run($cmd, null, 60.0, $cwd);

        if (!$result->ok) {
            return [
                'success' => false,
                'error' => 'pg_dump failed: ' . trim($result->stderr ?: $result->stdout),
            ];
        }

        return [
            'success' => true,
            'method' => 'pg_dump',
            'schema' => $result->stdout,
        ];
    }

    public static function summaryName(): string { return 'db_schema_dump'; }
    public static function summaryTitle(): string { return 'Dump database schema'; }
    public static function summaryDescription(): string { return 'Generate SQL schema (no data).'; }
    public static function schemaSummary(): array
    {
        return [
            'connection' => 'connection name (default)',
            'output_path' => 'file path (optional)',
            'method' => 'auto|artisan|native',
            'cwd' => 'Laravel project directory',
        ];
    }
}
