<?php

declare(strict_types=1);

namespace LaravelContextPack\Generator;

use Symfony\Component\Filesystem\Filesystem;
use Symfony\Component\Process\Process;

final class RouteSummaryGenerator
{
    private const JSON_COMMAND = 'php artisan route:list --json';

    private const TEXT_COMMAND = 'php artisan route:list';

    private Filesystem $filesystem;

    public function __construct(?Filesystem $filesystem = null)
    {
        $this->filesystem = $filesystem ?? new Filesystem;
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
                $this->writeSummary($routes, $outputPath);

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
                $this->writeSummary($routes, $outputPath);

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
     * @param  array<int, array<string, mixed>>|null  $routes
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
    private function writeSummary(array $routes, string $outputPath): void
    {
        $summary = $this->buildMarkdown($routes);
        $this->filesystem->dumpFile($outputPath . '/02-route-summary.md', $summary);
    }

    /**
     * @param  array<int, array<string, mixed>>  $routes
     */
    private function buildMarkdown(array $routes): string
    {
        $totalRoutes = count($routes);
        $methods = [];
        foreach ($routes as $route) {
            $methods = array_merge($methods, (array) ($route['method'] ?? []));
        }
        $uniqueMethods = implode(', ', array_unique(array_map('strtoupper', $methods)));

        $lines = [];
        $lines[] = '# Route Summary';
        $lines[] = '';
        $lines[] = '## Overview';
        $lines[] = sprintf('- Total routes: %d', $totalRoutes);
        $lines[] = sprintf('- Methods: %s', $uniqueMethods ?: 'N/A');
        $lines[] = '';
        $lines[] = '## Routes';

        $grouped = $this->groupRoutes($routes);
        foreach ($grouped as $group => $items) {
            $lines[] = sprintf('### Prefix: %s', $group === '__root__' ? 'none' : $group);
            $lines[] = '';
            $lines[] = '| Method | URI | Name | Action | Middleware |';
            $lines[] = '|-------|-----|------|--------|------------|';

            foreach ($items as $route) {
                $middleware = $route['middleware'] ?? [];
                if (is_array($middleware)) {
                    $middleware = implode(', ', $middleware);
                }

                $lines[] = sprintf(
                    '| %s | %s | %s | %s | %s |',
                    $this->escapePipe(is_array($route['method'] ?? null) ? implode(',', $route['method']) : (string) ($route['method'] ?? '')),
                    $this->escapePipe((string) ($route['uri'] ?? '')),
                    $this->escapePipe((string) ($route['name'] ?? '—')),
                    $this->escapePipe((string) ($route['action'] ?? '—')),
                    $this->escapePipe($middleware ?: '—'),
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

    private function appendLog(string $logPath, string $message): void
    {
        $message = trim($message);
        if ($message === '') {
            return;
        }

        $this->filesystem->appendToFile($logPath, $message . PHP_EOL);
    }

    private function now(): string
    {
        return (new \DateTimeImmutable)->format(DATE_ATOM);
    }

    private function escapePipe(string $value): string
    {
        return str_replace('|', '\\|', $value);
    }
}
