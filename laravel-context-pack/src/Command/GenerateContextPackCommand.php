<?php

declare(strict_types=1);

namespace LaravelContextPack\Command;

use LaravelContextPack\Formatter\JsonFormatter;
use LaravelContextPack\Formatter\MarkdownFormatter;
use LaravelContextPack\Formatter\OutputFormatter;
use LaravelContextPack\Generator\ModelOverviewGenerator;
use LaravelContextPack\Generator\RouteSummaryGenerator;
use LaravelContextPack\Generator\SchemaDumpGenerator;
use LaravelContextPack\Generator\StaticAnalysisGenerator;
use LaravelContextPack\Optimizer\ApproximateTokenCounter;
use LaravelContextPack\Optimizer\ContextOptimizer;
use LaravelContextPack\Optimizer\GeneratorOutput;
use LaravelContextPack\Optimizer\Gpt4TokenCounter;
use LaravelContextPack\Optimizer\Strategy\HierarchicalStrategy;
use LaravelContextPack\Optimizer\Strategy\ProportionalStrategy;
use LaravelContextPack\Optimizer\Strategy\RelevanceStrategy;
use LaravelContextPack\Optimizer\TokenCounter;
use LaravelContextPack\Support\MetadataDetector;
use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Input\InputOption;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;
use Symfony\Component\Filesystem\Filesystem;

#[AsCommand(name: 'generate', description: 'Generate a context pack for a Laravel project')]
final class GenerateContextPackCommand extends Command
{
    private const DEFAULT_OUTPUT_DIR = 'context-pack';

    private Filesystem $filesystem;

    private MetadataDetector $metadataDetector;

    private SchemaDumpGenerator $schemaDumpGenerator;

    private RouteSummaryGenerator $routeSummaryGenerator;

    private ModelOverviewGenerator $modelOverviewGenerator;

    private StaticAnalysisGenerator $staticAnalysisGenerator;

    public function __construct(
        ?Filesystem $filesystem = null,
        ?MetadataDetector $metadataDetector = null,
        ?SchemaDumpGenerator $schemaDumpGenerator = null,
        ?RouteSummaryGenerator $routeSummaryGenerator = null,
        ?ModelOverviewGenerator $modelOverviewGenerator = null,
        ?StaticAnalysisGenerator $staticAnalysisGenerator = null,
    ) {
        parent::__construct();

        $this->filesystem = $filesystem ?? new Filesystem;
        $this->metadataDetector = $metadataDetector ?? new MetadataDetector;
        $this->schemaDumpGenerator = $schemaDumpGenerator ?? new SchemaDumpGenerator;
        $this->routeSummaryGenerator = $routeSummaryGenerator ?? new RouteSummaryGenerator;
        $this->modelOverviewGenerator = $modelOverviewGenerator ?? new ModelOverviewGenerator;
        $this->staticAnalysisGenerator = $staticAnalysisGenerator ?? new StaticAnalysisGenerator;
    }

    protected function configure(): void
    {
        $this
            ->addOption(
                'project',
                mode: InputOption::VALUE_REQUIRED,
                description: 'Path to the Laravel project to analyse'
            )
            ->addOption(
                'output',
                mode: InputOption::VALUE_REQUIRED,
                description: 'Directory where the context pack will be generated'
            )
            ->addOption(
                'force',
                shortcut: 'f',
                mode: InputOption::VALUE_NONE,
                description: 'Overwrite the output directory if it already exists'
            )
            ->addOption(
                'dry-run',
                mode: InputOption::VALUE_NONE,
                description: 'Execute all validation steps without writing any files'
            )
            ->addOption(
                'max-tokens',
                mode: InputOption::VALUE_REQUIRED,
                description: 'Maximum token budget for generated context (default: 200000)'
            )
            ->addOption(
                'optimization-strategy',
                mode: InputOption::VALUE_REQUIRED,
                description: 'Summarization strategy: proportional, hierarchical, relevance (default: none)'
            )
            ->addOption(
                'token-counter',
                mode: InputOption::VALUE_REQUIRED,
                description: 'Token counter to use: approximate, gpt4 (default: approximate)'
            )
            ->addOption(
                'format',
                mode: InputOption::VALUE_REQUIRED,
                description: 'Output format: markdown, json (default: markdown)'
            );
    }

