<?php

declare(strict_types=1);

namespace LaravelContextPack\Command;

use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Input\InputArgument;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Input\InputOption;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Style\SymfonyStyle;
use Symfony\Component\Filesystem\Filesystem;

#[AsCommand(name: 'make:generator', description: 'Create a new generator class')]
final class MakeGeneratorCommand extends Command
{
    private Filesystem $filesystem;

    public function __construct(?Filesystem $filesystem = null)
    {
        parent::__construct();
        $this->filesystem = $filesystem ?? new Filesystem();
    }

    protected function configure(): void
    {
        $this
            ->addArgument(
                'name',
                InputArgument::REQUIRED,
                'The name of the generator (e.g., ApiDocumentation, FeatureFlag)'
            )
            ->addOption(
                'namespace',
                null,
                InputOption::VALUE_REQUIRED,
                'Custom namespace for the generator (default: App\\Generators)',
                'App\\Generators'
            )
            ->addOption(
                'path',
                null,
                InputOption::VALUE_REQUIRED,
                'Custom path for the generator file',
                null
            )
            ->addOption(
                'force',
                'f',
                InputOption::VALUE_NONE,
                'Overwrite existing files'
            );
    }

    protected function execute(InputInterface $input, OutputInterface $output): int
    {
        $io = new SymfonyStyle($input, $output);

        $name = (string) $input->getArgument('name');
        $namespace = (string) $input->getOption('namespace');
        $customPath = $input->getOption('path');
        $force = (bool) $input->getOption('force');

        // Validate generator name
        $validationError = $this->validateGeneratorName($name);
        if ($validationError !== null) {
            $io->error($validationError);
            return Command::FAILURE;
        }

        // Normalize generator name to PascalCase
        $className = $this->normalizeClassName($name);
        $generatorName = $this->classNameToGeneratorName($className);

        // Determine file paths
        $generatorPath = $this->resolveGeneratorPath($className, $namespace, $customPath);
        $testPath = $this->resolveTestPath($className, $namespace);

        // Check if files already exist
        if (!$force && $this->filesystem->exists($generatorPath)) {
            $io->error(sprintf('Generator already exists at: %s', $generatorPath));
            $io->note('Use --force to overwrite existing files.');
            return Command::FAILURE;
        }

        // Create directories if needed
        $generatorDir = dirname($generatorPath);
        $testDir = dirname($testPath);

        if (!is_dir($generatorDir)) {
            $this->filesystem->mkdir($generatorDir);
        }
        if (!is_dir($testDir)) {
            $this->filesystem->mkdir($testDir);
        }

        // Generate files
        $generatorContent = $this->generateGeneratorClass($className, $generatorName, $namespace);
        $testContent = $this->generateTestClass($className, $generatorName, $namespace);

        $this->filesystem->dumpFile($generatorPath, $generatorContent);
        $this->filesystem->dumpFile($testPath, $testContent);

        $io->success('Generator created successfully!');
        $io->writeln([
            '',
            '<info>Generator:</info> ' . $generatorPath,
            '<info>Test:</info> ' . $testPath,
            '',
            '<comment>Next steps:</comment>',
            '1. Implement the generate() method in your generator',
            '2. Update the canRun() method to check for required files/directories',
            '3. Add configuration schema if needed via getConfigSchema()',
            '4. Write tests for your generator',
            '5. Register your generator in the GeneratorRegistry',
            '',
        ]);

        return Command::SUCCESS;
    }

    /**
     * Validate generator name format.
     */
    private function validateGeneratorName(string $name): ?string
    {
        if ($name === '') {
            return 'Generator name cannot be empty.';
        }

        if (!preg_match('/^[A-Za-z][A-Za-z0-9]*$/', $name)) {
            return 'Generator name must start with a letter and contain only alphanumeric characters.';
        }

        if (strlen($name) > 100) {
            return 'Generator name is too long (max 100 characters).';
        }

        return null;
    }

    /**
     * Normalize class name to PascalCase with "Generator" suffix.
     */
    private function normalizeClassName(string $name): string
    {
        // Convert to PascalCase
        $className = str_replace(['-', '_', ' '], '', ucwords($name, '-_ '));

        // Ensure it ends with "Generator"
        if (!str_ends_with($className, 'Generator')) {
            $className .= 'Generator';
        }

        return $className;
    }

    /**
     * Convert class name to generator identifier (lowercase-hyphenated).
     */
    private function classNameToGeneratorName(string $className): string
    {
        // Remove "Generator" suffix
        $name = preg_replace('/Generator$/', '', $className);

        if ($name === null || $name === '') {
            $name = $className;
        }

        // Convert PascalCase to kebab-case
        $name = (string) preg_replace('/([a-z])([A-Z])/', '$1-$2', $name);
        $name = strtolower($name);

        return $name;
    }

