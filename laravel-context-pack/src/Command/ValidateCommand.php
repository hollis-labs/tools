<?php

declare(strict_types=1);

namespace LaravelContextPack\Command;

use LaravelContextPack\Quality\Validation\ContextValidator;
use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Input\InputArgument;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;

#[AsCommand(
    name: 'validate',
    description: 'Validate generated context output against quality rules'
)]
final class ValidateCommand extends Command
{
    protected function configure(): void
    {
        $this
            ->addArgument(
                'context-file',
                InputArgument::REQUIRED,
                'Path to the context JSON file to validate'
            );
    }

    protected function execute(InputInterface $input, OutputInterface $output): int
    {
        $io = new SymfonyStyle($input, $output);

        $contextFile = (string) $input->getArgument('context-file');

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

        $io->section('Running validation rules');

        $validator = new ContextValidator();
        // Note: File-based validation rules require path parameters
        // For context-based validation, we use the validator without file rules
        // You can add custom context-based rules here as needed

        $report = $validator->validate($context);

        // Display results
        foreach ($report->getResults() as $result) {
            if ($result->isPassing()) {
                $io->writeln(sprintf(
                    '  ✓ <info>%s</info>: %s',
                    $result->getRuleName(),
                    $result->getMessage()
                ));
            } else {
                $severity = $result->getSeverity();
                $symbol = match ($severity) {
                    'error' => '✗',
                    'warning' => '⚠',
                    default => 'ℹ',
                };

                $style = match ($severity) {
                    'error' => 'error',
                    'warning' => 'comment',
                    default => 'info',
                };

                $io->writeln(sprintf(
                    '  %s <%s>%s</%s>: %s',
                    $symbol,
                    $style,
                    $result->getRuleName(),
                    $style,
                    $result->getMessage()
                ));
            }
        }

        $io->newLine();

        // Summary
        $io->section('Validation Summary');
        $io->writeln(sprintf('  Passed: <info>%d</info>', $report->getPassCount()));
        $io->writeln(sprintf('  Errors: %s', $report->getErrorCount() > 0
            ? sprintf('<error>%d</error>', $report->getErrorCount())
            : '<info>0</info>'
        ));
        $io->writeln(sprintf('  Warnings: %s', $report->getWarningCount() > 0
            ? sprintf('<comment>%d</comment>', $report->getWarningCount())
            : '<info>0</info>'
        ));

        if ($report->isValid()) {
            $io->success('Validation passed! Context output meets quality standards.');

            return Command::SUCCESS;
        } else {
            $io->error('Validation failed. Please address the errors above.');

            return Command::FAILURE;
        }
    }
}
