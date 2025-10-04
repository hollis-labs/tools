<?php

declare(strict_types=1);

namespace LaravelContextPack\Support;

use DateTimeImmutable;
use RuntimeException;
use Symfony\Component\Filesystem\Filesystem;
use Symfony\Component\Process\Exception\ProcessFailedException;
use Symfony\Component\Process\Process;

final class MetadataDetector
{
    private Filesystem $filesystem;

    public function __construct(?Filesystem $filesystem = null)
    {
        $this->filesystem = $filesystem ?? new Filesystem;
    }

    /**
     * @return array<string, mixed>
     */
    public function detect(string $projectPath): array
    {
        $projectPath = rtrim($projectPath, DIRECTORY_SEPARATOR);
        $composer = $this->readJson($projectPath . '/composer.json');
        $composerLock = $this->readJsonIfExists($projectPath . '/composer.lock');

        $projectName = $composer['name'] ?? basename($projectPath);
        $packages = $this->collectPackages($composerLock);

        $metadata = [
            'generatedAt' => (new DateTimeImmutable)->format(DATE_ATOM),
            'project' => [
                'name' => $projectName,
                'path' => $projectPath,
            ],
            'laravel' => [
                'version' => $this->resolveLaravelVersion($composerLock, $projectPath),
                'artisanVersion' => $this->detectArtisanVersion($projectPath),
            ],
            'php' => [
                'version' => PHP_VERSION,
            ],
            'database' => [
                'driver' => $this->detectDatabaseDriver($projectPath),
            ],
            'detectedTools' => $this->detectTooling($packages, $projectPath),
            'packages' => $packages,
        ];

        return $metadata;
    }

    /**
     * @return array<string, mixed>
     */
    private function readJson(string $path): array
    {
        if (! $this->filesystem->exists($path)) {
            throw new RuntimeException(sprintf('Expected file not found: %s', $path));
        }

        $contents = file_get_contents($path);
        $decoded = json_decode($contents ?: 'null', true);
        if (! is_array($decoded)) {
            throw new RuntimeException(sprintf('Unable to parse JSON file: %s', $path));
        }

        return $decoded;
    }

    /**
     * @return array<string, mixed>
     */
    private function readJsonIfExists(string $path): array
    {
        if (! $this->filesystem->exists($path)) {
            return [];
        }

        $contents = file_get_contents($path);
        $decoded = json_decode($contents ?: 'null', true);
        if (! is_array($decoded)) {
            return [];
        }

        return $decoded;
    }

    /**
     * @return array<string, string>
     */
    private function collectPackages(array $composerLock): array
    {
        $packages = [];
        $locked = array_merge(
            $composerLock['packages'] ?? [],
            $composerLock['packages-dev'] ?? [],
        );

        foreach ($locked as $package) {
            if (! isset($package['name'], $package['version'])) {
                continue;
            }

            $packages[(string) $package['name']] = (string) $package['version'];
        }

        return $packages;
    }

    private function resolveLaravelVersion(array $composerLock, string $projectPath): ?string
    {
        $packages = $this->collectPackages($composerLock);
        if (isset($packages['laravel/framework'])) {
            return $packages['laravel/framework'];
        }

        if (isset($packages['laravel/lumen-framework'])) {
            return $packages['laravel/lumen-framework'];
        }

        $artisan = $this->detectArtisanVersion($projectPath);
        if ($artisan !== null) {
            return $artisan;
        }

        return null;
    }

    private function detectArtisanVersion(string $projectPath): ?string
    {
        $artisanPath = $projectPath . DIRECTORY_SEPARATOR . 'artisan';
        if (! is_file($artisanPath)) {
            return null;
        }

        $process = Process::fromShellCommandline('php artisan --version', $projectPath);
        $process->setTimeout(10);

        try {
            $process->run();
        } catch (ProcessFailedException) {
            return null;
        } catch (\Throwable) {
            return null;
        }

        if (! $process->isSuccessful()) {
            return null;
        }

        $output = trim($process->getOutput());
        if ($output === '') {
            return null;
        }

        return $output;
    }

    private function detectDatabaseDriver(string $projectPath): ?string
    {
        $envPath = $projectPath . '/.env';
        if (! is_file($envPath)) {
            return null;
        }

        $contents = file_get_contents($envPath) ?: '';
        if (preg_match('/^DB_CONNECTION=([^\n\r]+)/m', $contents, $matches) === 1) {
            return trim($matches[1]);
        }

        return null;
    }

    /**
     * @param  array<string, string>  $packages
     * @return array<string, array<string, mixed>>
     */
    private function detectTooling(array $packages, string $projectPath): array
    {
        return [
            'pint' => [
                'available' => isset($packages['laravel/pint']),
            ],
            'phpstan' => [
                'available' => isset($packages['phpstan/phpstan']) || isset($packages['nunomaduro/larastan']),
            ],
            'larastan' => [
                'available' => isset($packages['nunomaduro/larastan']),
            ],
            'pest' => [
                'available' => isset($packages['pestphp/pest']) || $this->filesystem->exists($projectPath . '/tests/Pest.php'),
            ],
            'rector' => [
                'available' => isset($packages['rector/rector']),
            ],
        ];
    }
}
