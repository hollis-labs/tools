<?php

declare(strict_types=1);

namespace LaravelContextPack\Command;

use LaravelContextPack\Quality\Report\JsonReportFormatter;
use LaravelContextPack\Quality\Report\MarkdownReportFormatter;
use LaravelContextPack\Quality\Report\QualityReportGenerator;
use LaravelContextPack\Quality\Scoring\Metric\GeneratorCoverageMetric;
use LaravelContextPack\Quality\Scoring\Metric\MetadataRichnessMetric;
use LaravelContextPack\Quality\Scoring\Metric\ModelCoverageMetric;
use LaravelContextPack\Quality\Scoring\Metric\ParseAccuracyMetric;
use LaravelContextPack\Quality\Scoring\Metric\RouteCoverageMetric;
use LaravelContextPack\Quality\Scoring\QualityScorer;
use LaravelContextPack\Quality\Validation\ContextValidator;
use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Input\InputArgument;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Input\InputOption;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;

#[AsCommand(
    name: 'quality-score',
    description: 'Calculate quality score for generated context'
)]
final class QualityScoreCommand extends Command
{
    protected function configure(): void
    {
        $this
            ->addArgument(
                'context-file',
                InputArgument::REQUIRED,
                'Path to the context JSON file to score'
            )
            ->addOption(
                'min-score',
                's',
                InputOption::VALUE_REQUIRED,
                'Minimum score threshold (0.0-1.0). Command fails if score is below this.',
                null
            )
            ->addOption(
                'format',
                'f',
                InputOption::VALUE_REQUIRED,
                'Output format: markdown, json',
                'markdown'
            )
            ->addOption(
                'output',
                'o',
                InputOption::VALUE_REQUIRED,
                'Save report to file instead of stdout',
                null
            );
    }

    protected function execute(InputInterface $input, OutputInterface $output): int
    {
        $io = new SymfonyStyle($input, $output);

        $contextFile = (string) $input->getArgument('context-file');
        $minScoreOption = $input->getOption('min-score');
        $format = (string) $input->getOption('format');
        $outputFile = $input->getOption('output');

        if (!file_exists($contextFile)) {
            $io->error("Context file not found: {$contextFile}");

            return Command::FAILURE;
        }

        $io->section('Loading context data');

        $content = file_get_contents($contextFile);

        if ($content === false) {
            $io->error("Failed to read context file: {$contextFile}");

            return Command::FAILURE;
        }

        $context = json_decode($content, true);

        if ($context === null) {
            $io->error('Failed to parse context JSON: ' . json_last_error_msg());

            return Command::FAILURE;
        }

        $io->writeln(sprintf('✓ Loaded context from: <info>%s</info>', $contextFile));

        // Setup validator
        $validator = new ContextValidator();
        // Note: File-based validation rules require path parameters
        // For context-based validation, custom rules can be added here

        // Setup scorer
        $scorer = new QualityScorer();
        $scorer->addMetrics([
            new GeneratorCoverageMetric(),
            new ModelCoverageMetric(),
            new RouteCoverageMetric(),
            new ParseAccuracyMetric(),
            new MetadataRichnessMetric(),
        ]);

        // Generate report
        $io->section('Calculating quality score');

        $reportGenerator = new QualityReportGenerator($validator, $scorer);
        $qualityScore = $reportGenerator->getQualityScore($context);

        $io->writeln(sprintf(
            '  Overall Score: <info>%.1f%%</info> (Grade: <info>%s</info>)',
            $qualityScore->getPercentage(),
            $qualityScore->getGrade()
        ));

        // Check minimum score threshold
        if ($minScoreOption !== null) {
            $minScore = (float) $minScoreOption;

            if ($minScore < 0.0 || $minScore > 1.0) {
                $io->error('Minimum score must be between 0.0 and 1.0');

                return Command::FAILURE;
            }

            if (!$qualityScore->meetsThreshold($minScore)) {
                $io->error(sprintf(
                    'Quality score %.1f%% is below minimum threshold %.1f%%',
                    $qualityScore->getPercentage(),
                    $minScore * 100
                ));

                return Command::FAILURE;
            }

            $io->writeln(sprintf(
                '  ✓ Score meets minimum threshold of %.1f%%',
                $minScore * 100
            ));
        }

        // Format report
        $io->section('Generating report');

        $formatter = match ($format) {
            'json' => new JsonReportFormatter(),
            'markdown' => new MarkdownReportFormatter(),
            default => throw new \InvalidArgumentException("Unsupported format: {$format}"),
        };

        $report = $reportGenerator->generate($context, $formatter);

        // Output report
        if ($outputFile !== null) {
            $outputPath = (string) $outputFile;
            file_put_contents($outputPath, $report);
            $io->success(sprintf('Report saved to: %s', $outputPath));
        } else {
            $io->newLine();
            $output->write($report);
            $io->newLine();
        }

        return Command::SUCCESS;
    }
}
