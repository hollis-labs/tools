<?php

declare(strict_types=1);

use Rector\Config\RectorConfig;
use Rector\Php82\Rector\ClassConst\AddTypeToConstRector;
use Rector\Set\ValueObject\LevelSetList;
use Rector\Set\ValueObject\SetList;

return static function (RectorConfig $config): void {
    $config->paths([
        __DIR__ . '/src',
        __DIR__ . '/bin',
        __DIR__ . '/tests',
    ]);

    $config->sets([
        LevelSetList::UP_TO_PHP_82,
        SetList::CODE_QUALITY,
        SetList::DEAD_CODE,
    ]);

    $config->rule(AddTypeToConstRector::class);
};
