<?php

declare(strict_types=1);

namespace LaravelContextPack\Generator;

use Symfony\Component\Filesystem\Filesystem;
use Symfony\Component\Process\Process;

final class SchemaDumpGenerator
{
    private const ARTISAN_COMMAND = 'php artisan schema:dump --no-interaction --prune --schema-path=%s';

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

            return [
                'status' => 'failed',
                'message' => 'Schema dump command failed. Review logs/schema-dump.log.',
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

    private function appendLog(string $logPath, string $message): void
    {
        $this->filesystem->appendToFile($logPath, trim($message) . PHP_EOL);
    }

    private function now(): string
    {
        return (new \DateTimeImmutable)->format(DATE_ATOM);
    }
}
