<?php

declare(strict_types=1);

use LaravelContextPack\Generator\ModelOverviewGenerator;
use Symfony\Component\Filesystem\Filesystem;

beforeEach(function (): void {
    $this->filesystem = new Filesystem;
    $this->workspace = sys_get_temp_dir() . '/lcp-model-ext-test-' . bin2hex(random_bytes(5));
    $this->projectPath = $this->workspace . '/project';
    $this->outputPath = $this->workspace . '/output';

    $this->filesystem->mkdir([
        $this->workspace,
        $this->projectPath . '/app/Models',
        $this->outputPath . '/logs',
    ]);
});

afterEach(function (): void {
    $this->filesystem->remove($this->workspace);
});

test('it detects soft deletes trait', function (): void {
    file_put_contents($this->projectPath . '/app/Models/Post.php', <<<'PHP'
    <?php
    namespace App\Models;
    use Illuminate\Database\Eloquent\Model;
    use Illuminate\Database\Eloquent\SoftDeletes;

    class Post extends Model
    {
        use SoftDeletes;

        protected $fillable = ['title'];
    }
    PHP);

    $generator = new ModelOverviewGenerator;
    $result = $generator->generate($this->projectPath, $this->outputPath);

    expect($result['status'])->toBe('success');

    $content = file_get_contents($this->outputPath . '/03-model-overview.md');
    expect($content)->toContain('Soft Deletes: yes');
});

test('it detects fillable and guarded properties', function (): void {
    file_put_contents($this->projectPath . '/app/Models/User.php', <<<'PHP'
    <?php
    namespace App\Models;
    use Illuminate\Database\Eloquent\Model;

    class User extends Model
    {
        protected $fillable = ['name', 'email'];
        protected $guarded = ['id'];
    }
    PHP);

    $generator = new ModelOverviewGenerator;
    $result = $generator->generate($this->projectPath, $this->outputPath);

    $content = file_get_contents($this->outputPath . '/03-model-overview.md');
    expect($content)->toContain('Fillable:');
    expect($content)->toContain('`name, email`');
    expect($content)->toContain('Guarded:');
    expect($content)->toContain('`id`');
});

test('it detects casts', function (): void {
    file_put_contents($this->projectPath . '/app/Models/Event.php', <<<'PHP'
    <?php
    namespace App\Models;
    use Illuminate\Database\Eloquent\Model;

    class Event extends Model
    {
        protected $casts = [
            'start_date' => 'datetime',
            'is_active' => 'boolean',
            'data' => 'array',
        ];
    }
    PHP);

    $generator = new ModelOverviewGenerator;
    $result = $generator->generate($this->projectPath, $this->outputPath);

    $content = file_get_contents($this->outputPath . '/03-model-overview.md');
    expect($content)->toContain('Casts:');
    expect($content)->toContain('start_date: datetime');
    expect($content)->toContain('is_active: boolean');
    expect($content)->toContain('data: array');
});

test('it detects accessors', function (): void {
    file_put_contents($this->projectPath . '/app/Models/Product.php', <<<'PHP'
    <?php
    namespace App\Models;
    use Illuminate\Database\Eloquent\Model;

    class Product extends Model
    {
        public function getFormattedPriceAttribute($value)
        {
            return '$' . number_format($value, 2);
        }

        public function getIsExpensiveAttribute()
        {
            return $this->price > 1000;
        }
    }
    PHP);

    $generator = new ModelOverviewGenerator;
    $result = $generator->generate($this->projectPath, $this->outputPath);

    $content = file_get_contents($this->outputPath . '/03-model-overview.md');
    expect($content)->toContain('Accessor: `formatted_price`');
    expect($content)->toContain('Accessor: `is_expensive`');
});

test('it detects mutators', function (): void {
    file_put_contents($this->projectPath . '/app/Models/User.php', <<<'PHP'
    <?php
    namespace App\Models;
    use Illuminate\Database\Eloquent\Model;

    class User extends Model
    {
        public function setEmailAttribute($value)
        {
            $this->attributes['email'] = strtolower($value);
        }

        public function setPasswordAttribute($value)
        {
            $this->attributes['password'] = bcrypt($value);
        }
    }
    PHP);

    $generator = new ModelOverviewGenerator;
    $result = $generator->generate($this->projectPath, $this->outputPath);

    $content = file_get_contents($this->outputPath . '/03-model-overview.md');
    expect($content)->toContain('Mutator: `email`');
    expect($content)->toContain('Mutator: `password`');
});

