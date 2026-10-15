<?php

namespace HollisLabs\ToolCrate\Tools;

use Illuminate\JsonSchema\JsonSchema;
use Laravel\Mcp\Request;
use Laravel\Mcp\Response;
use Laravel\Mcp\Server\Tool;
use HollisLabs\ToolCrate\Support\Exec;
use HollisLabs\ToolCrate\Tools\Contracts\SummarizesTool;

class GitSandboxTool extends Tool implements SummarizesTool
{
    protected string $name = 'git_sandbox';
    protected string $title = 'Git sandbox worktree';
    protected string $description = 'Create/manage temporary Git worktrees for safe experiments.';

    public function schema(JsonSchema $s): array
    {
        return [
            'action' => $s->string()
                ->enum(['create', 'end', 'status'])
                ->default('create')
                ->description('Action to perform'),
            'description' => $s->string()
                ->description('Sandbox description (for create action)'),
            'interactive' => $s->boolean()
                ->default(false)
                ->description('Enable interactive mode with prompts'),
            'tmux' => $s->boolean()
                ->default(false)
                ->description('Launch in tmux session'),
            'theme' => $s->string()
                ->enum(['green', 'blue', 'synth', 'sunset', 'mono'])
                ->description('Visual theme'),
            'allow_dirty' => $s->boolean()
                ->default(false)
                ->description('Allow uncommitted changes'),
            'keep' => $s->boolean()
                ->default(false)
                ->description('Keep branch when ending (only for end action)'),
            'cwd' => $s->string()
                ->description('Git repository directory'),
        ];
    }

    public function handle(Request $r): Response
    {
        // Check if git-sandbox is installed
        $which = Exec::run(['which', 'git-sandbox']);
        if (!$which->ok) {
            return Response::error('git-sandbox not found on PATH. Install via: brew install hollis-labs/tap/git-sandbox');
        }

        $action = (string) $r->get('action', 'create');
        $cwd = $r->get('cwd');

        $cmd = ['git', 'sandbox'];

        switch ($action) {
            case 'end':
                $cmd[] = '--end';
                if ($r->get('keep', false)) {
                    $cmd[] = '-k';
                }
                break;

            case 'status':
                // Check for active sandbox by looking for lock file
                $lockCheck = Exec::run(['test', '-f', '.git/sandbox.lock'], null, 3.0, $cwd);
                if ($lockCheck->ok) {
                    return Response::json([
                        'active' => true,
                        'message' => 'Sandbox session is active'
                    ]);
                }
                return Response::json([
                    'active' => false,
                    'message' => 'No active sandbox session'
                ]);

            case 'create':
            default:
                $description = $r->get('description', 'experiment-' . time());
                $cmd[] = $description;

                if ($r->get('interactive', false)) {
                    $cmd[] = '-i';
                }
                if ($r->get('tmux', false)) {
                    $cmd[] = '--tmux';
                }
                if ($theme = $r->get('theme')) {
                    $cmd[] = '--theme=' . $theme;
                }
                if ($r->get('allow_dirty', false)) {
                    $cmd[] = '--allow-dirty';
                }
                break;
        }

        $res = Exec::run($cmd, null, 30.0, $cwd);

        if (!$res->ok) {
            return Response::error(trim($res->stderr ?: $res->stdout));
        }

        return Response::json([
            'success' => true,
            'action' => $action,
            'output' => trim($res->stdout),
        ]);
    }

    public static function summaryName(): string { return 'git_sandbox'; }
    public static function summaryTitle(): string { return 'Git sandbox worktree'; }
    public static function summaryDescription(): string { return 'Create safe temporary worktrees for experiments.'; }
    public static function schemaSummary(): array
    {
        return [
            'action' => 'create|end|status',
            'description' => 'sandbox description',
            'interactive' => 'prompt mode',
            'tmux' => 'launch in tmux',
            'theme' => 'visual theme',
            'allow_dirty' => 'permit uncommitted changes',
            'keep' => 'keep branch on end',
            'cwd' => 'repository directory',
        ];
    }
}
