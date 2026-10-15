<?php

declare(strict_types=1);

namespace LaravelContextPack\Generator;

use Symfony\Component\Filesystem\Filesystem;
use Symfony\Component\Process\Process;

final class RouteSummaryGenerator extends AbstractGenerator
{
    private const JSON_COMMAND = 'php artisan route:list --json';

    private const TEXT_COMMAND = 'php artisan route:list';

    public function __construct(?Filesystem $filesystem = null)
    {
        parent::__construct($filesystem);
    }

    public function getName(): string
    {
        return 'route-summary';
    }

    public function getDescription(): string
    {
        return 'Generate route summary with controller mapping and JSON metadata';
    }

    /**
     * @return array{status: string, message: string}
     */
    public function generate(string $projectPath, string $outputPath): array
    {
        $logPath = $outputPath . '/logs/route-summary.log';

        $jsonResult = $this->runCommand(self::JSON_COMMAND, $projectPath, $logPath);
        if ($jsonResult['success']) {
            $routes = $this->decodeJsonRoutes($jsonResult['output']);
            if ($routes !== null) {
                $normalized = $this->normalizeRoutes($routes, $projectPath);
                $this->writeOutputs($normalized, $outputPath);

                return [
                    'status' => 'success',
                    'message' => 'Route summary generated from JSON export.',
                ];
            }
        }

        $textResult = $this->runCommand(self::TEXT_COMMAND, $projectPath, $logPath);
        if ($textResult['success']) {
            $routes = $this->parseTextRoutes($textResult['output']);
            if ($routes !== null) {
                $normalized = $this->normalizeRoutes($routes, $projectPath);
                $this->writeOutputs($normalized, $outputPath);

                return [
                    'status' => 'success',
                    'message' => 'Route summary generated from text export.',
                ];
            }
        }

        return [
            'status' => 'failed',
            'message' => 'Unable to parse route list output; see logs/route-summary.log.',
        ];
    }

    /**
     * @return array{success: bool, output: string}
     */
    private function runCommand(string $command, string $cwd, string $logPath): array
    {
        $process = Process::fromShellCommandline($command, $cwd);
        $process->setTimeout(30);

        $this->appendLog($logPath, sprintf('[%s] Running: %s', $this->now(), $command));
        $process->run();

        if (! $process->isSuccessful()) {
            $this->appendLog($logPath, sprintf('[%s] Exit code %d', $this->now(), $process->getExitCode() ?? -1));
            $this->appendLog($logPath, trim($process->getOutput()));
            $this->appendLog($logPath, trim($process->getErrorOutput()));

            return ['success' => false, 'output' => ''];
        }

        $output = trim($process->getOutput());
        $this->appendLog($logPath, $output);

        return ['success' => true, 'output' => $output];
    }

    /**
     * @return array<int, array<string, mixed>>|null
     */
    private function decodeJsonRoutes(string $output): ?array
    {
        if ($output === '') {
            return null;
        }

        $data = json_decode($output, true);
        if ($data === null || ! is_array($data)) {
            return null;
        }

        return $data;
    }

    /**
     * @return array<int, array<string, mixed>>|null
     */
    private function parseTextRoutes(string $output): ?array
    {
        if ($output === '') {
            return null;
        }

        $lines = array_values(array_filter(array_map('trim', explode("\n", $output))));
        if (count($lines) < 2) {
            return null;
        }

        $routeLines = [];
        foreach ($lines as $line) {
            if (preg_match('/^(GET|HEAD|POST|PUT|PATCH|DELETE|OPTIONS|ANY)\s+([^\s]+)\s+([^\s]+)\s+([^\s]+)\s*(.*)$/i', $line, $matches) === 1) {
                $routeLines[] = [
                    'method' => $matches[1],
                    'uri' => $matches[2],
                    'name' => $matches[3] !== '—' ? $matches[3] : null,
                    'action' => $matches[4],
                    'middleware' => trim($matches[5]) !== '' ? preg_split('/\s*,\s*/', trim($matches[5])) : [],
                    'prefix' => null,
                ];
            }
        }

        if ($routeLines === []) {
            return null;
        }

        return $routeLines;
    }

