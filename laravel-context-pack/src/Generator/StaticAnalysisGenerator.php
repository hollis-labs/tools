<?php

declare(strict_types=1);

namespace LaravelContextPack\Generator;

use Symfony\Component\Filesystem\Filesystem;
use Symfony\Component\Process\Process;

final class StaticAnalysisGenerator extends AbstractGenerator
{
    /**
     * @var array<string, array{label: string, command: string}>
     */
    private const TOOL_COMMANDS = [
        'pint' => [
            'label' => 'Laravel Pint',
            'command' => './vendor/bin/pint --test',
        ],
        'phpstan' => [
            'label' => 'PHPStan',
            'command' => './vendor/bin/phpstan analyse --no-progress',
        ],
        'larastan' => [
            'label' => 'Larastan',
            'command' => './vendor/bin/phpstan analyse --no-progress',
        ],
        'pest' => [
            'label' => 'Pest',
            'command' => './vendor/bin/pest --configuration=./phpunit.xml --parallel --max-workers=2',
        ],
        'rector' => [
            'label' => 'Rector',
            'command' => './vendor/bin/rector process --dry-run',
        ],
    ];

    /**
     * @var array<string, array<string, mixed>>
     */
    private array $detectedToolsConfig = [];

    public function __construct(?Filesystem $filesystem = null)
    {
        parent::__construct($filesystem);
    }

    public function getName(): string
    {
        return 'static-analysis';
    }

    public function getDescription(): string
    {
        return 'Aggregate static analysis tool output (PHPStan, Pint, Pest, Rector)';
    }

    /**
     * @return array<string, array<string, mixed>>
     */
    public function getConfigSchema(): array
    {
        return [
            'detected_tools' => [
                'type' => 'array',
                'description' => 'Detected tools configuration',
                'default' => [],
            ],
        ];
    }

    /**
     * Set detected tools configuration.
     *
     * @param array<string, array<string, mixed>> $detectedTools
     */
    public function setDetectedToolsConfig(array $detectedTools): void
    {
        $this->detectedToolsConfig = $detectedTools;
    }

    /**
     * @param  array<string, array<string, mixed>>  $detectedTools Optional for backward compatibility
     * @return array{status: string, message: string, tools: array<string, array{status: string, message: string}>}
     */
    public function generate(string $projectPath, string $outputPath, array $detectedTools = []): array
    {
        // Merge provided detected tools with instance config
        $toolsConfig = array_merge($this->detectedToolsConfig, $detectedTools);

        $logPath = $outputPath . '/logs/static-analysis.log';
        $results = [];

        $availableToolRan = false;
        $availableToolFailed = false;

        foreach (self::TOOL_COMMANDS as $key => $config) {
            $available = (bool) ($toolsConfig[$key]['available'] ?? false);
            if (! $available) {
                $results[$key] = [
                    'status' => 'skipped',
                    'message' => 'Tool not available in target project.',
                    'issues' => null,
                ];

                continue;
            }

            $command = $this->commandForTool($key, $projectPath, $config['command']);
            $this->appendLog($logPath, sprintf('[%s] Running %s: %s', $this->now(), $config['label'], $command));

            $process = Process::fromShellCommandline($command, $projectPath);
            $process->setTimeout(120);
            $process->run();

            $output = trim($process->getOutput());
            $errorOutput = trim($process->getErrorOutput());

            if ($output !== '') {
                $this->appendLog($logPath, $output);
            }

            if ($errorOutput !== '') {
                $this->appendLog($logPath, $errorOutput);
            }

            $issueCount = $this->countIssues($process->getOutput(), $process->getErrorOutput());

            if (! $process->isSuccessful()) {
                $availableToolFailed = true;
                $results[$key] = [
                    'status' => 'failed',
                    'message' => sprintf('%s exited with code %d.', $config['label'], $process->getExitCode() ?? -1),
                    'issues' => $issueCount,
                ];

                continue;
            }

            $availableToolRan = true;
            $results[$key] = [
                'status' => 'success',
                'message' => sprintf('%s completed successfully.', $config['label']),
                'issues' => $issueCount,
            ];
        }

        $summaryStatus = 'skipped';
        $summaryMessage = 'No static analysis tools ran.';

        if ($availableToolRan && ! $availableToolFailed) {
            $summaryStatus = 'success';
            $summaryMessage = 'All available static analysis tools completed successfully.';
        } elseif ($availableToolRan && $availableToolFailed) {
            $summaryStatus = 'partial';
            $summaryMessage = 'Some static analysis tools failed; review logs/static-analysis.log.';
        } elseif ($availableToolFailed) {
            $summaryStatus = 'failed';
            $summaryMessage = 'Static analysis tools failed to run; review logs/static-analysis.log.';
        }

        $this->writeMarkdownSummary($outputPath . '/04-static-analysis.md', $results, $summaryStatus, $summaryMessage);

        return [
            'status' => $summaryStatus,
            'message' => $summaryMessage,
            'tools' => $results,
        ];
    }