test('it detects custom table name', function (): void {
    file_put_contents($this->projectPath . '/app/Models/Legacy.php', <<<'PHP'
    <?php
    namespace App\Models;
    use Illuminate\Database\Eloquent\Model;

    class Legacy extends Model
    {
        protected $table = 'old_legacy_table';
    }
    PHP);

    $generator = new ModelOverviewGenerator;
    $result = $generator->generate($this->projectPath, $this->outputPath);

    $content = file_get_contents($this->outputPath . '/03-model-overview.md');
    expect($content)->toContain('Table: `old_legacy_table`');
});

test('it detects belongsToMany relationships', function (): void {
    file_put_contents($this->projectPath . '/app/Models/User.php', <<<'PHP'
    <?php
    namespace App\Models;
    use Illuminate\Database\Eloquent\Model;

    class User extends Model
    {
        public function roles()
        {
            return $this->belongsToMany(Role::class);
        }
    }
    PHP);

    file_put_contents($this->projectPath . '/app/Models/Role.php', <<<'PHP'
    <?php
    namespace App\Models;
    use Illuminate\Database\Eloquent\Model;

    class Role extends Model
    {
        public function users()
        {
            return $this->belongsToMany(User::class);
        }
    }
    PHP);

    $generator = new ModelOverviewGenerator;
    $result = $generator->generate($this->projectPath, $this->outputPath);

    $content = file_get_contents($this->outputPath . '/03-model-overview.md');
    expect($content)->toContain('[pivot]');
    expect($content)->toContain('BELONGSTOMANY');
});

test('it detects morphTo relationships', function (): void {
    file_put_contents($this->projectPath . '/app/Models/Image.php', <<<'PHP'
    <?php
    namespace App\Models;
    use Illuminate\Database\Eloquent\Model;

    class Image extends Model
    {
        public function imageable()
        {
            return $this->morphTo();
        }
    }
    PHP);

    $generator = new ModelOverviewGenerator;
    $result = $generator->generate($this->projectPath, $this->outputPath);

    $content = file_get_contents($this->outputPath . '/03-model-overview.md');
    expect($content)->toContain('MORPHTO');
    expect($content)->toContain('[polymorphic]');
});

test('it handles models with no relationships', function (): void {
    file_put_contents($this->projectPath . '/app/Models/Setting.php', <<<'PHP'
    <?php
    namespace App\Models;
    use Illuminate\Database\Eloquent\Model;

    class Setting extends Model
    {
        protected $fillable = ['key', 'value'];
    }
    PHP);

    $generator = new ModelOverviewGenerator;
    $result = $generator->generate($this->projectPath, $this->outputPath);

    expect($result['status'])->toBe('success');

    $content = file_get_contents($this->outputPath . '/03-model-overview.md');
    expect($content)->toContain('App\\Models\\Setting');
    expect($content)->toContain('Relationships: none');
});

