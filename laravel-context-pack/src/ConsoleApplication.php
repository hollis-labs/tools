<?php

declare(strict_types=1);

namespace LaravelContextPack;

use LaravelContextPack\Command\GenerateContextPackCommand;
use Symfony\Component\Console\Application;

final class ConsoleApplication extends Application
{
    public function __construct()
    {
        parent::__construct('Laravel Context Pack', '0.1.0-dev');

        $this->add(new GenerateContextPackCommand);
    }
}
