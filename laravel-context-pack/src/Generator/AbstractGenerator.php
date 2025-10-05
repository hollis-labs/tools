<?php

declare(strict_types=1);

namespace LaravelContextPack\Generator;

use Symfony\Component\Filesystem\Filesystem;

/**
 * Abstract base class providing common functionality for generators.
 *
 * Provides:
 * - Filesystem access
 * - Logging utilities
 * - Default implementations for optional interface methods
 * - Lifecycle hooks (before/after generation)
 */
abstract class AbstractGenerator implements GeneratorInterface
{
    protected Filesystem $filesystem;

    public function __construct(?Filesystem $filesystem = null)
    {
        $this->filesystem = $filesystem ?? new Filesystem();
    }

    /**
     * {@inheritdoc}
     */
    abstract public function getName(): string;

    /**
     * {@inheritdoc}
     */
    abstract public function getDescription(): string;

    /**
     * {@inheritdoc}
     */
    abstract public function generate(string $projectPath, string $outputPath): array;

    /**
     * {@inheritdoc}
     *
     * Default implementation: always return true.
     * Override in subclasses to add pre-run checks.
     */
    public function canRun(string $projectPath): bool
    {
        return true;
    }

    /**
     * {@inheritdoc}
     *
     * Default implementation: no configuration.
     * Override in subclasses to define configuration schema.
     *
     * @return array<string, array<string, mixed>>
     */
    public function getConfigSchema(): array
    {
        return [];
    }

    /**
     * Hook called before generation starts.
     *
     * Override to perform setup tasks.
     */
    protected function beforeGenerate(string $projectPath, string $outputPath): void
    {
        // Override in subclasses if needed
    }

    /**
     * Hook called after generation completes.
     *
     * Override to perform cleanup tasks.
     *
     * @param array{status: string, message: string, ...} $result
     */
    protected function afterGenerate(string $projectPath, string $outputPath, array $result): void
    {
        // Override in subclasses if needed
    }

    /**
     * Append a log message to the generator's log file.
     */
    protected function appendLog(string $logPath, string $message): void
    {
        $message = trim($message);
        if ($message === '') {
            return;
        }

        $this->filesystem->appendToFile($logPath, $message . PHP_EOL);
    }

    /**
     * Get current timestamp in ISO 8601 format.
     */
    protected function now(): string
    {
        return (new \DateTimeImmutable())->format(DATE_ATOM);
    }

    /**
     * Check if a directory exists and is readable.
     */
    protected function isDirectoryAccessible(string $path): bool
    {
        return is_dir($path) && is_readable($path);
    }

    /**
     * Check if a file exists and is readable.
     */
    protected function isFileAccessible(string $path): bool
    {
        return is_file($path) && is_readable($path);
    }
}