test('it generates Mermaid ERD for complex relationships', function (): void {
    file_put_contents($this->projectPath . '/app/Models/Author.php', <<<'PHP'
    <?php
    namespace App\Models;
    use Illuminate\Database\Eloquent\Model;

    class Author extends Model
    {
        public function books()
        {
            return $this->hasMany(Book::class);
        }

        public function profile()
        {
            return $this->hasOne(Profile::class);
        }
    }
    PHP);

    file_put_contents($this->projectPath . '/app/Models/Book.php', <<<'PHP'
    <?php
    namespace App\Models;
    use Illuminate\Database\Eloquent\Model;

    class Book extends Model
    {
        public function author()
        {
            return $this->belongsTo(Author::class);
        }

        public function reviews()
        {
            return $this->hasMany(Review::class);
        }
    }
    PHP);

    file_put_contents($this->projectPath . '/app/Models/Review.php', <<<'PHP'
    <?php
    namespace App\Models;
    use Illuminate\Database\Eloquent\Model;

    class Review extends Model
    {
        public function book()
        {
            return $this->belongsTo(Book::class);
        }
    }
    PHP);

    file_put_contents($this->projectPath . '/app/Models/Profile.php', <<<'PHP'
    <?php
    namespace App\Models;
    use Illuminate\Database\Eloquent\Model;

    class Profile extends Model
    {
        public function author()
        {
            return $this->belongsTo(Author::class);
        }
    }
    PHP);

    $generator = new ModelOverviewGenerator;
    $result = $generator->generate($this->projectPath, $this->outputPath);

    $content = file_get_contents($this->outputPath . '/03-model-overview.md');

    expect($content)->toContain('```mermaid');
    expect($content)->toContain('erDiagram');
    expect($content)->toContain('Author ||--o{ Book');
    expect($content)->toContain('Book ||--o{ Review');
    expect($content)->toContain('Author ||--|| Profile');
    expect($content)->toContain('%% Legend:');
});

test('it handles models in subdirectories', function (): void {
    $this->filesystem->mkdir($this->projectPath . '/app/Models/Admin');

    file_put_contents($this->projectPath . '/app/Models/Admin/AdminUser.php', <<<'PHP'
    <?php
    namespace App\Models\Admin;
    use Illuminate\Database\Eloquent\Model;

    class AdminUser extends Model
    {
        protected $table = 'admin_users';
    }
    PHP);

    $generator = new ModelOverviewGenerator;
    $result = $generator->generate($this->projectPath, $this->outputPath);

    $content = file_get_contents($this->outputPath . '/03-model-overview.md');
    expect($content)->toContain('App\\Models\\Admin\\AdminUser');
});

test('it logs model discovery', function (): void {
    file_put_contents($this->projectPath . '/app/Models/Test.php', <<<'PHP'
    <?php
    namespace App\Models;
    use Illuminate\Database\Eloquent\Model;

    class Test extends Model {}
    PHP);

    $generator = new ModelOverviewGenerator;
    $result = $generator->generate($this->projectPath, $this->outputPath);

    expect(file_exists($this->outputPath . '/logs/model-overview.log'))->toBeTrue();

    $log = file_get_contents($this->outputPath . '/logs/model-overview.log');
    expect($log)->toContain('Model overview generated');
});

test('it handles empty models directory', function (): void {
    $generator = new ModelOverviewGenerator;
    $result = $generator->generate($this->projectPath, $this->outputPath);

    expect($result['status'])->toBe('success');

    $content = file_get_contents($this->outputPath . '/03-model-overview.md');
    expect($content)->toContain('# Model Overview');
    expect($content)->toContain('No models found');
});

test('it generates clean Mermaid labels without method parameters', function (): void {
    file_put_contents($this->projectPath . '/app/Models/Post.php', <<<'PHP'
    <?php
    namespace App\Models;
    use Illuminate\Database\Eloquent\Model;

    class Post extends Model
    {
        public function comments()
        {
            return $this->hasMany(Comment::class, 'post_id', 'id');
        }
    }
    PHP);

    file_put_contents($this->projectPath . '/app/Models/Comment.php', <<<'PHP'
    <?php
    namespace App\Models;
    use Illuminate\Database\Eloquent\Model;

    class Comment extends Model
    {
        public function post()
        {
            return $this->belongsTo(Post::class, 'post_id', 'id');
        }
    }
    PHP);

    $generator = new ModelOverviewGenerator;
    $result = $generator->generate($this->projectPath, $this->outputPath);

    expect($result['status'])->toBe('success');

    $content = file_get_contents($this->outputPath . '/03-model-overview.md');

    // Mermaid diagram should show clean labels with only relationship names
    expect($content)->toContain('Post ||--o{ Comment : comments');
    expect($content)->toContain('Comment o{--|| Post : post');

    // Should NOT contain verbose method parameters in Mermaid edges
    expect($content)->not->toContain('post_id');
    expect($content)->not->toContain('::class');

    // Relationship table should show clean targets
    expect($content)->toContain('| HASMANY | Comment | comments |');
    expect($content)->toContain('| BELONGSTO | Post | post |');
});