    /**
     * Resolve the full path for the generator file.
     */
    private function resolveGeneratorPath(string $className, string $namespace, ?string $customPath): string
    {
        if ($customPath !== null) {
            $customPath = (string) $customPath;
            if (!str_ends_with($customPath, '.php')) {
                $customPath = rtrim($customPath, DIRECTORY_SEPARATOR) . DIRECTORY_SEPARATOR . $className . '.php';
            }
            return $customPath;
        }

        // Default: use PSR-4 autoloading convention
        $relativePath = str_replace('\\', DIRECTORY_SEPARATOR, $namespace);
        $basePath = getcwd();

        // Common Laravel/PHP project structures
        if (str_starts_with($namespace, 'App\\')) {
            $basePath .= DIRECTORY_SEPARATOR . 'app' . DIRECTORY_SEPARATOR . str_replace('\\', DIRECTORY_SEPARATOR, substr($namespace, 4));
        } elseif (str_starts_with($namespace, 'LaravelContextPack\\')) {
            $basePath .= DIRECTORY_SEPARATOR . 'src' . DIRECTORY_SEPARATOR . str_replace('\\', DIRECTORY_SEPARATOR, substr($namespace, 19));
        } else {
            $basePath .= DIRECTORY_SEPARATOR . 'src' . DIRECTORY_SEPARATOR . $relativePath;
        }

        return $basePath . DIRECTORY_SEPARATOR . $className . '.php';
    }

    /**
     * Resolve the full path for the test file.
     */
    private function resolveTestPath(string $className, string $namespace): string
    {
        $basePath = getcwd() . DIRECTORY_SEPARATOR . 'tests';

        // Determine test subdirectory based on namespace
        if (str_starts_with($namespace, 'App\\')) {
            $testPath = $basePath . DIRECTORY_SEPARATOR . 'Feature' . DIRECTORY_SEPARATOR . 'Generators';
        } else {
            $testPath = $basePath . DIRECTORY_SEPARATOR . 'Unit' . DIRECTORY_SEPARATOR . 'Generator';
        }

        return $testPath . DIRECTORY_SEPARATOR . $className . 'Test.php';
    }

    /**
     * Generate the generator class content.
     */
    private function generateGeneratorClass(string $className, string $generatorName, string $namespace): string
    {
        $stubPath = $this->getStubPath('generator.stub');

        if (!file_exists($stubPath)) {
            // Fallback: generate inline if stub doesn't exist
            return $this->generateInlineGeneratorClass($className, $generatorName, $namespace);
        }

        $stub = file_get_contents($stubPath);
        if ($stub === false) {
            return $this->generateInlineGeneratorClass($className, $generatorName, $namespace);
        }

        return str_replace(
            ['{{ namespace }}', '{{ class }}', '{{ generator_name }}', '{{ description }}'],
            [
                $namespace,
                $className,
                $generatorName,
                'Generate ' . str_replace('-', ' ', $generatorName) . ' documentation',
            ],
            $stub
        );
    }

    /**
     * Generate the test class content.
     */
    private function generateTestClass(string $className, string $generatorName, string $namespace): string
    {
        $stubPath = $this->getStubPath('generator-test.stub');

        if (!file_exists($stubPath)) {
            // Fallback: generate inline if stub doesn't exist
            return $this->generateInlineTestClass($className, $generatorName, $namespace);
        }

        $stub = file_get_contents($stubPath);
        if ($stub === false) {
            return $this->generateInlineTestClass($className, $generatorName, $namespace);
        }

        $testNamespace = str_starts_with($namespace, 'App\\')
            ? 'Tests\\Feature\\Generators'
            : 'LaravelContextPack\\Tests\\Unit\\Generator';

        return str_replace(
            ['{{ namespace }}', '{{ testNamespace }}', '{{ class }}', '{{ generator_name }}'],
            [$namespace, $testNamespace, $className, $generatorName],
            $stub
        );
    }

    /**
     * Get stub file path.
     */
    private function getStubPath(string $filename): string
    {
        // Check multiple possible locations
        $locations = [
            getcwd() . DIRECTORY_SEPARATOR . 'stubs' . DIRECTORY_SEPARATOR . $filename,
            __DIR__ . DIRECTORY_SEPARATOR . '..' . DIRECTORY_SEPARATOR . '..' . DIRECTORY_SEPARATOR . 'stubs' . DIRECTORY_SEPARATOR . $filename,
            dirname(__DIR__, 2) . DIRECTORY_SEPARATOR . 'stubs' . DIRECTORY_SEPARATOR . $filename,
        ];

        foreach ($locations as $location) {
            if (file_exists($location)) {
                return $location;
            }
        }

        return $locations[0]; // Return first location as default
    }

