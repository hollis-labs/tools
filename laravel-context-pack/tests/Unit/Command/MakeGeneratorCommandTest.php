<?php

declare(strict_types=1);

namespace LaravelContextPack\Tests\Unit\Command;

use LaravelContextPack\Command\MakeGeneratorCommand;
use PHPUnit\Framework\TestCase;
use Symfony\Component\Console\Application;
use Symfony\Component\Console\Tester\CommandTester;
use Symfony\Component\Filesystem\Filesystem;

/**
 * @covers \LaravelContextPack\Command\MakeGeneratorCommand
 */
final class MakeGeneratorCommandTest extends TestCase
{
    private Filesystem $filesystem;

    private string $tempDir;

    protected function setUp(): void
    {
        parent::setUp();

        $this->filesystem = new Filesystem();
        $this->tempDir = sys_get_temp_dir() . '/make-generator-test-' . uniqid();
        $this->filesystem->mkdir($this->tempDir);

        // Change to temp directory for test execution
        chdir($this->tempDir);
    }

    protected function tearDown(): void
    {
        parent::tearDown();

        if (is_dir($this->tempDir)) {
            $this->filesystem->remove($this->tempDir);
        }
    }

    public function test_it_creates_generator_successfully(): void
    {
        $command = new MakeGeneratorCommand($this->filesystem);
        $application = new Application();
        $application->add($command);

        $commandTester = new CommandTester($command);
        $commandTester->execute([
            'name' => 'TestGenerator',
        ]);

        $this->assertEquals(0, $commandTester->getStatusCode());
        $this->assertStringContainsString('Generator created successfully', $commandTester->getDisplay());
    }

    public function test_it_creates_generator_and_test_files(): void
    {
        $command = new MakeGeneratorCommand($this->filesystem);
        $commandTester = new CommandTester($command);

        // Create necessary directories
        $this->filesystem->mkdir([
            $this->tempDir . '/src/App/Generators',
            $this->tempDir . '/tests/Feature/Generators',
        ]);

        $commandTester->execute([
            'name' => 'ApiDocumentation',
        ]);

        // Check that files were created
        $this->assertFileExists($this->tempDir . '/src/App/Generators/ApiDocumentationGenerator.php');
        $this->assertFileExists($this->tempDir . '/tests/Feature/Generators/ApiDocumentationGeneratorTest.php');
    }

    public function test_it_normalizes_class_name_to_pascal_case(): void
    {
        $command = new MakeGeneratorCommand($this->filesystem);
        $commandTester = new CommandTester($command);

        $this->filesystem->mkdir([
            $this->tempDir . '/src/App/Generators',
            $this->tempDir . '/tests/Feature/Generators',
        ]);

        $commandTester->execute([
            'name' => 'api-documentation',
        ]);

        // Should normalize to ApiDocumentationGenerator
        $this->assertFileExists($this->tempDir . '/src/App/Generators/ApiDocumentationGenerator.php');

        $content = file_get_contents($this->tempDir . '/src/App/Generators/ApiDocumentationGenerator.php');
        $this->assertStringContainsString('class ApiDocumentationGenerator', $content);
    }

    public function test_it_adds_generator_suffix_if_missing(): void
    {
        $command = new MakeGeneratorCommand($this->filesystem);
        $commandTester = new CommandTester($command);

        $this->filesystem->mkdir([
            $this->tempDir . '/src/App/Generators',
            $this->tempDir . '/tests/Feature/Generators',
        ]);

        $commandTester->execute([
            'name' => 'ApiDocumentation',
        ]);

        $content = file_get_contents($this->tempDir . '/src/App/Generators/ApiDocumentationGenerator.php');
        $this->assertStringContainsString('class ApiDocumentationGenerator', $content);
    }

