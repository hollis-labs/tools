<?php

declare(strict_types=1);

namespace LaravelContextPack\Command;

use LaravelContextPack\Quality\Benchmark\QualityBenchmark;
use LaravelContextPack\Quality\Scoring\Metric\GeneratorCoverageMetric;
use LaravelContextPack\Quality\Scoring\Metric\MetadataRichnessMetric;
use LaravelContextPack\Quality\Scoring\Metric\ModelCoverageMetric;
use LaravelContextPack\Quality\Scoring\Metric\ParseAccuracyMetric;
use LaravelContextPack\Quality\Scoring\Metric\RouteCoverageMetric;
use LaravelContextPack\Quality\Scoring\QualityScorer;
use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Input\InputArgument;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Input\InputOption;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;

#[AsCommand(
    name: 'benchmark',
    description: 'Run quality benchmarks and detect regressions'
)]
final class BenchmarkCommand extends Command
{
    protected function configure(): void
    {
        $this
            ->addArgument(
                'context-file',
                InputArgument::REQUIRED,
                'Path to the context JSON file to benchmark'
            )
            ->addOption(
                'baseline',
                'b',
                InputOption::VALUE_REQUIRED,
                'Path to baseline benchmark file',
                'quality-baseline.json'
            )
            ->addOption(
                'save',
                null,
                InputOption::VALUE_NONE,
                'Save current score as new baseline'
            )
            ->addOption(
                'compare',
                'c',
                InputOption::VALUE_NONE,
                'Compare against baseline (default if baseline exists)'
            );
    }

    protected function execute(InputInterface $input, OutputInterface $output): int
    {
        $io = new SymfonyStyle($input, $output);

        $contextFile = (string) $input->getArgument('context-file');
        $baselinePath = (string) $input->getOption('baseline');
        $save = (bool) $input->getOption('save');
        $compare = (bool) $input->getOption('compare');

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

        // Setup scorer
        $scorer = new QualityScorer();
        $scorer->addMetrics([
            new GeneratorCoverageMetric(),
            new ModelCoverageMetric(),
            new RouteCoverageMetric(),
            new ParseAccuracyMetric(),
            new MetadataRichnessMetric(),
        ]);

        $io->section('Calculating quality score');

        $currentScore = $scorer->score($context);

        $io->writeln(sprintf(
            '  Current Score: <info>%.1f%%</info> (Grade: <info>%s</info>)',
            $currentScore->getPercentage(),
            $currentScore->getGrade()
        ));

        $benchmark = new QualityBenchmark();

        // Save baseline if requested
        if ($save) {
            $io->section('Saving baseline');

            $benchmark->saveBaseline($currentScore, $baselinePath);

            $io->success(sprintf('Baseline saved to: %s', $baselinePath));

            return Command::SUCCESS;
        }

        // Compare with baseline
        $baselineExists = file_exists($baselinePath);

        if ($compare || $baselineExists) {
            if (!$baselineExists) {
                $io->warning(sprintf('Baseline file not found: %s', $baselinePath));
                $io->note('Run with --save to create a baseline');

                return Command::FAILURE;
            }

            $io->section('Loading baseline');

            try {
                $baseline = $benchmark->loadBaseline($baselinePath);

                $io->writeln(sprintf(
                    '  ✓ Loaded baseline from: <info>%s</info>',
                    $baselinePath
                ));

                if (isset($baseline['timestamp'])) {
                    $io->writeln(sprintf(
                        '  Baseline created: <info>%s</info>',
                        $baseline['timestamp']
                    ));
                }

                $io->section('Comparing with baseline');

                $comparison = $benchmark->compare($currentScore, $baseline);

                $io->writeln(sprintf(
                    '  Baseline Score: <info>%.1f%%</info>',
                    $comparison->getBaselineScore() * 100
                ));

                $io->writeln(sprintf(
                    '  Current Score: <info>%.1f%%</info>',
                    $comparison->getCurrentScore() * 100
                ));

                $io->writeln(sprintf(
                    '  Change: %s%.1f%%',
                    $comparison->getDifference() >= 0 ? '+' : '',
                    $comparison->getPercentageChange()
                ));

                $io->newLine();

                // Display metric comparisons
                $io->writeln('<comment>Metric Breakdown:</comment>');

                foreach ($comparison->getMetricComparisons() as $metricName => $metricComparison) {
                    $symbol = match (true) {
                        $metricComparison['has_regression'] => '✗',
                        $metricComparison['difference'] > 0.05 => '↑',
                        $metricComparison['difference'] < -0.01 => '↓',
                        default => '→',
                    };

                    $style = match (true) {
                        $metricComparison['has_regression'] => 'error',
                        $metricComparison['difference'] > 0.05 => 'info',
                        default => 'comment',
                    };

                    $io->writeln(sprintf(
                        '  %s <%s>%s</%s>: %.1f%% → %.1f%% (%s%.1f%%)',
                        $symbol,
                        $style,
                        str_replace('_', ' ', $metricName),
                        $style,
                        $metricComparison['baseline'] * 100,
                        $metricComparison['current'] * 100,
                        $metricComparison['difference'] >= 0 ? '+' : '',
                        $metricComparison['percentage_change']
                    ));
                }

                $io->newLine();

                // Final result
                if ($comparison->hasRegression()) {
                    $io->error($comparison->getSummary());

                    $regressedMetrics = $comparison->getRegressedMetrics();
                    if (count($regressedMetrics) > 0) {
                        $io->warning(sprintf(
                            'Regressed metrics: %s',
                            implode(', ', $regressedMetrics)
                        ));
                    }

                    return Command::FAILURE;
                } elseif ($comparison->hasImprovement()) {
                    $io->success($comparison->getSummary());

                    return Command::SUCCESS;
                } else {
                    $io->info($comparison->getSummary());

                    return Command::SUCCESS;
                }
            } catch (\Exception $e) {
                $io->error('Failed to compare with baseline: ' . $e->getMessage());

                return Command::FAILURE;
            }
        }

        // No baseline, just show current score
        $io->note('No baseline to compare against. Use --save to create one or --compare to use existing baseline.');

        return Command::SUCCESS;
    }
}
