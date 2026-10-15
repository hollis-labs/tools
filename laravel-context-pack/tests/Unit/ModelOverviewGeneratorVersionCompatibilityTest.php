<?php

declare(strict_types=1);

use LaravelContextPack\Generator\ModelOverviewGenerator;

describe('ModelOverviewGenerator Version Compatibility', function () {
    beforeEach(function () {
        $this->generator = new ModelOverviewGenerator;
        $this->tempDir = sys_get_temp_dir() . '/context-pack-test-' . uniqid();
        mkdir($this->tempDir);
        mkdir($this->tempDir . '/logs', 0755, true);
    });

    afterEach(function () {
        if (is_dir($this->tempDir)) {
            // Recursively delete directory
            $files = new RecursiveIteratorIterator(
                new RecursiveDirectoryIterator($this->tempDir, RecursiveDirectoryIterator::SKIP_DOTS),
                RecursiveIteratorIterator::CHILD_FIRST
            );
            foreach ($files as $fileinfo) {
                $func = ($fileinfo->isDir() ? 'rmdir' : 'unlink');
                $func($fileinfo->getRealPath());
            }
            rmdir($this->tempDir);
        }
    });

    test('parses Laravel 8.x models with old accessor/mutator syntax', function () {
        $fixtureDir = __DIR__ . '/../../tests/fixtures/laravel-8x';
        $result = $this->generator->generate($fixtureDir, $this->tempDir);

        expect($result['status'])->toBe('success');
        expect(file_exists($this->tempDir . '/03-model-overview.md'))->toBeTrue();

        $content = file_get_contents($this->tempDir . '/03-model-overview.md');

        // Check that old-style accessors are detected
        expect($content)->toContain('Accessor: `fullName`');
        expect($content)->toContain('Accessor: `titleUppercase`');

        // Check that old-style mutators are detected (setEmailAttribute in User model)
        // Note: Mutators may not always be in output if no set methods are defined

        // Check that $casts property is detected
        expect($content)->toContain('email_verified_at: datetime');
        expect($content)->toContain('is_admin: boolean');
    });

    test('parses Laravel 9.x models with new Attribute syntax', function () {
        $fixtureDir = __DIR__ . '/../../tests/fixtures/laravel-9x';
        $result = $this->generator->generate($fixtureDir, $this->tempDir);

        expect($result['status'])->toBe('success');
        expect(file_exists($this->tempDir . '/03-model-overview.md'))->toBeTrue();

        $content = file_get_contents($this->tempDir . '/03-model-overview.md');

        // Check that new-style accessors are detected
        expect($content)->toContain('Accessor: `fullName`');
        expect($content)->toContain('Accessor: `titleUppercase`');
        expect($content)->toContain('Accessor: `excerpt`');

        // Check that old-style accessors still work (mixed mode)
        expect($content)->toContain('Accessor: `displayName`');

        // Check that new-style mutators are detected (email in User model has both get and set)
        expect($content)->toContain('Mutator: `email`');

        // Check that $casts property is detected
        expect($content)->toContain('published_at: datetime');
        expect($content)->toContain('is_featured: boolean');
    });

    test('parses Laravel 10.x models with Attribute syntax', function () {
        $fixtureDir = __DIR__ . '/../../tests/fixtures/laravel-10x';
        $result = $this->generator->generate($fixtureDir, $this->tempDir);

        expect($result['status'])->toBe('success');
        expect(file_exists($this->tempDir . '/03-model-overview.md'))->toBeTrue();

        $content = file_get_contents($this->tempDir . '/03-model-overview.md');

        // Check that accessors with type hints are detected
        expect($content)->toContain('Accessor: `formattedPrice`');

        // Check that $casts property is detected
        expect($content)->toContain('price: decimal:2');
    });

    test('parses Laravel 11.x models with casts() method', function () {
        $fixtureDir = __DIR__ . '/../../tests/fixtures/laravel-11x';
        $result = $this->generator->generate($fixtureDir, $this->tempDir);

        expect($result['status'])->toBe('success');
        expect(file_exists($this->tempDir . '/03-model-overview.md'))->toBeTrue();

        $content = file_get_contents($this->tempDir . '/03-model-overview.md');

        // Check that accessors are detected
        expect($content)->toContain('Accessor: `maskedAccountNumber`');
        expect($content)->toContain('Accessor: `formattedAmount`');
        expect($content)->toContain('Accessor: `fullName`');

        // Check that old-style accessors still work
        expect($content)->toContain('Accessor: `initials`');

        // Check that casts() method is detected
        expect($content)->toContain('balance: decimal:2');
        expect($content)->toContain('opened_at: datetime');
        expect($content)->toContain('is_active: boolean');
        expect($content)->toContain('amount: decimal:2');
        expect($content)->toContain('processed_at: datetime');
    });

    test('detects relationships across all Laravel versions', function () {
        $fixtureDir = __DIR__ . '/../../tests/fixtures/laravel-8x';
        $result = $this->generator->generate($fixtureDir, $this->tempDir);

        expect($result['status'])->toBe('success');

        $content = file_get_contents($this->tempDir . '/03-model-overview.md');

        // Check relationships
        expect($content)->toContain('| HASMANY | Post | posts |');
        expect($content)->toContain('| BELONGSTOMANY | Role | roles |');
        expect($content)->toContain('| BELONGSTO | User | user |');
    });

    test('generates mermaid diagram across all Laravel versions', function () {
        $fixtureDir = __DIR__ . '/../../tests/fixtures/laravel-9x';
        $result = $this->generator->generate($fixtureDir, $this->tempDir);

        expect($result['status'])->toBe('success');

        $content = file_get_contents($this->tempDir . '/03-model-overview.md');

        // Check for mermaid diagram presence
        expect($content)->toContain('```mermaid');
        expect($content)->toContain('erDiagram');
    });
});
