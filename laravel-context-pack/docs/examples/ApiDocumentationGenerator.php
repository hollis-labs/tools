<?php

declare(strict_types=1);

namespace App\Generators;

use LaravelContextPack\Generator\AbstractGenerator;
use Symfony\Component\Finder\Finder;

/**
 * API Documentation Generator
 *
 * Scans your Laravel project for API routes and controllers,
 * then generates comprehensive API documentation including:
 * - Endpoint URLs and HTTP methods
 * - Controller methods and parameters
 * - Route middleware
 * - Request validation rules (if present)
 *
 * Example use case:
 * Generate documentation for a REST API to share with frontend developers
 * or external API consumers.
 */
final class ApiDocumentationGenerator extends AbstractGenerator
{
    /**
     * {@inheritdoc}
     */
    public function getName(): string
    {
        return 'api-documentation';
    }

    /**
     * {@inheritdoc}
     */
    public function getDescription(): string
    {
        return 'Generate API endpoint documentation from routes and controllers';
    }

    /**
     * {@inheritdoc}
     */
    public function generate(string $projectPath, string $outputPath): array
    {
        $outputFile = $outputPath . '/api-documentation.md';
        $logFile = $outputPath . '/logs/api-documentation.log';

        $this->appendLog($logFile, '[' . $this->now() . '] Starting API documentation generation');

        try {
            // Step 1: Parse routes/api.php
            $apiRoutes = $this->parseApiRoutes($projectPath);
            $this->appendLog($logFile, sprintf('Found %d API routes', count($apiRoutes)));

            // Step 2: Extract controller information
            $endpoints = $this->analyzeEndpoints($projectPath, $apiRoutes);
            $this->appendLog($logFile, sprintf('Analyzed %d endpoints', count($endpoints)));

            // Step 3: Build documentation
            $documentation = $this->buildDocumentation($projectPath, $endpoints);

            // Step 4: Write output file
            $this->filesystem->dumpFile($outputFile, $documentation);

            $this->appendLog($logFile, '[' . $this->now() . '] API documentation generated successfully');

            return [
                'status' => 'success',
                'message' => 'API documentation generated successfully',
                'outputFile' => $outputFile,
                'endpoints' => count($endpoints),
            ];
        } catch (\Exception $e) {
            $this->appendLog($logFile, '[' . $this->now() . '] ERROR: ' . $e->getMessage());

            return [
                'status' => 'failure',
                'message' => 'Failed to generate API documentation: ' . $e->getMessage(),
            ];
        }
    }

    /**
     * {@inheritdoc}
     */
    public function canRun(string $projectPath): bool
    {
        // Check if API routes file exists
        return $this->isFileAccessible($projectPath . '/routes/api.php');
    }

    /**
     * {@inheritdoc}
     */
    public function getConfigSchema(): array
    {
        return [
            'include_middleware' => [
                'type' => 'boolean',
                'default' => true,
                'description' => 'Include middleware information for each endpoint',
            ],
            'include_validation' => [
                'type' => 'boolean',
                'default' => true,
                'description' => 'Include validation rules if found in FormRequests',
            ],
            'group_by' => [
                'type' => 'string',
                'default' => 'controller',
                'enum' => ['controller', 'prefix', 'method'],
                'description' => 'How to group endpoints in documentation',
            ],
        ];
    }

    /**
     * Parse API routes from routes/api.php
     *
     * @return list<array{method: string, uri: string, action: string, middleware: list<string>}>
     */
    private function parseApiRoutes(string $projectPath): array
    {
        $routesFile = $projectPath . '/routes/api.php';
        $routes = [];

        // In a real implementation, you would:
        // 1. Use Laravel's route:list command to get actual routes
        // 2. Parse the command output
        // 3. Filter for API routes only
        //
        // For this example, we'll demonstrate the structure:

        // Example: Execute `php artisan route:list --json` and parse
        // $output = $this->runArtisanCommand($projectPath, 'route:list --json --path=api');
        // $routes = json_decode($output, true);

        // Simplified example structure:
        $routes[] = [
            'method' => 'GET',
            'uri' => 'api/users',
            'action' => 'App\\Http\\Controllers\\Api\\UserController@index',
            'middleware' => ['api', 'auth:sanctum'],
        ];

        $routes[] = [
            'method' => 'POST',
            'uri' => 'api/users',
            'action' => 'App\\Http\\Controllers\\Api\\UserController@store',
            'middleware' => ['api', 'auth:sanctum'],
        ];

        $routes[] = [
            'method' => 'GET',
            'uri' => 'api/users/{id}',
            'action' => 'App\\Http\\Controllers\\Api\\UserController@show',
            'middleware' => ['api', 'auth:sanctum'],
        ];

        return $routes;
    }

