<?php

namespace HollisLabs\ToolCrate\Tools;

use Illuminate\JsonSchema\JsonSchema;
use Laravel\Mcp\Request;
use Laravel\Mcp\Response;
use Laravel\Mcp\Server\Tool;
use HollisLabs\ToolCrate\Tools\Contracts\SummarizesTool;
use PDO;

class DbQueryTool extends Tool implements SummarizesTool
{
    protected string $name = 'db_query';
    protected string $title = 'Query database (read-only)';
    protected string $description = 'Execute safe SELECT queries against configured database.';

    public function schema(JsonSchema $s): array
    {
        return [
            'sql' => $s->string()
                ->required()
                ->description('SELECT query to execute'),
            'connection' => $s->string()
                ->default('default')
                ->description('Database connection name'),
            'limit' => $s->integer()
                ->min(1)
                ->max(5000)
                ->default(500)
                ->description('Maximum rows to return'),
            'bindings' => $s->array()
                ->items($s->string())
                ->description('Prepared statement bindings'),
        ];
    }

    public function handle(Request $r): Response
    {
        $sql = trim((string) $r->get('sql'));
        $connection = (string) $r->get('connection', 'default');
        $limit = (int) $r->get('limit', 500);
        $bindings = $r->get('bindings', []);

        // Safety check: only allow SELECT queries
        if (!$this->isSafeQuery($sql)) {
            return Response::error('Only SELECT queries are allowed. Detected non-read operation.');
        }

        // Check if we're in a Laravel context
        if (!function_exists('config') || !function_exists('app')) {
            return Response::error('This tool requires a Laravel application context.');
        }

        try {
            // Get database connection
            $db = app('db');

            // Handle 'default' connection name
            if ($connection === 'default') {
                $connection = config('database.default');
            }

            // Execute query with limit
            $sqlWithLimit = $this->addLimit($sql, $limit);
            $results = $db->connection($connection)
                ->select($sqlWithLimit, is_array($bindings) ? $bindings : []);

            // Convert to array
            $rows = array_map(function ($row) {
                return (array) $row;
            }, $results);

            // Get column names from first row
            $columns = !empty($rows) ? array_keys($rows[0]) : [];

            return Response::json([
                'columns' => $columns,
                'rows' => $rows,
                'count' => count($rows),
                'limited' => count($rows) >= $limit,
                'connection' => $connection,
            ]);
        } catch (\Exception $e) {
            return Response::error('Query failed: ' . $e->getMessage());
        }
    }

    private function isSafeQuery(string $sql): bool
    {
        $normalized = strtolower(preg_replace('/\s+/', ' ', $sql));
        $normalized = trim($normalized);

        // Must start with SELECT
        if (!str_starts_with($normalized, 'select')) {
            return false;
        }

        // Check for dangerous keywords (basic protection)
        $dangerous = ['insert', 'update', 'delete', 'drop', 'alter', 'create', 'truncate', 'replace', 'exec', 'execute'];
        foreach ($dangerous as $keyword) {
            if (str_contains($normalized, $keyword)) {
                return false;
            }
        }

        return true;
    }

    private function addLimit(string $sql, int $limit): string
    {
        // Check if already has LIMIT
        if (preg_match('/\bLIMIT\s+\d+/i', $sql)) {
            return $sql;
        }

        // Add LIMIT at the end
        return rtrim($sql, '; ') . ' LIMIT ' . $limit;
    }

    public static function summaryName(): string { return 'db_query'; }
    public static function summaryTitle(): string { return 'Query database (read-only)'; }
    public static function summaryDescription(): string { return 'Execute safe SELECT queries.'; }
    public static function schemaSummary(): array
    {
        return [
            'sql' => 'SELECT query',
            'connection' => 'connection name (default)',
            'limit' => 'max rows (500 default, 5000 max)',
            'bindings' => 'prepared statement values',
        ];
    }
}
