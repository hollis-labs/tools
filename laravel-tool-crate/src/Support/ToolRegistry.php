<?php

namespace HollisLabs\ToolCrate\Support;

use HollisLabs\ToolCrate\Tools\Contracts\SummarizesTool;
use ReflectionClass;
use Throwable;

class ToolRegistry
{
    public static function summarize(array $toolClasses): array
    {
        $out = [];

        foreach ($toolClasses as $cls) {
            if (!class_exists($cls)) {
                continue;
            }

            $tool = self::instantiateTool($cls);
            if ($tool === null) {
                continue;
            }

            $summaryDefaults = self::summaryDefaults($cls);
            $name = self::resolveMetadata($tool, 'name', $summaryDefaults['name']);

            if ($name === null || $name === '') {
                continue;
            }

            $title = self::resolveMetadata($tool, 'title', $summaryDefaults['title']) ?? $name;
            $description = self::resolveMetadata($tool, 'description', $summaryDefaults['description']) ?? '';

            $out[$name] = [
                'name'        => $name,
                'title'       => $title,
                'description' => $description,
                'hint'        => sprintf("Use help_tool { name: '%s' }", $name),
                'schema'      => $summaryDefaults['schema'],
            ];
        }

        return $out;
    }

    private static function instantiateTool(string $cls): ?object
    {
        if (function_exists('app')) {
            try {
                return app($cls);
            } catch (Throwable $e) {
                // fall back to direct instantiation
            }
        }

        try {
            return new $cls();
        } catch (Throwable $e) {
            return null;
        }
    }

    private static function summaryDefaults(string $cls): array
    {
        if (!is_subclass_of($cls, SummarizesTool::class)) {
            return ['name' => null, 'title' => null, 'description' => null, 'schema' => []];
        }

        /** @var class-string<SummarizesTool> $cls */
        return [
            'name' => $cls::summaryName(),
            'title' => $cls::summaryTitle(),
            'description' => $cls::summaryDescription(),
            'schema' => $cls::schemaSummary(),
        ];
    }

    private static function resolveMetadata(object $tool, string $property, ?string $fallback): ?string
    {
        if (method_exists($tool, $property)) {
            $value = $tool->$property();
            if (is_string($value) && $value !== '') {
                return $value;
            }
        }

        try {
            $ref = new ReflectionClass($tool);
            if ($ref->hasProperty($property)) {
                $prop = $ref->getProperty($property);
                $prop->setAccessible(true);
                $value = $prop->getValue($tool);
                if (is_string($value) && $value !== '') {
                    return $value;
                }
            }
        } catch (Throwable $e) {
            // Ignore reflection issues, fall back to provided default.
        }

        return $fallback;
    }
}