    public function test_it_uses_custom_namespace(): void
    {
        $command = new MakeGeneratorCommand($this->filesystem);
        $commandTester = new CommandTester($command);

        $this->filesystem->mkdir([
            $this->tempDir . '/src/Custom/Generators',
            $this->tempDir . '/tests/Feature/Generators',
        ]);

        $commandTester->execute([
            'name' => 'TestGenerator',
            '--namespace' => 'Custom\\Generators',
        ]);

        $generatorPath = $this->tempDir . '/src/Custom/Generators/TestGeneratorGenerator.php';
        $this->assertFileExists($generatorPath);

        $content = file_get_contents($generatorPath);
        $this->assertStringContainsString('namespace Custom\\Generators;', $content);
    }

    public function test_it_rejects_empty_name(): void
    {
        $command = new MakeGeneratorCommand($this->filesystem);
        $commandTester = new CommandTester($command);

        $commandTester->execute([
            'name' => '',
        ]);

        $this->assertEquals(1, $commandTester->getStatusCode());
        $this->assertStringContainsString('Generator name cannot be empty', $commandTester->getDisplay());
    }

    public function test_it_rejects_invalid_name_format(): void
    {
        $command = new MakeGeneratorCommand($this->filesystem);
        $commandTester = new CommandTester($command);

        $commandTester->execute([
            'name' => '123Invalid',
        ]);

        $this->assertEquals(1, $commandTester->getStatusCode());
        $this->assertStringContainsString('must start with a letter', $commandTester->getDisplay());
    }

    public function test_it_rejects_name_with_special_characters(): void
    {
        $command = new MakeGeneratorCommand($this->filesystem);
        $commandTester = new CommandTester($command);

        $commandTester->execute([
            'name' => 'Test@Generator',
        ]);

        $this->assertEquals(1, $commandTester->getStatusCode());
    }

    public function test_it_prevents_overwriting_without_force(): void
    {
        $command = new MakeGeneratorCommand($this->filesystem);
        $commandTester = new CommandTester($command);

        $this->filesystem->mkdir($this->tempDir . '/src/App/Generators');

        // Create file first
        $filePath = $this->tempDir . '/src/App/Generators/TestGeneratorGenerator.php';
        file_put_contents($filePath, '<?php // existing content');

        $commandTester->execute([
            'name' => 'TestGenerator',
        ]);

        $this->assertEquals(1, $commandTester->getStatusCode());
        $this->assertStringContainsString('already exists', $commandTester->getDisplay());

        // Verify original content wasn't overwritten
        $content = file_get_contents($filePath);
        $this->assertStringContainsString('existing content', $content);
    }

    public function test_it_allows_overwriting_with_force(): void
    {
        $command = new MakeGeneratorCommand($this->filesystem);
        $commandTester = new CommandTester($command);

        $this->filesystem->mkdir([
            $this->tempDir . '/src/App/Generators',
            $this->tempDir . '/tests/Feature/Generators',
        ]);

        // Create file first
        $filePath = $this->tempDir . '/src/App/Generators/TestGeneratorGenerator.php';
        file_put_contents($filePath, '<?php // old content');

        $commandTester->execute([
            'name' => 'TestGenerator',
            '--force' => true,
        ]);

        $this->assertEquals(0, $commandTester->getStatusCode());

        // Verify content was overwritten
        $content = file_get_contents($filePath);
        $this->assertStringNotContainsString('old content', $content);
        $this->assertStringContainsString('class TestGeneratorGenerator', $content);
    }

    public function test_generated_class_extends_abstract_generator(): void
    {
        $command = new MakeGeneratorCommand($this->filesystem);
        $commandTester = new CommandTester($command);

        $this->filesystem->mkdir([
            $this->tempDir . '/src/App/Generators',
            $this->tempDir . '/tests/Feature/Generators',
        ]);

        $commandTester->execute([
            'name' => 'TestGenerator',
        ]);

        $content = file_get_contents($this->tempDir . '/src/App/Generators/TestGeneratorGenerator.php');

        $this->assertStringContainsString('use LaravelContextPack\\Generator\\AbstractGenerator;', $content);
        $this->assertStringContainsString('extends AbstractGenerator', $content);
    }