    /**
     * @param  array<int, array<string, mixed>>  $routes
     */
    private function writeOutputs(array $routes, string $outputPath): void
    {
        $summary = $this->buildMarkdown($routes);
        $this->filesystem->dumpFile($outputPath . '/02-route-summary.md', $summary);
        $this->filesystem->dumpFile(
            $outputPath . '/02-route-summary.json',
            json_encode($routes, JSON_PRETTY_PRINT | JSON_UNESCAPED_SLASHES) . PHP_EOL,
        );
    }

    /**
     * @param  array<int, array<string, mixed>>  $routes
     */
    private function buildMarkdown(array $routes): string
    {
        $totalRoutes = count($routes);
        $methods = [];
        $methodTotals = [];
        $routesMissingNames = [];
        $closureRoutes = [];

        foreach ($routes as $route) {
            $methodList = (array) ($route['method'] ?? []);
            foreach ($methodList as $method) {
                $method = strtoupper((string) $method);
                $methods[] = $method;
                $methodTotals[$method] = ($methodTotals[$method] ?? 0) + 1;
            }

            if (($route['name'] ?? null) === null || $route['name'] === '') {
                $routesMissingNames[] = $route;
            }

            $action = (string) ($route['action'] ?? '');
            if ($action === 'Closure' || str_contains(strtolower($action), 'closure@')) {
                $closureRoutes[] = $route;
            }
        }

        $uniqueMethods = implode(', ', array_unique($methods));

        $lines = [];
        $lines[] = '# Route Summary';
        $lines[] = '';
        $lines[] = '## Overview';
        $lines[] = sprintf('- Total routes: %d', $totalRoutes);
        $lines[] = sprintf('- Methods: %s', $uniqueMethods ?: 'N/A');
        $lines[] = '';
        $lines[] = '## Metrics';
        if ($methodTotals !== []) {
            foreach ($methodTotals as $method => $count) {
                $lines[] = sprintf('- %s routes: %d', $method, $count);
            }
        } else {
            $lines[] = '- No routes detected.';
        }
        $lines[] = '';

        if ($routesMissingNames !== [] || $closureRoutes !== []) {
            $lines[] = '## Warnings';
            if ($routesMissingNames !== []) {
                $lines[] = sprintf('- %d route(s) missing a name.', count($routesMissingNames));
            }
            if ($closureRoutes !== []) {
                $lines[] = sprintf('- %d route(s) using Closure actions.', count($closureRoutes));
            }
            $lines[] = '';
        }

        $lines[] = '## Routes';

        $grouped = $this->groupRoutes($routes);
        foreach ($grouped as $group => $items) {
            $lines[] = sprintf('### Prefix: %s', $group === '__root__' ? 'none' : $group);
            $lines[] = '';
            $lines[] = '| Method | URI | Name | Action | Middleware | Source |';
            $lines[] = '|-------|-----|------|--------|------------|--------|';

            foreach ($items as $route) {
                $middleware = implode(', ', $route['middleware'] ?? []);
                $sourceCell = $this->formatSourceCell($route);

                $lines[] = sprintf(
                    '| %s | %s | %s | %s | %s | %s |',
                    $this->escapePipe(implode(',', $route['method'] ?? [])),
                    $this->escapePipe((string) ($route['uri'] ?? '')),
                    $this->escapePipe((string) ($route['name'] ?? '—')),
                    $this->escapePipe((string) ($route['action'] ?? '—')),
                    $this->escapePipe($middleware ?: '—'),
                    $this->escapePipe($sourceCell),
                );
            }

            $lines[] = '';
        }

        return implode(PHP_EOL, $lines) . PHP_EOL;
    }

