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
            'database' => $this->detectDatabaseInfo($projectPath, $composerLock),
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

    /**
     * @return array<string, mixed>
     */
    private function detectDatabaseInfo(string $projectPath, array $composerLock): array
    {
        $env = $this->parseEnvFile($projectPath . '/.env');

        $driver = $env['DB_CONNECTION'] ?? null;
        $database = $env['DB_DATABASE'] ?? null;
        $host = $env['DB_HOST'] ?? null;
        $port = $env['DB_PORT'] ?? null;
        $username = $env['DB_USERNAME'] ?? null;
        $schema = $env['DB_SCHEMA'] ?? null;

        if ($driver === null) {
            $driver = $this->resolveDriverFallback($composerLock);
        }

        return [
            'driver' => $driver,
            'database' => $database,
            'host' => $host,
            'port' => $port,
            'username' => $username,
            'schema' => $schema,
            'hasPassword' => isset($env['DB_PASSWORD']) && $env['DB_PASSWORD'] !== '',
        ];
    }

    /**
     * @return array<string, string>
     */
    private function parseEnvFile(string $path): array
    {
        if (! is_file($path)) {
            return [];
        }

        $lines = preg_split('/\r?\n/', (string) file_get_contents($path)) ?: [];
        $values = [];

        foreach ($lines as $line) {
            $line = trim($line);
            if ($line === '' || str_starts_with($line, '#')) {
                continue;
            }

            if (strpos($line, '=') === false) {
                continue;
            }

            [$key, $value] = explode('=', $line, 2);
            $key = trim($key);
            $value = trim($value);

            if ($key === '') {
                continue;
            }

            if ($value !== '') {
                $value = trim($value, "'\"");
            }

            $values[$key] = $value;
        }

        return $values;
    }

    private function resolveDriverFallback(array $composerLock): ?string
    {
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