    /**
     * @param  array<string, array{status: string, message: string, issues: ?int}>  $results
     */
    private function writeMarkdownSummary(string $path, array $results, string $status, string $message): void
    {
        $lines = [];
        $lines[] = '# Static Analysis Summary';
        $lines[] = '';
        $lines[] = sprintf('**Overall Status:** %s — %s', strtoupper($status), $message);
        $lines[] = '';
        $lines[] = '## Tool Results';
        $lines[] = '| Tool | Status | Issues | Details |';
        $lines[] = '|------|--------|--------|---------|';

        foreach (self::TOOL_COMMANDS as $key => $config) {
            $result = $results[$key] ?? ['status' => 'skipped', 'message' => 'Not evaluated.', 'issues' => null];
            $issues = $result['issues'];
            $issuesCell = $issues === null ? '—' : (string) $issues;
            $lines[] = sprintf(
                '| %s | %s | %s | %s |',
                $this->escapePipe($config['label']),
                strtoupper($result['status']),
                $issuesCell,
                $this->escapePipe($result['message'])
            );
        }

        $lines[] = '';
        $lines[] = '_Logs are available under `logs/static-analysis.log`._';
        $lines[] = '';

        $this->filesystem->dumpFile($path, implode(PHP_EOL, $lines));
    }

    private function countIssues(string $output, string $errorOutput): int
    {
        $content = strtolower($output . PHP_EOL . $errorOutput);

        $patterns = [
            '/\b(error|errors|fail|failed|warning)s?:?\b/',
            '/\b\d+\s+failures?\b/',
            '/\[fail\]/',
        ];

        $count = 0;
        foreach ($patterns as $pattern) {
            if (preg_match_all($pattern, $content, $matches) !== false) {
                $count += count($matches[0]);
            }
        }

        if ($count === 0 && str_contains(strtolower($content), 'error')) {
            $count = 1;
        }

        return $count;
    }

    private function commandForTool(string $toolKey, string $projectPath, string $defaultCommand): string
    {
        if (in_array($toolKey, ['phpstan', 'larastan'], true)) {
            $config = $this->resolvePhpStanConfig($projectPath);
            if ($config !== null) {
                return $defaultCommand . ' --configuration=' . escapeshellarg($config);
            }
        }

        return $defaultCommand;
    }

    private function resolvePhpStanConfig(string $projectPath): ?string
    {
        $candidates = [
            'phpstan.neon',
            'phpstan.neon.dist',
            'phpstan.neon.php',
        ];

        foreach ($candidates as $candidate) {
            if (is_file($projectPath . '/' . $candidate)) {
                return $candidate;
            }
        }

        return null;
    }

    private function escapePipe(string $value): string
    {
        return str_replace('|', '\\|', $value);
    }
}