    protected function execute(InputInterface $input, OutputInterface $output): int
    {
        $io = new SymfonyStyle($input, $output);

        $projectOption = $input->getOption('project');
        $projectPath = $projectOption !== null ? (string) $projectOption : (string) getcwd();
        $projectPath = $this->normalisePath($projectPath);

        if ($projectPath === null || ! is_dir($projectPath)) {
            $io->error('The provided project path could not be found.');

            return Command::FAILURE;
        }

        $validationErrors = $this->validateProject($projectPath);
        if ($validationErrors !== []) {
            foreach ($validationErrors as $error) {
                $io->error($error);
            }

            return Command::FAILURE;
        }

        $outputOption = $input->getOption('output');
        $outputPath = $outputOption !== null
            ? $this->normalisePath((string) $outputOption, base: getcwd())
            : $this->normalisePath(self::DEFAULT_OUTPUT_DIR, base: $projectPath);

        if ($outputPath === null) {
            $io->error('Unable to resolve the output directory path.');

            return Command::FAILURE;
        }

        $force = (bool) $input->getOption('force');
        $dryRun = (bool) $input->getOption('dry-run');

        if (is_dir($outputPath) && ! $force && ! $dryRun) {
            $io->error(sprintf('The output directory "%s" already exists. Use --force to overwrite it.', $outputPath));

            return Command::FAILURE;
        }

        $io->section('Preparing context pack workspace');
        if (! $dryRun) {
            if ($force && is_dir($outputPath)) {
                $this->filesystem->remove($outputPath);
            }

            $this->filesystem->mkdir([
                $outputPath,
                $outputPath . '/logs',
            ]);
        }

        $io->writeln(sprintf('✔ Project path: <info>%s</info>', $projectPath));
        $io->writeln(sprintf('✔ Output path: <info>%s</info>', $outputPath));

        $io->section('Collecting metadata');
        $metadata = $this->metadataDetector->detect($projectPath);
        $io->writeln(sprintf('✔ Laravel version detected: <info>%s</info>', $metadata['laravel']['version'] ?? 'unknown'));
        $io->writeln(sprintf('✔ PHP version detected: <info>%s</info>', $metadata['php']['version'] ?? 'unknown'));

        $artifactStatuses = $this->initialArtifactStatuses();

        if ($dryRun) {
            $artifactStatuses['schemaDump'] = [
                'status' => 'skipped',
                'message' => 'Schema dump skipped (dry run).',
            ];
            $artifactStatuses['routeSummary'] = [
                'status' => 'skipped',
                'message' => 'Route summary skipped (dry run).',
            ];
            $artifactStatuses['modelOverview'] = [
                'status' => 'skipped',
                'message' => 'Model overview skipped (dry run).',
            ];
            $artifactStatuses['staticAnalysis'] = [
                'status' => 'skipped',
                'message' => 'Static analysis skipped (dry run).',
            ];
        } else {
            $io->section('Generating schema dump');
            $artifactStatuses['schemaDump'] = $this->schemaDumpGenerator->generate(
                $projectPath,
                $outputPath,
                $metadata['database'] ?? [],
            );

            $io->writeln(sprintf(
                'Schema dump status: <info>%s</info> — %s',
                strtoupper($artifactStatuses['schemaDump']['status']),
                $artifactStatuses['schemaDump']['message'],
            ));

            $io->section('Generating route summary');
            $artifactStatuses['routeSummary'] = $this->routeSummaryGenerator->generate($projectPath, $outputPath);

            $io->writeln(sprintf(
                'Route summary status: <info>%s</info> — %s',
                strtoupper($artifactStatuses['routeSummary']['status']),
                $artifactStatuses['routeSummary']['message'],
            ));

            $io->section('Generating model overview');
            $artifactStatuses['modelOverview'] = $this->modelOverviewGenerator->generate($projectPath, $outputPath);

            $io->writeln(sprintf(
                'Model overview status: <info>%s</info> — %s',
                strtoupper($artifactStatuses['modelOverview']['status']),
                $artifactStatuses['modelOverview']['message'],
            ));

            $io->section('Running static analysis');
            $artifactStatuses['staticAnalysis'] = $this->staticAnalysisGenerator->generate(
                $projectPath,
                $outputPath,
                $metadata['detectedTools'] ?? []
            );

            $io->writeln(sprintf(
                'Static analysis status: <info>%s</info> — %s',
                strtoupper($artifactStatuses['staticAnalysis']['status']),
                $artifactStatuses['staticAnalysis']['message'],
            ));
        }

        $metadata['artifacts'] = $artifactStatuses;

        if (! $dryRun) {
            $this->writeJson($outputPath . '/metadata.json', $metadata);
            $this->writeFile($outputPath . '/README.md', $this->buildReadme($metadata, $artifactStatuses));

            foreach (['schema-dump.log', 'route-summary.log', 'model-overview.log', 'static-analysis.log'] as $logFile) {
                $this->ensureLogFile($outputPath . '/logs/' . $logFile);
            }
        }

        // Apply optimization and formatting if requested
        $maxTokensOption = $input->getOption('max-tokens');
        $strategyOption = $input->getOption('optimization-strategy');
        $formatOption = $input->getOption('format');

        if ($maxTokensOption !== null || $strategyOption !== null || $formatOption !== null) {
            $this->applyOptimizationAndFormatting(
                $io,
                $dryRun,
                $outputPath,
                $artifactStatuses,
                $maxTokensOption !== null ? (int) $maxTokensOption : null,
                $strategyOption !== null ? (string) $strategyOption : null,
                $input->getOption('token-counter') !== null ? (string) $input->getOption('token-counter') : 'approximate',
                $formatOption !== null ? (string) $formatOption : 'markdown'
            );
        }

        $io->success('Workspace prepared. Ready for schema, route, and analysis generators.');

        return Command::SUCCESS;
    }

