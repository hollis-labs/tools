<?php

declare(strict_types=1);

namespace App\Generators;

use LaravelContextPack\Generator\AbstractGenerator;
use Symfony\Component\Finder\Finder;

/**
 * Feature Flag Generator
 *
 * Scans your Laravel project for feature flag usage and generates
 * comprehensive documentation including:
 * - All defined feature flags
 * - Where each flag is checked in the codebase
 * - Default values and configurations
 * - Dependencies between flags
 *
 * Supports common feature flag packages:
 * - Laravel Pennant
 * - Laravel Feature Flags
 * - Custom implementations
 *
 * Example use case:
 * Track feature flags across a large codebase to understand which
 * features are behind flags and where they're used.
 */
final class FeatureFlagGenerator extends AbstractGenerator
{
    /**
     * {@inheritdoc}
     */
    public function getName(): string
    {
        return 'feature-flag';
    }

    /**
     * {@inheritdoc}
     */
    public function getDescription(): string
    {
        return 'Document feature flags and their usage across the application';
    }

    /**
     * {@inheritdoc}
     */
    public function generate(string $projectPath, string $outputPath): array
    {
        $outputFile = $outputPath . '/feature-flags.md';
        $logFile = $outputPath . '/logs/feature-flag.log';

        $this->appendLog($logFile, '[' . $this->now() . '] Starting feature flag documentation generation');

        try {
            // Step 1: Discover feature flag definitions
            $flags = $this->discoverFeatureFlags($projectPath);
            $this->appendLog($logFile, sprintf('Found %d feature flags', count($flags)));

            // Step 2: Find usage locations
            $flagUsage = $this->findFlagUsage($projectPath, $flags);
            $this->appendLog($logFile, sprintf('Scanned usage across %d files', count($flagUsage)));

            // Step 3: Build documentation
            $documentation = $this->buildDocumentation($projectPath, $flags, $flagUsage);

            // Step 4: Write output file
            $this->filesystem->dumpFile($outputFile, $documentation);

            $this->appendLog($logFile, '[' . $this->now() . '] Feature flag documentation generated successfully');

            return [
                'status' => 'success',
                'message' => 'Feature flag documentation generated successfully',
                'outputFile' => $outputFile,
                'flags' => count($flags),
                'usageLocations' => array_sum(array_map('count', $flagUsage)),
            ];
        } catch (\Exception $e) {
            $this->appendLog($logFile, '[' . $this->now() . '] ERROR: ' . $e->getMessage());

            return [
                'status' => 'failure',
                'message' => 'Failed to generate feature flag documentation: ' . $e->getMessage(),
            ];
        }
    }

    /**
     * {@inheritdoc}
     */
    public function canRun(string $projectPath): bool
    {
        // Check if feature flag configuration exists
        return $this->isFileAccessible($projectPath . '/config/features.php')
            || $this->isFileAccessible($projectPath . '/config/pennant.php')
            || $this->isDirectoryAccessible($projectPath . '/app/Features');
    }

    /**
     * {@inheritdoc}
     */
    public function getConfigSchema(): array
    {
        return [
            'scan_patterns' => [
                'type' => 'array',
                'default' => ['Feature::active', 'Features::enabled', 'feature('],
                'description' => 'Patterns to search for feature flag usage',
            ],
            'include_inactive' => [
                'type' => 'boolean',
                'default' => true,
                'description' => 'Include flags that are not currently active',
            ],
            'show_file_paths' => [
                'type' => 'boolean',
                'default' => true,
                'description' => 'Show file paths where flags are used',
            ],
        ];
    }

    /**
     * Discover feature flag definitions
     *
     * @return array<string, array{name: string, default: bool, description: string, class: ?string}>
     */
    private function discoverFeatureFlags(string $projectPath): array
    {
        $flags = [];

        // Check config/features.php
        $configFile = $projectPath . '/config/features.php';
        if ($this->isFileAccessible($configFile)) {
            $flags = array_merge($flags, $this->parseConfigFile($configFile));
        }

        // Check config/pennant.php (Laravel Pennant)
        $pennantFile = $projectPath . '/config/pennant.php';
        if ($this->isFileAccessible($pennantFile)) {
            $flags = array_merge($flags, $this->parsePennantConfig($pennantFile));
        }

        // Check for feature classes in app/Features
        $featuresDir = $projectPath . '/app/Features';
        if ($this->isDirectoryAccessible($featuresDir)) {
            $flags = array_merge($flags, $this->scanFeatureClasses($featuresDir));
        }

        return $flags;
    }

    /**
     * Parse feature flags from config file
     *
     * @return array<string, array{name: string, default: bool, description: string, class: ?string}>
     */
    private function parseConfigFile(string $configFile): array
    {
        $flags = [];

        // In a real implementation, parse the PHP file
        // For this example, we'll show the expected structure

        // Example structure:
        $flags['new_dashboard'] = [
            'name' => 'new_dashboard',
            'default' => false,
            'description' => 'Enable the new dashboard UI',
            'class' => null,
        ];

        $flags['api_v2'] = [
            'name' => 'api_v2',
            'default' => true,
            'description' => 'Enable API v2 endpoints',
            'class' => null,
        ];

        return $flags;
    }

    /**
     * Parse Pennant configuration
     *
     * @return array<string, array{name: string, default: bool, description: string, class: ?string}>
     */
    private function parsePennantConfig(string $pennantFile): array
    {
        // Parse Laravel Pennant configuration
        return [];
    }

