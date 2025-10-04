<?php

declare(strict_types=1);

namespace LaravelContextPack\Command;

use LaravelContextPack\Generator\SchemaDumpGenerator;
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

    public function __construct(
        ?Filesystem $filesystem = null,
        ?MetadataDetector $metadataDetector = null,
        ?SchemaDumpGenerator $schemaDumpGenerator = null,
    ) {
        parent::__construct();

        $this->filesystem = $filesystem ?? new Filesystem;
        $this->metadataDetector = $metadataDetector ?? new MetadataDetector;
        $this->schemaDumpGenerator = $schemaDumpGenerator ?? new SchemaDumpGenerator;
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
            );
    }

    protected function execute(InputInterface $input, OutputInterface $output): int
    {
        $io = new SymfonyStyle($input, $output);

        $projectOption = $input->getOption('project');
        $projectPath = $projectOption !== null ? (string) $projectOption : getcwd();
        $projectPath = $this->normalisePath($projectPath ?? '.');

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
        } else {
            $io->section('Generating schema dump');
            $artifactStatuses['schemaDump'] = $this->schemaDumpGenerator->generate($projectPath, $outputPath);

            $io->writeln(sprintf(
                'Schema dump status: <info>%s</info> — %s',
                strtoupper($artifactStatuses['schemaDump']['status']),
                $artifactStatuses['schemaDump']['message'],
            ));
        }

        $metadata['artifacts'] = $artifactStatuses;

        if (! $dryRun) {
            $this->writeJson($outputPath . '/metadata.json', $metadata);
            $this->writeFile($outputPath . '/README.md', $this->buildReadme($metadata, $artifactStatuses));

            foreach (['schema-dump.log', 'route-summary.log', 'static-analysis.log'] as $logFile) {
                $this->ensureLogFile($outputPath . '/logs/' . $logFile);
            }
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
}
