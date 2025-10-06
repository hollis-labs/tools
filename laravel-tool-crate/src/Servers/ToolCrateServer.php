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

class ToolCrateServer extends Server
{
    protected string $name = 'ToolCrate';
    protected string $version = '0.2.0';
    protected string $instructions = 'Local dev toolbox. Use help.index to discover JSON, search, and file helpers.';

    protected array $tools = [
        JqQueryTool::class,
        TextSearchTool::class,
        FileReadTool::class,
        TextReplaceTool::class,
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
            JqQueryTool::class => 'json.query',
            TextSearchTool::class => 'text.search',
            FileReadTool::class => 'file.read',
            TextReplaceTool::class => 'text.replace',
            HelpIndexTool::class => 'help.index',
            HelpToolDetail::class => 'help.tool',
        ];

        return $map[$toolClass] ?? '';
    }
}
