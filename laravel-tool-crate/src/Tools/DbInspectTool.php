<?php

namespace HollisLabs\ToolCrate\Tools;

use Illuminate\JsonSchema\JsonSchema;
use Laravel\Mcp\Request;
use Laravel\Mcp\Response;
use Laravel\Mcp\Server\Tool;
use HollisLabs\ToolCrate\Tools\Contracts\SummarizesTool;

class DbInspectTool extends Tool implements SummarizesTool
{
    protected string $name = 'db_inspect';
    protected string $title = 'Inspect database schema';
    protected string $description = 'List tables, columns, indexes, and foreign keys.';

    public function schema(JsonSchema $s): array
    {
        return [
            'connection' => $s->string()
                ->default('default')
                ->description('Database connection name'),
            'table' => $s->string()
                ->description('Specific table to inspect (optional)'),
            'show_columns' => $s->boolean()
                ->default(true)
                ->description('Include column details'),
            'show_indexes' => $s->boolean()
                ->default(true)
                ->description('Include index information'),
            'show_foreign_keys' => $s->boolean()
                ->default(true)
                ->description('Include foreign key constraints'),
        ];
    }

    public function handle(Request $r): Response
    {
        $connection = (string) $r->get('connection', 'default');
        $table = $r->get('table');
        $showColumns = (bool) $r->get('show_columns', true);
        $showIndexes = (bool) $r->get('show_indexes', true);
        $showForeignKeys = (bool) $r->get('show_foreign_keys', true);

        // Check if we're in a Laravel context
        if (!function_exists('config') || !function_exists('app')) {
            return Response::error('This tool requires a Laravel application context.');
        }

        try {
            $db = app('db');

            // Handle 'default' connection name
            if ($connection === 'default') {
                $connection = config('database.default');
            }

            $conn = $db->connection($connection);
            $driver = $conn->getDriverName();

            if ($table) {
                // Inspect specific table
                $result = $this->inspectTable($conn, $driver, $table, $showColumns, $showIndexes, $showForeignKeys);
            } else {
                // List all tables
                $result = $this->listTables($conn, $driver, $showColumns);
            }

            return Response::json([
                'connection' => $connection,
                'driver' => $driver,
                ...$result,
            ]);
        } catch (\Exception $e) {
            return Response::error('Inspection failed: ' . $e->getMessage());
        }
    }

    private function listTables($conn, string $driver, bool $showColumns): array
    {
        $tables = match ($driver) {
            'mysql' => $conn->select('SHOW TABLES'),
            'pgsql' => $conn->select("SELECT tablename as table_name FROM pg_catalog.pg_tables WHERE schemaname = 'public'"),
            'sqlite' => $conn->select("SELECT name as table_name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'"),
            default => [],
        };

        $result = [];
        foreach ($tables as $table) {
            $tableName = (array) $table;
            $tableName = array_values($tableName)[0];

            $tableInfo = ['name' => $tableName];

            // Get row count
            try {
                $count = $conn->table($tableName)->count();
                $tableInfo['row_count'] = $count;
            } catch (\Exception $e) {
                $tableInfo['row_count'] = null;
            }

            if ($showColumns) {
                $tableInfo['columns'] = $this->getColumns($conn, $driver, $tableName);
            }

            $result[] = $tableInfo;
        }

        return ['tables' => $result];
    }

    private function inspectTable($conn, string $driver, string $table, bool $showColumns, bool $showIndexes, bool $showForeignKeys): array
    {
        $result = [
            'table' => $table,
        ];

        // Get row count
        try {
            $result['row_count'] = $conn->table($table)->count();
        } catch (\Exception $e) {
            $result['row_count'] = null;
        }

        if ($showColumns) {
            $result['columns'] = $this->getColumns($conn, $driver, $table);
        }

        if ($showIndexes) {
            $result['indexes'] = $this->getIndexes($conn, $driver, $table);
        }

        if ($showForeignKeys) {
            $result['foreign_keys'] = $this->getForeignKeys($conn, $driver, $table);
        }

        return $result;
    }