    /**
     * @param  array<int, array<string, mixed>>  $routes
     * @return array<string, array<int, array<string, mixed>>>
     */
    private function groupRoutes(array $routes): array
    {
        $grouped = [];

        foreach ($routes as $route) {
            $prefix = $route['prefix'] ?? $route['uri'] ?? null;
            if (is_string($prefix) && str_contains($prefix, '/')) {
                $prefix = explode('/', $prefix)[0];
            }

            $key = $prefix ?: '__root__';
            $grouped[$key][] = $route;
        }

        ksort($grouped);

        return $grouped;
    }

    /**
     * @param  array<int, array<string, mixed>>  $routes
     * @return array<int, array{method: array<int, string>, uri: string, name: ?string, action: string, middleware: array<int, string>, prefix: ?string}>
     */
    private function normalizeRoutes(array $routes, string $projectPath): array
    {
        $normalized = [];

        foreach ($routes as $route) {
            $methods = (array) ($route['method'] ?? []);
            if ($methods === [] && isset($route['method'])) {
                $methods = [(string) $route['method']];
            }

            $action = (string) ($route['action'] ?? '');
            [$controller, $controllerMethod] = $this->parseController($action);
            $controllerPath = $controller !== null
                ? $this->resolveControllerPath($controller, $projectPath)
                : null;

            $normalized[] = [
                'method' => array_values(array_map(static fn ($method) => strtoupper((string) $method), $methods)),
                'uri' => (string) ($route['uri'] ?? ''),
                'name' => ($route['name'] ?? null) !== '' ? ($route['name'] ?? null) : null,
                'action' => $action,
                'controller' => $controller,
                'controllerMethod' => $controllerMethod,
                'controllerPath' => $controllerPath,
                'middleware' => array_values(array_map(static fn ($mw) => (string) $mw, (array) ($route['middleware'] ?? []))),
                'prefix' => isset($route['prefix']) && $route['prefix'] !== '' ? (string) $route['prefix'] : null,
            ];
        }

        return $normalized;
    }

    /**
     * @return array{0: ?string, 1: ?string}
     */
    private function parseController(string $action): array
    {
        if ($action === '' || str_starts_with(strtolower($action), 'closure')) {
            return [null, null];
        }

        if (str_contains($action, '@')) {
            [$controller, $method] = explode('@', $action, 2);

            return [$controller !== '' ? $controller : null, $method !== '' ? $method : null];
        }

        if (str_contains($action, '::')) {
            [$controller, $method] = explode('::', $action, 2);

            return [$controller !== '' ? $controller : null, $method !== '' ? $method : null];
        }

        return [$action, null];
    }

    private function resolveControllerPath(string $controller, string $projectPath): ?string
    {
        $controller = ltrim($controller, '\\');
        $candidates = [];

        if (str_starts_with($controller, 'App\\')) {
            $candidates[] = $projectPath . '/app/' . str_replace('\\', '/', substr($controller, 4)) . '.php';
        }

        $candidates[] = $projectPath . '/' . str_replace('\\', '/', $controller) . '.php';

        foreach ($candidates as $candidate) {
            if (is_file($candidate)) {
                return rtrim($this->filesystem->makePathRelative($candidate, $projectPath . '/'), '/');
            }
        }

        return null;
    }

    /**
     * @param  array<string, mixed>  $route
     */
    private function formatSourceCell(array $route): string
    {
        $controllerPath = $route['controllerPath'] ?? null;
        $controller = $route['controller'] ?? null;
        $method = $route['controllerMethod'] ?? null;

        if ($controllerPath !== null) {
            return sprintf('`%s%s`', $controllerPath, $method !== null ? '@' . $method : '');
        }

        if ($controller !== null) {
            return sprintf('`%s%s`', $controller, $method !== null ? '@' . $method : '');
        }

        return '—';
    }

    private function escapePipe(string $value): string
    {
        return str_replace('|', '\\|', $value);
    }
}