    /**
     * @return list<string>
     */
    private function validateProject(string $projectPath): array
    {
        $errors = [];

        if (! is_file($projectPath . '/artisan')) {
            $errors[] = 'Expected artisan executable not found in project root.';
        }

        if (! is_file($projectPath . '/composer.json')) {
            $errors[] = 'composer.json not found; ensure this is a Laravel application root.';
        }

        if ((new \Symfony\Component\Process\ExecutableFinder)->find('php') === null) {
            $errors[] = 'PHP executable not found in PATH. Install PHP 8.2+ before running the generator.';
        }

        return $errors;
    }

    private function normalisePath(string $path, ?string $base = null): ?string
    {
        if ($base !== null && ! $this->isAbsolutePath($path)) {
            $path = rtrim($base, DIRECTORY_SEPARATOR) . DIRECTORY_SEPARATOR . $path;
        }

        $realPath = realpath($path);
        if ($realPath !== false) {
            return $realPath;
        }

        if ($this->isAbsolutePath($path)) {
            return $path;
        }

        return $base !== null ? rtrim($base, DIRECTORY_SEPARATOR) . DIRECTORY_SEPARATOR . $path : null;
    }

    private function isAbsolutePath(string $path): bool
    {
        return str_starts_with($path, DIRECTORY_SEPARATOR)
            || (bool) preg_match('/^[A-Za-z]:\\\\/', $path);
    }

    /**
     * @param  array<string, mixed>  $payload
     */
    private function writeJson(string $path, array $payload): void
    {
        $json = json_encode($payload, JSON_PRETTY_PRINT | JSON_UNESCAPED_SLASHES);

        if ($json === false) {
            throw new \RuntimeException('Unable to encode metadata payload as JSON.');
        }

        $this->writeFile($path, $json . PHP_EOL);
    }

