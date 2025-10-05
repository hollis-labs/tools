<?php

declare(strict_types=1);

namespace LaravelContextPack;

use LaravelContextPack\Command\BenchmarkCommand;
use LaravelContextPack\Command\GenerateContextPackCommand;
use LaravelContextPack\Command\MakeGeneratorCommand;
use LaravelContextPack\Command\QualityScoreCommand;
use LaravelContextPack\Command\ValidateCommand;
use Symfony\Component\Console\Application;

final class ConsoleApplication extends Application
{
    public function __construct()
    {
        parent::__construct('Laravel Context Pack', '0.1.0-dev');

        $this->add(new GenerateContextPackCommand);
        $this->add(new MakeGeneratorCommand);
        $this->add(new ValidateCommand);
        $this->add(new QualityScoreCommand);
        $this->add(new BenchmarkCommand);
    }
}
