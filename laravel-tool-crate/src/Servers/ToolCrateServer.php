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
use HollisLabs\ToolCrate\Tools\Orchestration\AgentsListTool;
use HollisLabs\ToolCrate\Tools\Orchestration\AgentDetailTool;
use HollisLabs\ToolCrate\Tools\Orchestration\AgentSaveTool;
use HollisLabs\ToolCrate\Tools\Orchestration\AgentStatusTool;
use HollisLabs\ToolCrate\Tools\Orchestration\SprintsListTool;
use HollisLabs\ToolCrate\Tools\Orchestration\TaskAssignTool;
use HollisLabs\ToolCrate\Tools\Orchestration\TaskDetailTool;
use HollisLabs\ToolCrate\Tools\Orchestration\TasksListTool;
use HollisLabs\ToolCrate\Tools\Orchestration\TaskStatusTool;
use HollisLabs\ToolCrate\Tools\Orchestration\SprintDetailTool;
use HollisLabs\ToolCrate\Tools\Orchestration\SprintSaveTool;
use HollisLabs\ToolCrate\Tools\Orchestration\SprintStatusTool;
use HollisLabs\ToolCrate\Tools\Orchestration\SprintTasksAttachTool;

class ToolCrateServer extends Server
{
    protected string $name = 'ToolCrate';
    protected string $version = '0.2.0';
    protected string $instructions = 'Local dev toolbox. Use help.index to discover JSON, search, file, and orchestration helpers.';

    protected array $tools = [
        JqQueryTool::class,
        TextSearchTool::class,
        FileReadTool::class,
        TextReplaceTool::class,
        HelpIndexTool::class,
        HelpToolDetail::class,
        AgentsListTool::class,
        AgentDetailTool::class,
        AgentSaveTool::class,
        AgentStatusTool::class,
        SprintsListTool::class,
        TasksListTool::class,
        TaskDetailTool::class,
        TaskAssignTool::class,
        TaskStatusTool::class,
        SprintDetailTool::class,
        SprintSaveTool::class,
        SprintStatusTool::class,
        SprintTasksAttachTool::class,
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
            AgentsListTool::class => 'orchestration.agents.list',
            AgentDetailTool::class => 'orchestration.agents.detail',
            AgentSaveTool::class => 'orchestration.agents.save',
            AgentStatusTool::class => 'orchestration.agents.status',
            SprintsListTool::class => 'orchestration.sprints.list',
            TasksListTool::class => 'orchestration.tasks.list',
            TaskDetailTool::class => 'orchestration.tasks.detail',
            TaskAssignTool::class => 'orchestration.tasks.assign',
            TaskStatusTool::class => 'orchestration.tasks.status',
            SprintDetailTool::class => 'orchestration.sprints.detail',
            SprintSaveTool::class => 'orchestration.sprints.save',
            SprintStatusTool::class => 'orchestration.sprints.status',
            SprintTasksAttachTool::class => 'orchestration.sprints.attach_tasks',
        ];

        return $map[$toolClass] ?? '';
    }
}