    public function test_generated_class_implements_required_methods(): void
    {
        $command = new MakeGeneratorCommand($this->filesystem);
        $commandTester = new CommandTester($command);

        $this->filesystem->mkdir([
            $this->tempDir . '/src/App/Generators',
            $this->tempDir . '/tests/Feature/Generators',
        ]);

        $commandTester->execute([
            'name' => 'TestGenerator',
        ]);

        $content = file_get_contents($this->tempDir . '/src/App/Generators/TestGeneratorGenerator.php');

        $this->assertStringContainsString('public function getName(): string', $content);
        $this->assertStringContainsString('public function getDescription(): string', $content);
        $this->assertStringContainsString('public function generate(', $content);
        $this->assertStringContainsString('public function canRun(', $content);
        $this->assertStringContainsString('public function getConfigSchema(): array', $content);
    }

    public function test_generated_test_includes_basic_test_cases(): void
    {
        $command = new MakeGeneratorCommand($this->filesystem);
        $commandTester = new CommandTester($command);

        $this->filesystem->mkdir([
            $this->tempDir . '/src/App/Generators',
            $this->tempDir . '/tests/Feature/Generators',
        ]);

        $commandTester->execute([
            'name' => 'TestGenerator',
        ]);

        $testPath = $this->tempDir . '/tests/Feature/Generators/TestGeneratorGeneratorTest.php';
        $content = file_get_contents($testPath);

        $this->assertStringContainsString('test_it_has_correct_name', $content);
        $this->assertStringContainsString('test_it_has_non_empty_description', $content);
        $this->assertStringContainsString('test_it_generates_output', $content);
        $this->assertStringContainsString('test_config_schema_returns_array', $content);
    }

    public function test_generated_code_has_strict_types_declaration(): void
    {
        $command = new MakeGeneratorCommand($this->filesystem);
        $commandTester = new CommandTester($command);

        $this->filesystem->mkdir([
            $this->tempDir . '/src/App/Generators',
            $this->tempDir . '/tests/Feature/Generators',
        ]);

        $commandTester->execute([
            'name' => 'TestGenerator',
        ]);

        $generatorContent = file_get_contents($this->tempDir . '/src/App/Generators/TestGeneratorGenerator.php');
        $testContent = file_get_contents($this->tempDir . '/tests/Feature/Generators/TestGeneratorGeneratorTest.php');

        $this->assertStringContainsString('declare(strict_types=1);', $generatorContent);
        $this->assertStringContainsString('declare(strict_types=1);', $testContent);
    }

    public function test_generator_name_converts_to_kebab_case(): void
    {
        $command = new MakeGeneratorCommand($this->filesystem);
        $commandTester = new CommandTester($command);

        $this->filesystem->mkdir([
            $this->tempDir . '/src/App/Generators',
            $this->tempDir . '/tests/Feature/Generators',
        ]);

        $commandTester->execute([
            'name' => 'ApiDocumentation',
        ]);

        $content = file_get_contents($this->tempDir . '/src/App/Generators/ApiDocumentationGenerator.php');

        // Should use kebab-case for generator name
        $this->assertStringContainsString("return 'api-documentation';", $content);
    }

    public function test_it_displays_helpful_next_steps(): void
    {
        $command = new MakeGeneratorCommand($this->filesystem);
        $commandTester = new CommandTester($command);

        $this->filesystem->mkdir([
            $this->tempDir . '/src/App/Generators',
            $this->tempDir . '/tests/Feature/Generators',
        ]);

        $commandTester->execute([
            'name' => 'TestGenerator',
        ]);

        $output = $commandTester->getDisplay();

        $this->assertStringContainsString('Next steps:', $output);
        $this->assertStringContainsString('Implement the generate() method', $output);
        $this->assertStringContainsString('Write tests', $output);
        $this->assertStringContainsString('Register your generator', $output);
    }
}