    /**
     * Generate generator class inline (fallback).
     */
    private function generateInlineGeneratorClass(string $className, string $generatorName, string $namespace): string
    {
        return <<<PHP
<?php

declare(strict_types=1);

namespace {$namespace};

use LaravelContextPack\Generator\AbstractGenerator;

/**
 * Generator for {$generatorName}.
 *
 * TODO: Add detailed description of what this generator does.
 */
final class {$className} extends AbstractGenerator
{
    /**
     * {@inheritdoc}
     */
    public function getName(): string
    {
        return '{$generatorName}';
    }

    /**
     * {@inheritdoc}
     */
    public function getDescription(): string
    {
        return 'Generate {$generatorName} documentation';
    }

    /**
     * {@inheritdoc}
     */
    public function generate(string \$projectPath, string \$outputPath): array
    {
        // TODO: Implement your generator logic here
        //
        // Example:
        // 1. Scan relevant files/directories
        // 2. Parse and analyze data
        // 3. Generate documentation file
        // 4. Return result status

        \$outputFile = \$outputPath . '/TODO-{$generatorName}.md';
        \$logFile = \$outputPath . '/logs/{$generatorName}.log';

        \$this->appendLog(\$logFile, '[' . \$this->now() . '] Generation started');

        // Example implementation
        \$content = "# {$generatorName}\\n\\n";
        \$content .= "TODO: Add your generated content here\\n";

        \$this->filesystem->dumpFile(\$outputFile, \$content);

        \$this->appendLog(\$logFile, '[' . \$this->now() . '] Generation completed');

        return [
            'status' => 'success',
            'message' => ucfirst('{$generatorName}') . ' generated successfully',
            'outputFile' => \$outputFile,
        ];
    }

    /**
     * {@inheritdoc}
     */
    public function canRun(string \$projectPath): bool
    {
        // TODO: Add pre-run checks here
        //
        // Example:
        // return \$this->isDirectoryAccessible(\$projectPath . '/app/Http/Controllers');

        return true;
    }

    /**
     * {@inheritdoc}
     */
    public function getConfigSchema(): array
    {
        // TODO: Define configuration options if needed
        //
        // Example:
        // return [
        //     'include_examples' => ['type' => 'boolean', 'default' => true],
        //     'format' => ['type' => 'string', 'default' => 'markdown'],
        // ];

        return [];
    }
}

PHP;
    }

    /**
     * Generate test class inline (fallback).
     */
    private function generateInlineTestClass(string $className, string $generatorName, string $namespace): string
    {
        $testNamespace = str_starts_with($namespace, 'App\\')
            ? 'Tests\\Feature\\Generators'
            : 'LaravelContextPack\\Tests\\Unit\\Generator';

        $fqcn = $namespace . '\\' . $className;

        return <<<PHP
<?php

declare(strict_types=1);

namespace {$testNamespace};

use {$fqcn};

/**
 * @covers \\{$fqcn}
 */
final class {$className}Test extends \PHPUnit\Framework\TestCase
{
    private {$className} \$generator;

    protected function setUp(): void
    {
        parent::setUp();
        \$this->generator = new {$className}();
    }

    public function test_it_has_correct_name(): void
    {
        \$this->assertSame('{$generatorName}', \$this->generator->getName());
    }

    public function test_it_has_description(): void
    {
        \$description = \$this->generator->getDescription();
        \$this->assertNotEmpty(\$description);
        \$this->assertIsString(\$description);
    }

    public function test_it_can_run_by_default(): void
    {
        \$this->assertTrue(\$this->generator->canRun(__DIR__));
    }

    public function test_it_generates_output(): void
    {
        \$projectPath = __DIR__ . '/fixtures';
        \$outputPath = sys_get_temp_dir() . '/{$generatorName}-test-' . uniqid();

        // Create test directories
        if (!is_dir(\$projectPath)) {
            mkdir(\$projectPath, 0777, true);
        }
        if (!is_dir(\$outputPath)) {
            mkdir(\$outputPath, 0777, true);
        }
        if (!is_dir(\$outputPath . '/logs')) {
            mkdir(\$outputPath . '/logs', 0777, true);
        }

        try {
            \$result = \$this->generator->generate(\$projectPath, \$outputPath);

            \$this->assertIsArray(\$result);
            \$this->assertArrayHasKey('status', \$result);
            \$this->assertArrayHasKey('message', \$result);
            \$this->assertContains(\$result['status'], ['success', 'skipped', 'failure']);
        } finally {
            // Cleanup
            if (is_dir(\$outputPath)) {
                array_map('unlink', glob(\$outputPath . '/*'));
                array_map('unlink', glob(\$outputPath . '/logs/*'));
                @rmdir(\$outputPath . '/logs');
                @rmdir(\$outputPath);
            }
        }
    }

    public function test_config_schema_returns_array(): void
    {
        \$schema = \$this->generator->getConfigSchema();
        \$this->assertIsArray(\$schema);
    }
}

PHP;
    }
}