    private function writeFile(string $path, string $contents): void
    {
        $this->filesystem->dumpFile($path, $contents);
    }

    private function ensureLogFile(string $path): void
    {
        if (is_file($path)) {
            return;
        }

        $this->writeFile($path, sprintf("[%s] log initialised\n", (new \DateTimeImmutable)->format(DATE_ATOM)));
    }

    /**
     * @param  array<string, array{status: string, message: string}>  $artifactStatuses
     */
    /**
     * @param  array<string, mixed>  $metadata
     * @param  array<string, array{status: string, message: string, tools?: array<string, array{status: string, message: string}>}>  $artifactStatuses
     */
    private function buildReadme(array $metadata, array $artifactStatuses): string
    {
        $lines = [];
        $lines[] = '# Laravel Context Pack';
        $lines[] = '';
        $lines[] = sprintf('*Generated at:* %s', $metadata['generatedAt'] ?? (new \DateTimeImmutable)->format(DATE_ATOM));
        $lines[] = sprintf('*Project:* `%s`', $metadata['project']['name'] ?? 'unknown');
        $lines[] = '';
        $lines[] = '## Artefacts';
        $lines[] = '- `metadata.json` — captured environment metadata for downstream generators';
        $lines[] = sprintf('- `01-schema-dump.sql` — %s (%s)', $artifactStatuses['schemaDump']['status'], $artifactStatuses['schemaDump']['message']);
        $lines[] = sprintf('- `02-route-summary.md` — %s', $artifactStatuses['routeSummary']['status']);
        $lines[] = sprintf('- `03-model-overview.md` — %s', $artifactStatuses['modelOverview']['status']);
        $lines[] = sprintf('- `04-static-analysis.md` — %s', $artifactStatuses['staticAnalysis']['status']);
        $lines[] = '- `logs/` — raw command output for debugging';
        $lines[] = '';
        $lines[] = '## Next Steps';
        $lines[] = '- Run schema and route generators once implemented.';
        $lines[] = '- Review `metadata.json` for accuracy; report discrepancies.';

        return implode(PHP_EOL, $lines) . PHP_EOL;
    }

    /**
     * @return array<string, array{status: string, message: string}>
     */
    private function initialArtifactStatuses(): array
    {
        return [
            'schemaDump' => ['status' => 'pending', 'message' => 'Awaiting generation.'],
            'routeSummary' => ['status' => 'pending', 'message' => 'Awaiting implementation.'],
            'modelOverview' => ['status' => 'pending', 'message' => 'Awaiting implementation.'],
            'staticAnalysis' => ['status' => 'pending', 'message' => 'Awaiting implementation.'],
        ];
    }