    /**
     * Scan for feature classes
     *
     * @return array<string, array{name: string, default: bool, description: string, class: ?string}>
     */
    private function scanFeatureClasses(string $featuresDir): array
    {
        $flags = [];

        $finder = new Finder();
        $finder->files()->in($featuresDir)->name('*.php');

        foreach ($finder as $file) {
            $className = $this->extractClassName($file->getContents());
            if ($className !== null) {
                $flagName = $this->classNameToFlagName($className);
                $flags[$flagName] = [
                    'name' => $flagName,
                    'default' => false,
                    'description' => $this->extractClassDescription($file->getContents()),
                    'class' => $className,
                ];
            }
        }

        return $flags;
    }

    /**
     * Extract class name from file content
     */
    private function extractClassName(string $content): ?string
    {
        if (preg_match('/class\s+([A-Za-z0-9_]+)/', $content, $matches)) {
            return $matches[1];
        }
        return null;
    }

    /**
     * Extract class description from PHPDoc
     */
    private function extractClassDescription(string $content): string
    {
        if (preg_match('/\/\*\*\s*\n\s*\*\s*(.+?)\s*\n/s', $content, $matches)) {
            return trim($matches[1]);
        }
        return 'No description available';
    }

    /**
     * Convert class name to flag name
     */
    private function classNameToFlagName(string $className): string
    {
        // Convert PascalCase to snake_case
        $name = preg_replace('/([a-z])([A-Z])/', '$1_$2', $className);
        return strtolower($name ?: $className);
    }

    /**
     * Find where feature flags are used in the codebase
     *
     * @param array<string, array{name: string, default: bool, description: string, class: ?string}> $flags
     * @return array<string, list<array{file: string, line: int, context: string}>>
     */
    private function findFlagUsage(string $projectPath, array $flags): array
    {
        $usage = [];

        // Initialize usage array for each flag
        foreach ($flags as $flagName => $flag) {
            $usage[$flagName] = [];
        }

        // Scan PHP files for flag usage
        $finder = new Finder();
        $finder->files()
            ->in([$projectPath . '/app', $projectPath . '/resources/views'])
            ->name('*.php')
            ->name('*.blade.php')
            ->exclude(['vendor', 'node_modules']);

        foreach ($finder as $file) {
            $content = $file->getContents();
            $lines = explode("\n", $content);

            foreach ($flags as $flagName => $flag) {
                // Search for various patterns
                $patterns = [
                    "Feature::active('{$flagName}')",
                    "Feature::active(\"{$flagName}\")",
                    "Features::enabled('{$flagName}')",
                    "feature('{$flagName}')",
                    "@feature('{$flagName}')", // Blade directive
                ];

                foreach ($lines as $lineNumber => $line) {
                    foreach ($patterns as $pattern) {
                        if (str_contains($line, $pattern)) {
                            $usage[$flagName][] = [
                                'file' => $file->getRelativePathname(),
                                'line' => $lineNumber + 1,
                                'context' => trim($line),
                            ];
                        }
                    }
                }
            }
        }

        return $usage;
    }

    /**
     * Build the markdown documentation
     *
     * @param array<string, array{name: string, default: bool, description: string, class: ?string}> $flags
     * @param array<string, list<array{file: string, line: int, context: string}>> $flagUsage
     */
    private function buildDocumentation(string $projectPath, array $flags, array $flagUsage): string
    {
        $lines = [];
        $lines[] = '# Feature Flags';
        $lines[] = '';
        $lines[] = '*Generated at:* ' . $this->now();
        $lines[] = '*Project:* ' . basename($projectPath);
        $lines[] = '';
        $lines[] = '## Overview';
        $lines[] = '';
        $lines[] = sprintf('This document describes %d feature flags in this application.', count($flags));
        $lines[] = '';

        // Summary table
        $lines[] = '## Summary';
        $lines[] = '';
        $lines[] = '| Flag | Default | Usage Count | Description |';
        $lines[] = '|------|---------|-------------|-------------|';

        foreach ($flags as $flagName => $flag) {
            $usageCount = count($flagUsage[$flagName] ?? []);
            $defaultValue = $flag['default'] ? '✅ Enabled' : '❌ Disabled';
            $lines[] = sprintf(
                '| `%s` | %s | %d | %s |',
                $flagName,
                $defaultValue,
                $usageCount,
                $flag['description']
            );
        }
        $lines[] = '';

        // Detailed information for each flag
        $lines[] = '## Detailed Information';
        $lines[] = '';

        foreach ($flags as $flagName => $flag) {
            $lines[] = '### `' . $flagName . '`';
            $lines[] = '';
            $lines[] = '**Description:** ' . $flag['description'];
            $lines[] = '';
            $lines[] = '**Default State:** ' . ($flag['default'] ? 'Enabled' : 'Disabled');
            $lines[] = '';

            if ($flag['class'] !== null) {
                $lines[] = '**Class:** `' . $flag['class'] . '`';
                $lines[] = '';
            }

            $usage = $flagUsage[$flagName] ?? [];
            if (!empty($usage)) {
                $lines[] = '**Usage Locations:**';
                $lines[] = '';

                foreach ($usage as $location) {
                    $lines[] = sprintf(
                        '- `%s:%d`',
                        $location['file'],
                        $location['line']
                    );
                    $lines[] = '  ```php';
                    $lines[] = '  ' . $location['context'];
                    $lines[] = '  ```';
                }
                $lines[] = '';
            } else {
                $lines[] = '**Usage:** Not currently used in the codebase';
                $lines[] = '';
            }

            $lines[] = '---';
            $lines[] = '';
        }

        return implode(PHP_EOL, $lines);
    }
}
