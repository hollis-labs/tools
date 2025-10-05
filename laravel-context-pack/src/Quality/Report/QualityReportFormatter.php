<?php

declare(strict_types=1);

namespace LaravelContextPack\Quality\Report;

use LaravelContextPack\Quality\Scoring\QualityScore;
use LaravelContextPack\Quality\Validation\ValidationReport;

/**
 * Contract for formatting quality reports.
 */
interface QualityReportFormatter
{
    /**
     * Format a complete quality report.
     *
     * @param array<string, mixed> $context Context data that was analyzed
     */
    public function format(
        ValidationReport $validationReport,
        QualityScore $qualityScore,
        array $context
    ): string;

    /**
     * Get the format name (e.g., 'markdown', 'json').
     */
    public function getFormatName(): string;

    /**
     * Get the recommended file extension for this format.
     */
    public function getFileExtension(): string;
}