    /**
     * Apply optimization and formatting to generated outputs.
     *
     * @param array<string, array{status: string, message: string}> $artifactStatuses
     */
    private function applyOptimizationAndFormatting(
        SymfonyStyle $io,
        bool $dryRun,
        string $outputPath,
        array $artifactStatuses,
        ?int $maxTokens,
        ?string $strategyName,
        string $counterName,
        string $formatName
    ): void {
        if ($dryRun) {
            return;
        }

        $io->section('Applying Optimization & Formatting');

        // Create token counter
        $counter = $this->createTokenCounter($counterName);
        $io->writeln(sprintf('Token counter: <info>%s</info>', $counter->getName()));

        // Create strategy if specified
        $strategy = $strategyName !== null ? $this->createStrategy($strategyName) : null;
        if ($strategy !== null) {
            $io->writeln(sprintf('Optimization strategy: <info>%s</info>', $strategy->getName()));
        }

        // Create optimizer
        $optimizer = new ContextOptimizer(
            $counter,
            $strategy,
            $maxTokens ?? 200000
        );

        if ($maxTokens !== null) {
            $io->writeln(sprintf('Token budget: <info>%s</info>', number_format($maxTokens)));
        }

        // Collect generated outputs
        $outputs = $this->collectGeneratorOutputs($outputPath, $artifactStatuses);

        if ($outputs === []) {
            $io->warning('No outputs available for optimization.');
            return;
        }

        // Optimize
        $optimizedContext = $optimizer->optimize($outputs);

        // Display token usage
        $io->writeln(sprintf(
            'Original tokens: <info>%s</info> → Optimized tokens: <info>%s</info>',
            number_format($optimizedContext->metadata['originalTokens']),
            number_format($optimizedContext->metadata['optimizedTokens'])
        ));

        if ($optimizedContext->wasTruncated()) {
            $saved = $optimizedContext->metadata['originalTokens'] - $optimizedContext->metadata['optimizedTokens'];
            $percentage = ($saved / $optimizedContext->metadata['originalTokens']) * 100;
            $io->writeln(sprintf('<comment>Tokens saved: %s (%.1f%%)</comment>', number_format($saved), $percentage));
        }

        // Format and write output
        $formatter = $this->createFormatter($formatName);
        $io->writeln(sprintf('Output format: <info>%s</info>', $formatter->getName()));

        $formattedOutput = $formatter->format($optimizedContext, [
            'generatedAt' => date('c'),
        ]);

        $outputFile = $outputPath . '/optimized-context.' . $formatter->getExtension();
        $this->writeFile($outputFile, $formattedOutput);

        $io->writeln(sprintf('✔ Optimized output written to: <info>%s</info>', $outputFile));
    }

    /**
     * Create token counter from name.
     */
    private function createTokenCounter(string $name): TokenCounter
    {
        return match ($name) {
            'gpt4' => new Gpt4TokenCounter(),
            'approximate' => new ApproximateTokenCounter(),
            default => new ApproximateTokenCounter(),
        };
    }

    /**
     * Create summarization strategy from name.
     */
    private function createStrategy(string $name): ?\LaravelContextPack\Optimizer\SummarizationStrategy
    {
        return match ($name) {
            'proportional' => new ProportionalStrategy(),
            'hierarchical' => new HierarchicalStrategy(),
            'relevance' => new RelevanceStrategy(),
            default => null,
        };
    }

    /**
     * Create output formatter from name.
     */
    private function createFormatter(string $name): OutputFormatter
    {
        return match ($name) {
            'json' => new JsonFormatter(),
            'markdown' => new MarkdownFormatter(),
            default => new MarkdownFormatter(),
        };
    }

    /**
     * Collect generator outputs from files.
     *
     * @param array<string, array{status: string, message: string}> $artifactStatuses
     * @return list<GeneratorOutput>
     */
    private function collectGeneratorOutputs(string $outputPath, array $artifactStatuses): array
    {
        $outputs = [];
        $fileMap = [
            'schemaDump' => '01-schema-dump.sql',
            'routeSummary' => '02-route-summary.md',
            'modelOverview' => '03-model-overview.md',
            'staticAnalysis' => '04-static-analysis.md',
        ];

        foreach ($fileMap as $generatorName => $fileName) {
            $filePath = $outputPath . '/' . $fileName;

            if (! is_file($filePath)) {
                continue;
            }

            $content = file_get_contents($filePath);
            if ($content === false) {
                continue;
            }

            // Calculate relevance score based on status
            $status = $artifactStatuses[$generatorName] ?? ['status' => 'unknown'];
            $relevanceScore = match ($status['status']) {
                'success' => 1.0,
                'skipped' => 0.5,
                default => 0.8,
            };

            $outputs[] = new GeneratorOutput(
                generatorName: $generatorName,
                content: $content,
                relevanceScore: $relevanceScore,
                metadata: [
                    'fileName' => $fileName,
                    'status' => $status['status'],
                ]
            );
        }

        return $outputs;
    }
}
