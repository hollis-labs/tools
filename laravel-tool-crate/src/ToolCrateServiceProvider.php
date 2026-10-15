<?php

namespace HollisLabs\ToolCrate;

use Spatie\LaravelPackageTools\Package;
use Spatie\LaravelPackageTools\PackageServiceProvider;
use HollisLabs\ToolCrate\Console\DevJqCommand;
use HollisLabs\ToolCrate\Console\DevSearchCommand;
use HollisLabs\ToolCrate\Servers\ToolCrateServer;
use Laravel\Mcp\Facades\Mcp;

class ToolCrateServiceProvider extends PackageServiceProvider
{
    public function configurePackage(Package $package): void
    {
        $package
            ->name('laravel-tool-crate')
            ->hasConfigFile('tool-crate')
            ->hasCommands([DevJqCommand::class, DevSearchCommand::class]);
    }

    public function boot(): void
    {
        parent::boot();

        // Register the MCP server
        Mcp::local('laravel-tool-crate', ToolCrateServer::class);
    }
}
