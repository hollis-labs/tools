<?php

namespace HollisLabs\ToolCrate\Servers;

use Laravel\Mcp\Server;
use Laravel\Mcp\Server\Contracts\Transport;
use HollisLabs\ToolCrate\Tools\JqQueryTool;
use HollisLabs\ToolCrate\Tools\TextSearchTool;
use HollisLabs\ToolCrate\Tools\FileReadTool;
use HollisLabs\ToolCrate\Tools\TextReplaceTool;
use HollisLabs\ToolCrate\Tools\HelpIndexTool;
use HollisLabs\ToolCrate\Tools\HelpToolDetail;
use HollisLabs\ToolCrate\Tools\GitStatusTool;
use HollisLabs\ToolCrate\Tools\GitDiffTool;
use HollisLabs\ToolCrate\Tools\GitApplyPatchTool;
use HollisLabs\ToolCrate\Tools\GitSandboxTool;
use HollisLabs\ToolCrate\Tools\TableQueryTool;
use HollisLabs\ToolCrate\Tools\DbQueryTool;
use HollisLabs\ToolCrate\Tools\DbInspectTool;
use HollisLabs\ToolCrate\Tools\DbSchemaDumpTool;

class ToolCrateServer extends Server
{
    protected string $name = 'ToolCrate';
    protected string $version = '0.2.2';
    protected string $instructions = 'Local dev toolbox. Use help_index to discover JSON, search, and file helpers.';

    protected array $tools = [
        JqQueryTool::class,
        TextSearchTool::class,
        FileReadTool::class,
        TextReplaceTool::class,
        GitStatusTool::class,
        GitDiffTool::class,
        GitApplyPatchTool::class,
        GitSandboxTool::class,
        TableQueryTool::class,
        DbQueryTool::class,
        DbInspectTool::class,
        DbSchemaDumpTool::class,
        HelpIndexTool::class,
        HelpToolDetail::class,
    ];

    public function __construct(Transport $transport)
    {
        parent::__construct($transport);
    }

    protected function boot(): void
    {
        // Filter tools based on config
        $this->tools = collect($this->tools)->filter(function ($toolClass) {
            // Map class name back to config key
            $toolName = $this->getToolConfigKey($toolClass);
            return config('tool-crate.enabled_tools.' . $toolName, true);
        })->values()->all();
    }

    private function getToolConfigKey(string $toolClass): string
    {
        $map = [
            JqQueryTool::class      => 'json_query',
            TextSearchTool::class   => 'text_search',
            FileReadTool::class     => 'file_read',
            TextReplaceTool::class  => 'text_replace',
            HelpIndexTool::class    => 'help_index',
            HelpToolDetail::class   => 'help_tool',
            GitStatusTool::class    => 'git_status',
            GitDiffTool::class      => 'git_diff',
            GitApplyPatchTool::class=> 'git_apply_patch',
            GitSandboxTool::class   => 'git_sandbox',
            TableQueryTool::class   => 'table_query',
            DbQueryTool::class      => 'db_query',
            DbInspectTool::class    => 'db_inspect',
            DbSchemaDumpTool::class => 'db_schema_dump',
        ];

        return $map[$toolClass] ?? '';
    }
}