    /**
     * Analyze endpoints and extract additional information
     *
     * @param list<array{method: string, uri: string, action: string, middleware: list<string>}> $routes
     * @return list<array{method: string, uri: string, controller: string, action: string, middleware: list<string>, description: string}>
     */
    private function analyzeEndpoints(string $projectPath, array $routes): array
    {
        $endpoints = [];

        foreach ($routes as $route) {
            // Extract controller and method
            [$controller, $method] = $this->parseAction($route['action']);

            // Try to find controller file and extract PHPDoc
            $description = $this->extractMethodDescription($projectPath, $controller, $method);

            $endpoints[] = [
                'method' => $route['method'],
                'uri' => $route['uri'],
                'controller' => $controller,
                'action' => $method,
                'middleware' => $route['middleware'],
                'description' => $description,
            ];
        }

        return $endpoints;
    }

    /**
     * Parse controller action string
     *
     * @return array{string, string}
     */
    private function parseAction(string $action): array
    {
        if (str_contains($action, '@')) {
            [$controller, $method] = explode('@', $action);
            return [$controller, $method];
        }

        // Handle invokable controllers
        return [$action, '__invoke'];
    }

    /**
     * Extract method description from PHPDoc
     */
    private function extractMethodDescription(string $projectPath, string $controller, string $method): string
    {
        // Convert namespace to file path
        $relativePath = str_replace('\\', '/', str_replace('App\\', 'app/', $controller)) . '.php';
        $filePath = $projectPath . '/' . $relativePath;

        if (!$this->isFileAccessible($filePath)) {
            return 'No description available';
        }

        $content = file_get_contents($filePath);
        if ($content === false) {
            return 'No description available';
        }

        // Simple regex to find PHPDoc before method
        // In a real implementation, use nikic/php-parser for accurate parsing
        $pattern = '/\/\*\*\s*\n\s*\*\s*(.+?)\s*\n.*?public function ' . preg_quote($method, '/') . '/s';
        if (preg_match($pattern, $content, $matches)) {
            return trim($matches[1]);
        }

        return 'No description available';
    }

    /**
     * Build the markdown documentation
     *
     * @param list<array{method: string, uri: string, controller: string, action: string, middleware: list<string>, description: string}> $endpoints
     */
    private function buildDocumentation(string $projectPath, array $endpoints): string
    {
        $lines = [];
        $lines[] = '# API Documentation';
        $lines[] = '';
        $lines[] = '*Generated at:* ' . $this->now();
        $lines[] = '*Project:* ' . basename($projectPath);
        $lines[] = '';
        $lines[] = '## Overview';
        $lines[] = '';
        $lines[] = sprintf('This document describes %d API endpoints available in this application.', count($endpoints));
        $lines[] = '';

        // Group by controller
        $groupedEndpoints = $this->groupByController($endpoints);

        foreach ($groupedEndpoints as $controller => $controllerEndpoints) {
            $lines[] = '## ' . $this->formatControllerName($controller);
            $lines[] = '';

            foreach ($controllerEndpoints as $endpoint) {
                $lines[] = '### `' . $endpoint['method'] . ' /' . $endpoint['uri'] . '`';
                $lines[] = '';
                $lines[] = '**Description:** ' . $endpoint['description'];
                $lines[] = '';
                $lines[] = '**Controller:** `' . $endpoint['controller'] . '@' . $endpoint['action'] . '`';
                $lines[] = '';

                if (!empty($endpoint['middleware'])) {
                    $lines[] = '**Middleware:**';
                    foreach ($endpoint['middleware'] as $middleware) {
                        $lines[] = '- `' . $middleware . '`';
                    }
                    $lines[] = '';
                }

                $lines[] = '---';
                $lines[] = '';
            }
        }

        return implode(PHP_EOL, $lines);
    }

    /**
     * Group endpoints by controller
     *
     * @param list<array{method: string, uri: string, controller: string, action: string, middleware: list<string>, description: string}> $endpoints
     * @return array<string, list<array{method: string, uri: string, controller: string, action: string, middleware: list<string>, description: string}>>
     */
    private function groupByController(array $endpoints): array
    {
        $grouped = [];

        foreach ($endpoints as $endpoint) {
            $controller = $endpoint['controller'];
            if (!isset($grouped[$controller])) {
                $grouped[$controller] = [];
            }
            $grouped[$controller][] = $endpoint;
        }

        return $grouped;
    }

    /**
     * Format controller name for display
     */
    private function formatControllerName(string $controller): string
    {
        // Extract class name from fully qualified class name
        $parts = explode('\\', $controller);
        $className = end($parts);

        // Remove "Controller" suffix
        $name = preg_replace('/Controller$/', '', $className);

        // Add spaces before capital letters
        $name = (string) preg_replace('/([a-z])([A-Z])/', '$1 $2', $name);

        return $name ?: $className;
    }
}
