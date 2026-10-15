<?php

declare(strict_types=1);

namespace App\Generators;

use LaravelContextPack\Generator\AbstractGenerator;
use Symfony\Component\Finder\Finder;

/**
 * Permission Matrix Generator
 *
 * Analyzes Laravel authorization logic and generates a permission matrix
 * showing which users/roles can perform which actions on which resources.
 *
 * Scans:
 * - Policies (app/Policies)
 * - Gates (AuthServiceProvider)
 * - Middleware (can, ability, role)
 * - Authorization checks in controllers
 *
 * Example use case:
 * Document all authorization rules for security audits, onboarding,
 * or compliance requirements.
 */
final class PermissionMatrixGenerator extends AbstractGenerator
{
    /**
     * {@inheritdoc}
     */
    public function getName(): string
    {
        return 'permission-matrix';
    }

    /**
     * {@inheritdoc}
     */
    public function getDescription(): string
    {
        return 'Generate permission matrix for policies and gates';
    }

    /**
     * {@inheritdoc}
     */
    public function generate(string $projectPath, string $outputPath): array
    {
        $outputFile = $outputPath . '/permission-matrix.md';
        $logFile = $outputPath . '/logs/permission-matrix.log';

        $this->appendLog($logFile, '[' . $this->now() . '] Starting permission matrix generation');

        try {
            // Step 1: Discover policies
            $policies = $this->discoverPolicies($projectPath);
            $this->appendLog($logFile, sprintf('Found %d policies', count($policies)));

            // Step 2: Extract gates
            $gates = $this->extractGates($projectPath);
            $this->appendLog($logFile, sprintf('Found %d gates', count($gates)));

            // Step 3: Scan for authorization checks
            $authChecks = $this->scanAuthorizationChecks($projectPath);
            $this->appendLog($logFile, sprintf('Found %d authorization checks', count($authChecks)));

            // Step 4: Build permission matrix
            $documentation = $this->buildDocumentation($projectPath, $policies, $gates, $authChecks);

            // Step 5: Write output file
            $this->filesystem->dumpFile($outputFile, $documentation);

            $this->appendLog($logFile, '[' . $this->now() . '] Permission matrix generated successfully');

            return [
                'status' => 'success',
                'message' => 'Permission matrix generated successfully',
                'outputFile' => $outputFile,
                'policies' => count($policies),
                'gates' => count($gates),
                'authChecks' => count($authChecks),
            ];
        } catch (\Exception $e) {
            $this->appendLog($logFile, '[' . $this->now() . '] ERROR: ' . $e->getMessage());

            return [
                'status' => 'failure',
                'message' => 'Failed to generate permission matrix: ' . $e->getMessage(),
            ];
        }
    }