    private function getColumns($conn, string $driver, string $table): array
    {
        $columns = match ($driver) {
            'mysql' => $conn->select("SHOW COLUMNS FROM `{$table}`"),
            'pgsql' => $conn->select("
                SELECT column_name, data_type, is_nullable, column_default
                FROM information_schema.columns
                WHERE table_name = ?
                ORDER BY ordinal_position
            ", [$table]),
            'sqlite' => $conn->select("PRAGMA table_info(`{$table}`)"),
            default => [],
        };

        $result = [];
        foreach ($columns as $col) {
            $col = (array) $col;
            $result[] = match ($driver) {
                'mysql' => [
                    'name' => $col['Field'],
                    'type' => $col['Type'],
                    'nullable' => $col['Null'] === 'YES',
                    'default' => $col['Default'],
                    'key' => $col['Key'],
                ],
                'pgsql' => [
                    'name' => $col['column_name'],
                    'type' => $col['data_type'],
                    'nullable' => $col['is_nullable'] === 'YES',
                    'default' => $col['column_default'],
                ],
                'sqlite' => [
                    'name' => $col['name'],
                    'type' => $col['type'],
                    'nullable' => $col['notnull'] == 0,
                    'default' => $col['dflt_value'],
                    'primary_key' => $col['pk'] == 1,
                ],
                default => $col,
            };
        }

        return $result;
    }

    private function getIndexes($conn, string $driver, string $table): array
    {
        try {
            $indexes = match ($driver) {
                'mysql' => $conn->select("SHOW INDEX FROM `{$table}`"),
                'pgsql' => $conn->select("
                    SELECT indexname, indexdef
                    FROM pg_indexes
                    WHERE tablename = ?
                ", [$table]),
                'sqlite' => $conn->select("PRAGMA index_list(`{$table}`)"),
                default => [],
            };

            $result = [];
            foreach ($indexes as $idx) {
                $idx = (array) $idx;
                $result[] = match ($driver) {
                    'mysql' => [
                        'name' => $idx['Key_name'],
                        'column' => $idx['Column_name'],
                        'unique' => $idx['Non_unique'] == 0,
                    ],
                    'pgsql' => [
                        'name' => $idx['indexname'],
                        'definition' => $idx['indexdef'],
                    ],
                    'sqlite' => [
                        'name' => $idx['name'],
                        'unique' => $idx['unique'] == 1,
                    ],
                    default => $idx,
                };
            }

            return $result;
        } catch (\Exception $e) {
            return [];
        }
    }

    private function getForeignKeys($conn, string $driver, string $table): array
    {
        try {
            $foreignKeys = match ($driver) {
                'mysql' => $conn->select("
                    SELECT
                        CONSTRAINT_NAME,
                        COLUMN_NAME,
                        REFERENCED_TABLE_NAME,
                        REFERENCED_COLUMN_NAME
                    FROM information_schema.KEY_COLUMN_USAGE
                    WHERE TABLE_SCHEMA = DATABASE()
                    AND TABLE_NAME = ?
                    AND REFERENCED_TABLE_NAME IS NOT NULL
                ", [$table]),
                'pgsql' => $conn->select("
                    SELECT
                        tc.constraint_name,
                        kcu.column_name,
                        ccu.table_name AS foreign_table_name,
                        ccu.column_name AS foreign_column_name
                    FROM information_schema.table_constraints AS tc
                    JOIN information_schema.key_column_usage AS kcu
                        ON tc.constraint_name = kcu.constraint_name
                    JOIN information_schema.constraint_column_usage AS ccu
                        ON ccu.constraint_name = tc.constraint_name
                    WHERE tc.constraint_type = 'FOREIGN KEY' AND tc.table_name = ?
                ", [$table]),
                'sqlite' => $conn->select("PRAGMA foreign_key_list(`{$table}`)"),
                default => [],
            };

            $result = [];
            foreach ($foreignKeys as $fk) {
                $fk = (array) $fk;
                $result[] = match ($driver) {
                    'mysql' => [
                        'name' => $fk['CONSTRAINT_NAME'],
                        'column' => $fk['COLUMN_NAME'],
                        'references_table' => $fk['REFERENCED_TABLE_NAME'],
                        'references_column' => $fk['REFERENCED_COLUMN_NAME'],
                    ],
                    'pgsql' => [
                        'name' => $fk['constraint_name'],
                        'column' => $fk['column_name'],
                        'references_table' => $fk['foreign_table_name'],
                        'references_column' => $fk['foreign_column_name'],
                    ],
                    'sqlite' => [
                        'column' => $fk['from'],
                        'references_table' => $fk['table'],
                        'references_column' => $fk['to'],
                    ],
                    default => $fk,
                };
            }

            return $result;
        } catch (\Exception $e) {
            return [];
        }
    }

    public static function summaryName(): string { return 'db_inspect'; }
    public static function summaryTitle(): string { return 'Inspect database schema'; }
    public static function summaryDescription(): string { return 'List tables, columns, indexes, FKs.'; }
    public static function schemaSummary(): array
    {
        return [
            'connection' => 'connection name (default)',
            'table' => 'specific table (optional)',
            'show_columns' => 'include columns',
            'show_indexes' => 'include indexes',
            'show_foreign_keys' => 'include foreign keys',
        ];
    }
}
