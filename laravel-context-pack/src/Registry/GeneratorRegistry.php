<?php

declare(strict_types=1);

namespace LaravelContextPack\Registry;

use LaravelContextPack\Generator\Exception\GeneratorNotFoundException;
use LaravelContextPack\Generator\GeneratorInterface;

/**
 * Registry for managing and discovering context generators.
 *
 * Supports multiple registration methods:
 * - Explicit registration via register()
 * - Auto-discovery via discover()
 * - Configuration-based registration
 *
 * Example usage:
 * ```php
 * $registry = new GeneratorRegistry();
 * $registry->register(new ApiDocumentationGenerator());
 * $registry->register(new FeatureFlagGenerator());
 *
 * $generator = $registry->get('api-documentation');
 * $allGenerators = $registry->all();
 * ```
 */
final class GeneratorRegistry
{
    /**
     * @var array<string, GeneratorInterface>
     */
    private array $generators = [];

    /**
     * @var array<string, int>
     */
    private array $priorities = [];

    /**
     * Register a generator in the registry.
     *
     * @param int $priority Higher values run first (default: 0)
     *
     * @throws \InvalidArgumentException if a generator with the same name already exists
     */
    public function register(GeneratorInterface $generator, int $priority = 0): void
    {
        $name = $generator->getName();

        if (isset($this->generators[$name])) {
            throw new \InvalidArgumentException(
                sprintf('Generator "%s" is already registered', $name)
            );
        }

        $this->generators[$name] = $generator;
        $this->priorities[$name] = $priority;
    }

    /**
     * Get a generator by name.
     *
     * @throws GeneratorNotFoundException if the generator is not registered
     */
    public function get(string $name): GeneratorInterface
    {
        if (!isset($this->generators[$name])) {
            throw GeneratorNotFoundException::forName($name);
        }

        return $this->generators[$name];
    }

    /**
     * Check if a generator is registered.
     */
    public function has(string $name): bool
    {
        return isset($this->generators[$name]);
    }

    /**
     * Get all registered generators, ordered by priority (highest first).
     *
     * @return array<string, GeneratorInterface>
     */
    public function all(): array
    {
        $sorted = $this->generators;

        uasort($sorted, function (GeneratorInterface $a, GeneratorInterface $b): int {
            $priorityA = $this->priorities[$a->getName()] ?? 0;
            $priorityB = $this->priorities[$b->getName()] ?? 0;

            // Higher priority first
            return $priorityB <=> $priorityA;
        });

        return $sorted;
    }

    /**
     * Get the names of all registered generators.
     *
     * @return list<string>
     */
    public function getNames(): array
    {
        return array_keys($this->generators);
    }

    /**
     * Get the count of registered generators.
     */
    public function count(): int
    {
        return count($this->generators);
    }

    /**
     * Remove a generator from the registry.
     */
    public function unregister(string $name): void
    {
        unset($this->generators[$name], $this->priorities[$name]);
    }

    /**
     * Clear all registered generators.
     */
    public function clear(): void
    {
        $this->generators = [];
        $this->priorities = [];
    }
}