    /**
     * {@inheritdoc}
     */
    public function canRun(string $projectPath): bool
    {
        // Check if policies directory exists or AuthServiceProvider exists
        return $this->isDirectoryAccessible($projectPath . '/app/Policies')
            || $this->isFileAccessible($projectPath . '/app/Providers/AuthServiceProvider.php');
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
                'description' => 'Include middleware-based authorization',
            ],
            'include_controller_checks' => [
                'type' => 'boolean',
                'default' => true,
                'description' => 'Scan controllers for authorize() calls',
            ],
            'group_by' => [
                'type' => 'string',
                'default' => 'resource',
                'enum' => ['resource', 'ability', 'policy'],
                'description' => 'How to group permissions in the matrix',
            ],
        ];
    }

    /**
     * Discover all policy classes
     *
     * @return array<string, array{class: string, model: string, abilities: list<string>}>
     */
    private function discoverPolicies(string $projectPath): array
    {
        $policies = [];
        $policiesDir = $projectPath . '/app/Policies';

        if (!$this->isDirectoryAccessible($policiesDir)) {
            return [];
        }

        $finder = new Finder();
        $finder->files()->in($policiesDir)->name('*Policy.php');

        foreach ($finder as $file) {
            $content = $file->getContents();
            $className = $this->extractFullyQualifiedClassName($content);
            $abilities = $this->extractPolicyAbilities($content);
            $modelName = $this->extractModelName($className);

            if ($className !== null) {
                $policies[$className] = [
                    'class' => $className,
                    'model' => $modelName,
                    'abilities' => $abilities,
                ];
            }
        }

        return $policies;
    }

    /**
     * Extract fully qualified class name from file content
     */
    private function extractFullyQualifiedClassName(string $content): ?string
    {
        $namespace = null;
        $className = null;

        if (preg_match('/namespace\s+([^;]+);/', $content, $matches)) {
            $namespace = $matches[1];
        }

        if (preg_match('/class\s+([A-Za-z0-9_]+)/', $content, $matches)) {
            $className = $matches[1];
        }

        if ($namespace !== null && $className !== null) {
            return $namespace . '\\' . $className;
        }

        return $className;
    }

    /**
     * Extract policy abilities (public methods)
     *
     * @return list<string>
     */
    private function extractPolicyAbilities(string $content): array
    {
        $abilities = [];

        // Find all public methods except __construct and other magic methods
        preg_match_all('/public\s+function\s+([a-zA-Z][a-zA-Z0-9_]*)\s*\(/', $content, $matches);

        foreach ($matches[1] as $method) {
            if (!str_starts_with($method, '__')) {
                $abilities[] = $method;
            }
        }

        return $abilities;
    }

    /**
     * Extract model name from policy class name
     */
    private function extractModelName(string $policyClassName): string
    {
        // Extract last part of namespace
        $parts = explode('\\', $policyClassName);
        $className = end($parts);

        // Remove "Policy" suffix
        $modelName = preg_replace('/Policy$/', '', $className);

        return $modelName ?: $className;
    }

    /**
     * Extract gates from AuthServiceProvider
     *
     * @return array<string, array{name: string, definition: string}>
     */
    private function extractGates(string $projectPath): array
    {
        $gates = [];
        $authProvider = $projectPath . '/app/Providers/AuthServiceProvider.php';

        if (!$this->isFileAccessible($authProvider)) {
            return [];
        }

        $content = file_get_contents($authProvider);
        if ($content === false) {
            return [];
        }

        // Find Gate::define calls
        // Pattern: Gate::define('ability-name', ...)
        preg_match_all("/Gate::define\s*\(\s*['\"]([^'\"]+)['\"].*?\)/s", $content, $matches);

        foreach ($matches[1] as $index => $gateName) {
            $gates[$gateName] = [
                'name' => $gateName,
                'definition' => $this->extractGateDefinition($matches[0][$index]),
            ];
        }

        return $gates;
    }

    /**
     * Extract gate definition (simplified)
     */
    private function extractGateDefinition(string $gateCall): string
    {
        // Simplify the gate definition for display
        if (preg_match('/function\s*\([^)]*\)\s*\{.*?\}/s', $gateCall, $matches)) {
            return trim($matches[0]);
        }

        return 'Closure or callback';
    }

    /**
     * Scan for authorization checks in controllers
     *
     * @return list<array{file: string, line: int, ability: string, resource: string, method: string}>
     */
    private function scanAuthorizationChecks(string $projectPath): array
    {
        $checks = [];
        $controllersDir = $projectPath . '/app/Http/Controllers';

        if (!$this->isDirectoryAccessible($controllersDir)) {
            return [];
        }

        $finder = new Finder();
        $finder->files()->in($controllersDir)->name('*.php');

        foreach ($finder as $file) {
            $content = $file->getContents();
            $lines = explode("\n", $content);

            foreach ($lines as $lineNumber => $line) {
                // Find $this->authorize() calls
                if (preg_match("/\\\$this->authorize\s*\(\s*['\"]([^'\"]+)['\"].*?\)/", $line, $matches)) {
                    $checks[] = [
                        'file' => $file->getRelativePathname(),
                        'line' => $lineNumber + 1,
                        'ability' => $matches[1],
                        'resource' => $this->guessResourceFromFile($file->getRelativePathname()),
                        'method' => 'authorize',
                    ];
                }

                // Find Gate::allows() calls
                if (preg_match("/Gate::allows\s*\(\s*['\"]([^'\"]+)['\"].*?\)/", $line, $matches)) {
                    $checks[] = [
                        'file' => $file->getRelativePathname(),
                        'line' => $lineNumber + 1,
                        'ability' => $matches[1],
                        'resource' => $this->guessResourceFromFile($file->getRelativePathname()),
                        'method' => 'Gate::allows',
                    ];
                }

                // Find can() calls
                if (preg_match("/->can\s*\(\s*['\"]([^'\"]+)['\"].*?\)/", $line, $matches)) {
                    $checks[] = [
                        'file' => $file->getRelativePathname(),
                        'line' => $lineNumber + 1,
                        'ability' => $matches[1],
                        'resource' => $this->guessResourceFromFile($file->getRelativePathname()),
                        'method' => 'can',
                    ];
                }
            }
        }

        return $checks;
    }

    /**
     * Guess resource name from file path
     */
    private function guessResourceFromFile(string $filePath): string
    {
        // Extract controller name from path
        $fileName = basename($filePath, '.php');
        $resourceName = preg_replace('/Controller$/', '', $fileName);

        return $resourceName ?: $fileName;
    }

    /**
     * Build the markdown documentation
     *
     * @param array<string, array{class: string, model: string, abilities: list<string>}> $policies
     * @param array<string, array{name: string, definition: string}> $gates
     * @param list<array{file: string, line: int, ability: string, resource: string, method: string}> $authChecks
     */
    private function buildDocumentation(string $projectPath, array $policies, array $gates, array $authChecks): string
    {
        $lines = [];
        $lines[] = '# Permission Matrix';
        $lines[] = '';
        $lines[] = '*Generated at:* ' . $this->now();
        $lines[] = '*Project:* ' . basename($projectPath);
        $lines[] = '';
        $lines[] = '## Overview';
        $lines[] = '';
        $lines[] = sprintf('This document describes the authorization system with %d policies, %d gates, and %d authorization checks.', count($policies), count($gates), count($authChecks));
        $lines[] = '';

        // Policies section
        if (!empty($policies)) {
            $lines[] = '## Policies';
            $lines[] = '';

            foreach ($policies as $policy) {
                $lines[] = '### ' . $policy['model'] . ' Policy';
                $lines[] = '';
                $lines[] = '**Class:** `' . $policy['class'] . '`';
                $lines[] = '';
                $lines[] = '**Abilities:**';
                $lines[] = '';

                if (!empty($policy['abilities'])) {
                    $lines[] = '| Ability | Description |';
                    $lines[] = '|---------|-------------|';
                    foreach ($policy['abilities'] as $ability) {
                        $lines[] = sprintf('| `%s` | Check if user can %s %s |', $ability, $ability, strtolower($policy['model']));
                    }
                    $lines[] = '';
                } else {
                    $lines[] = '*No abilities defined*';
                    $lines[] = '';
                }

                $lines[] = '---';
                $lines[] = '';
            }
        }

        // Gates section
        if (!empty($gates)) {
            $lines[] = '## Gates';
            $lines[] = '';
            $lines[] = '| Gate Name | Definition |';
            $lines[] = '|-----------|------------|';

            foreach ($gates as $gate) {
                $definition = str_replace('|', '\\|', substr($gate['definition'], 0, 100));
                if (strlen($gate['definition']) > 100) {
                    $definition .= '...';
                }
                $lines[] = sprintf('| `%s` | %s |', $gate['name'], $definition);
            }
            $lines[] = '';
        }

        // Authorization checks section
        if (!empty($authChecks)) {
            $lines[] = '## Authorization Checks';
            $lines[] = '';
            $lines[] = 'Locations where authorization checks are performed:';
            $lines[] = '';

            // Group by resource
            $checksByResource = [];
            foreach ($authChecks as $check) {
                $resource = $check['resource'];
                if (!isset($checksByResource[$resource])) {
                    $checksByResource[$resource] = [];
                }
                $checksByResource[$resource][] = $check;
            }

            foreach ($checksByResource as $resource => $resourceChecks) {
                $lines[] = '### ' . $resource;
                $lines[] = '';
                $lines[] = '| Ability | Method | Location |';
                $lines[] = '|---------|--------|----------|';

                foreach ($resourceChecks as $check) {
                    $lines[] = sprintf(
                        '| `%s` | `%s` | `%s:%d` |',
                        $check['ability'],
                        $check['method'],
                        $check['file'],
                        $check['line']
                    );
                }
                $lines[] = '';
            }
        }

        // Summary matrix
        $lines[] = '## Permission Summary';
        $lines[] = '';
        $lines[] = 'This table shows all abilities across all resources:';
        $lines[] = '';
        $lines[] = '| Resource | Abilities |';
        $lines[] = '|----------|-----------|';

        foreach ($policies as $policy) {
            $abilitiesList = implode(', ', array_map(fn($a) => "`$a`", $policy['abilities']));
            $lines[] = sprintf('| %s | %s |', $policy['model'], $abilitiesList);
        }

        foreach ($gates as $gate) {
            $lines[] = sprintf('| Gate | `%s` |', $gate['name']);
        }

        $lines[] = '';

        return implode(PHP_EOL, $lines);
    }
}
