<?php

declare(strict_types=1);

namespace LaravelContextPack\Generator\Exception;

/**
 * Exception thrown when a requested generator cannot be found in the registry.
 */
class GeneratorNotFoundException extends GeneratorException
{
    public static function forName(string $name): self
    {
        return new self(sprintf('Generator "%s" not found in registry', $name));
    }
}
