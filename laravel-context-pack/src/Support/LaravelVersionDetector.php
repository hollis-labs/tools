<?php

declare(strict_types=1);

namespace LaravelContextPack\Support;

/**
 * Detects Laravel framework version from composer.json
 */
final class LaravelVersionDetector
{
    /**
     * Detect Laravel version from project's composer.json
     *
     * @return array{version: string, major: int, minor: int}|null
     */
    public static function detect(string $projectPath): ?array
    {
        $composerPath = $projectPath . '/composer.json';

        if (! is_file($composerPath)) {
            return null;
        }

        $contents = file_get_contents($composerPath);
        if ($contents === false) {
            return null;
        }

        $data = json_decode($contents, true);
        if (! is_array($data)) {
            return null;
        }

        // Check require section for laravel/framework
        $version = $data['require']['laravel/framework'] ?? null;

        if ($version === null || ! is_string($version)) {
            return null;
        }

        return self::parseVersion($version);
    }

    /**
     * Parse version constraint to extract major/minor version
     *
     * @return array{version: string, major: int, minor: int}|null
     */
    private static function parseVersion(string $constraint): ?array
    {
        // Remove common constraint prefixes
        $normalized = preg_replace('/^[\^~>=<]+/', '', $constraint);

        if ($normalized === null) {
            return null;
        }

        $normalized = trim($normalized);

        // Extract version parts
        if (preg_match('/^(\d+)\.(\d+)/', $normalized, $matches) === 1) {
            return [
                'version' => $normalized,
                'major' => (int) $matches[1],
                'minor' => (int) $matches[2],
            ];
        }

        return null;
    }

    /**
     * Check if Laravel version is >= specified version
     */
    public static function isVersionOrHigher(string $projectPath, int $major, int $minor = 0): bool
    {
        $detected = self::detect($projectPath);

        if ($detected === null) {
            return false;
        }

        if ($detected['major'] > $major) {
            return true;
        }

        if ($detected['major'] === $major && $detected['minor'] >= $minor) {
            return true;
        }

        return false;
    }

    /**
     * Check if Laravel version is exactly the specified major version
     */
    public static function isMajorVersion(string $projectPath, int $major): bool
    {
        $detected = self::detect($projectPath);

        if ($detected === null) {
            return false;
        }

        return $detected['major'] === $major;
    }
}
