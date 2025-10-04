<?php

declare(strict_types=1);

namespace LaravelContextPack\Generator;

use Symfony\Component\Filesystem\Filesystem;
use Symfony\Component\Finder\Finder;

final class ModelOverviewGenerator
{
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
        $logPath = $outputPath . '/logs/model-overview.log';
        $modelsPath = $projectPath . '/app/Models';

        if (! is_dir($modelsPath)) {
            $this->appendLog($logPath, sprintf('[%s] Models directory not found.', $this->now()));

            $this->filesystem->dumpFile(
                $outputPath . '/03-model-overview.md',
                "# Model Overview\n\n_No models detected._\n"
            );

            return [
                'status' => 'skipped',
                'message' => 'Models directory not found; overview skipped.',
            ];
        }

        $finder = (new Finder)
            ->files()
            ->in($modelsPath)
            ->name('*.php');

        $models = [];
        foreach ($finder as $file) {
            $parsed = $this->parseModelFile($file->getContents(), $file->getRelativePathname());
            if ($parsed !== null) {
                $models[] = $parsed;
            }
        }

        if ($models === []) {
            $this->appendLog($logPath, sprintf('[%s] No models parsed.', $this->now()));

            $this->filesystem->dumpFile(
                $outputPath . '/03-model-overview.md',
                "# Model Overview\n\n_No models detected._\n"
            );

            return [
                'status' => 'skipped',
                'message' => 'No model classes detected.',
            ];
        }

        $markdown = $this->buildMarkdown($models);
        $this->filesystem->dumpFile($outputPath . '/03-model-overview.md', $markdown);
        $this->appendLog($logPath, sprintf('[%s] Model overview generated for %d model(s).', $this->now(), count($models)));

        return [
            'status' => 'success',
            'message' => sprintf('Model overview generated for %d model(s).', count($models)),
        ];
    }

    /**
     * @param  list<array<string, mixed>>  $models
     */
    private function buildMarkdown(array $models): string
    {
        $totalModels = count($models);
        $relationshipCount = 0;
        $methodBreakdown = [];
        foreach ($models as $model) {
            foreach ($model['relationships'] as $relationship) {
                $relationshipCount++;
                $type = strtoupper((string) $relationship['type']);
                $methodBreakdown[$type] = ($methodBreakdown[$type] ?? 0) + 1;
            }
        }

        $lines = [];
        $lines[] = '# Model Overview';
        $lines[] = '';
        $lines[] = '## Summary';
        $lines[] = sprintf('- Models scanned: %d', $totalModels);
        $lines[] = sprintf('- Relationships detected: %d', $relationshipCount);
        if ($methodBreakdown !== []) {
            foreach ($methodBreakdown as $type => $count) {
                $lines[] = sprintf('  - %s: %d', $type, $count);
            }
        }
        $lines[] = '';

        foreach ($models as $model) {
            $lines[] = sprintf('## %s', $model['fqn']);
            $lines[] = '';
            $lines[] = sprintf('- File: `%s`', $model['file']);
            $lines[] = sprintf('- Table: `%s`', $model['table'] ?? 'not specified');
            $lines[] = sprintf('- Fillable: %s', $model['fillable'] !== [] ? '`' . implode('`, `', $model['fillable']) . '`' : 'not specified');
            $lines[] = sprintf('- Guarded: %s', $model['guarded'] !== [] ? '`' . implode('`, `', $model['guarded']) . '`' : 'not specified');

            if ($model['relationships'] !== []) {
                $lines[] = '';
                $lines[] = '| Relationship | Target | Method |';
                $lines[] = '|-------------|--------|--------|';
                foreach ($model['relationships'] as $relationship) {
                    $lines[] = sprintf(
                        '| %s | %s | %s |',
                        strtoupper((string) $relationship['type']),
                        $this->escapePipe((string) $relationship['target']),
                        $this->escapePipe((string) $relationship['name'])
                    );
                }
            }

            $lines[] = '';
        }

        return implode(PHP_EOL, $lines) . PHP_EOL;
    }

    /**
     * @return array<string, mixed>|null
     */
    private function parseModelFile(string $contents, string $relativePath): ?array
    {
        if ($contents === '') {
            return null;
        }

        $namespace = null;
        if (preg_match('/namespace\s+([^;]+);/m', $contents, $matches) === 1) {
            $namespace = trim($matches[1]);
        }

        if (preg_match('/class\s+(\w+)/', $contents, $matches) !== 1) {
            return null;
        }

        $class = $matches[1];
        $fqn = $namespace !== null ? $namespace . '\\' . $class : $class;
        $table = $this->matchPropertyArray($contents, 'table');
        $fillable = $this->extractArrayValues($contents, 'fillable');
        $guarded = $this->extractArrayValues($contents, 'guarded');
        $relationships = $this->extractRelationships($contents);

        return [
            'fqn' => $fqn,
            'file' => $relativePath,
            'table' => $table,
            'fillable' => $fillable,
            'guarded' => $guarded,
            'relationships' => $relationships,
        ];
    }

    /**
     * @return list<string>
     */
    private function extractArrayValues(string $contents, string $property): array
    {
        $pattern = sprintf('/protected\s+\$%s\s*=\s*\[(.*?)\];/s', preg_quote($property, '/'));
        if (preg_match($pattern, $contents, $matches) !== 1) {
            return [];
        }

        $body = $matches[1];
        preg_match_all('/[\'\"]([^\'\"]+)[\'\"]/m', $body, $valueMatches);

        return array_map('trim', $valueMatches[1] ?? []);
    }

    private function matchPropertyArray(string $contents, string $property): ?string
    {
        $pattern = sprintf('/protected\s+\$%s\s*=\s*[\'\"]([^\'\"]+)[\'\"];/', preg_quote($property, '/'));
        if (preg_match($pattern, $contents, $matches) !== 1) {
            return null;
        }

        return trim($matches[1]);
    }

    /**
     * @return list<array{name: string, type: string, target: string}>
     */
    private function extractRelationships(string $contents): array
    {
        $relationships = [];
        $pattern = '/function\s+(\w+)\s*\([^)]*\)\s*\{([^{}]*?return\s+\$this->(hasOne|hasMany|belongsToMany|belongsTo|morphTo|morphMany|morphOne|morphToMany|hasOneThrough|hasManyThrough)\([^;]+;)/s';
        if (preg_match_all($pattern, $contents, $matches, PREG_SET_ORDER) === false) {
            return $relationships;
        }

        foreach ($matches as $match) {
            $methodName = $match[1];
            $type = $match[3];

            if (preg_match('/return\s+\$this->' . $type . '\(([^\)]*)\)/', $match[2], $targetMatches) === 1) {
                $raw = trim($targetMatches[1]);
                $target = $this->normaliseRelationshipTarget($raw);
            } else {
                $target = 'unknown';
            }

            $relationships[] = [
                'name' => $methodName,
                'type' => $type,
                'target' => $target,
            ];
        }

        return $relationships;
    }

    private function normaliseRelationshipTarget(string $raw): string
    {
        $raw = trim($raw, "'\" ");
        if (str_ends_with($raw, '::class')) {
            return str_replace('::class', '', $raw);
        }

        return $raw;
    }

    private function appendLog(string $logPath, string $message): void
    {
        $message = trim($message);
        if ($message === '') {
            return;
        }

        $this->filesystem->appendToFile($logPath, $message . PHP_EOL);
    }

    private function escapePipe(string $value): string
    {
        return str_replace('|', '\\|', $value);
    }

    private function now(): string
    {
        return (new \DateTimeImmutable)->format(DATE_ATOM);
    }
}
