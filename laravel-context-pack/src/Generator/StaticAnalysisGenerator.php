<?php

declare(strict_types=1);

namespace LaravelContextPack\Generator;

use Symfony\Component\Filesystem\Filesystem;
use Symfony\Component\Process\Process;

final class StaticAnalysisGenerator
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

    private Filesystem $filesystem;

    public function __construct(?Filesystem $filesystem = null)
    {
        $this->filesystem = $filesystem ?? new Filesystem;
    }

    /**
     * @param  array<string, array<string, mixed>>  $detectedTools
     * @return array{status: string, message: string, tools: array<string, array{status: string, message: string}>}
     */
    public function generate(string $projectPath, string $outputPath, array $detectedTools): array
    {
        $logPath = $outputPath . '/logs/static-analysis.log';
        $results = [];

        $availableToolRan = false;
        $availableToolFailed = false;

        foreach (self::TOOL_COMMANDS as $key => $config) {
            $available = (bool) ($detectedTools[$key]['available'] ?? false);
            if (! $available) {
                $results[$key] = [
                    'status' => 'skipped',
                    'message' => 'Tool not available in target project.',
                ];

                continue;
            }

            $command = $config['command'];
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

            if (! $process->isSuccessful()) {
                $availableToolFailed = true;
                $results[$key] = [
                    'status' => 'failed',
                    'message' => sprintf('%s exited with code %d.', $config['label'], $process->getExitCode() ?? -1),
                ];

                continue;
            }

            $availableToolRan = true;
            $results[$key] = [
                'status' => 'success',
                'message' => sprintf('%s completed successfully.', $config['label']),
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
     * @param  array<string, array{status: string, message: string}>  $results
     */
    private function writeMarkdownSummary(string $path, array $results, string $status, string $message): void
    {
        $lines = [];
        $lines[] = '# Static Analysis Summary';
        $lines[] = '';
        $lines[] = sprintf('**Overall Status:** %s — %s', strtoupper($status), $message);
        $lines[] = '';
        $lines[] = '## Tool Results';
        $lines[] = '| Tool | Status | Details |';
        $lines[] = '|------|--------|---------|';

        foreach (self::TOOL_COMMANDS as $key => $config) {
            $result = $results[$key] ?? ['status' => 'skipped', 'message' => 'Not evaluated.'];
            $lines[] = sprintf(
                '| %s | %s | %s |',
                $this->escapePipe($config['label']),
                strtoupper($result['status']),
                $this->escapePipe($result['message'])
            );
        }

        $lines[] = '';
        $lines[] = '_Logs are available under `logs/static-analysis.log`._';
        $lines[] = '';

        $this->filesystem->dumpFile($path, implode(PHP_EOL, $lines));
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
