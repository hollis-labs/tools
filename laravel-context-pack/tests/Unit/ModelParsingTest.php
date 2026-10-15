<?php

declare(strict_types=1);

use LaravelContextPack\Generator\ModelOverviewGenerator;

describe('Model Parsing', function () {
    test('detects old-style accessors', function () {
        $generator = new ModelOverviewGenerator;
        $reflection = new ReflectionClass($generator);
        $method = $reflection->getMethod('extractAccessors');
        $method->setAccessible(true);

        $code = <<<'PHP'
public function getFullNameAttribute() {
    return $this->first_name . ' ' . $this->last_name;
}
PHP;

        $result = $method->invoke($generator, $code);
        expect($result)->toContain('fullName');
    });

    test('detects new-style Attribute accessors', function () {
        $generator = new ModelOverviewGenerator;
        $reflection = new ReflectionClass($generator);
        $method = $reflection->getMethod('extractAccessors');
        $method->setAccessible(true);

        $code = <<<'PHP'
protected function fullName(): Attribute {
    return Attribute::make(
        get: fn () => $this->first_name . ' ' . $this->last_name,
    );
}
PHP;

        $result = $method->invoke($generator, $code);
        expect($result)->toContain('fullName');
    });

    test('detects old-style mutators', function () {
        $generator = new ModelOverviewGenerator;
        $reflection = new ReflectionClass($generator);
        $method = $reflection->getMethod('extractMutators');
        $method->setAccessible(true);

        $code = <<<'PHP'
public function setEmailAttribute($value) {
    $this->attributes['email'] = strtolower($value);
}
PHP;

        $result = $method->invoke($generator, $code);
        expect($result)->toContain('email');
    });

    test('detects new-style Attribute mutators', function () {
        $generator = new ModelOverviewGenerator;
        $reflection = new ReflectionClass($generator);
        $method = $reflection->getMethod('extractMutators');
        $method->setAccessible(true);

        $code = <<<'PHP'
protected function email(): Attribute {
    return Attribute::make(
        get: fn (string $value) => $value,
        set: fn (string $value) => strtolower($value),
    );
}
PHP;

        $result = $method->invoke($generator, $code);
        expect($result)->toContain('email');
    });

    test('detects casts from property', function () {
        $generator = new ModelOverviewGenerator;
        $reflection = new ReflectionClass($generator);
        $method = $reflection->getMethod('extractAssociativeArray');
        $method->setAccessible(true);

        $code = <<<'PHP'
protected $casts = [
    'email_verified_at' => 'datetime',
    'is_admin' => 'boolean',
];
PHP;

        $result = $method->invoke($generator, $code, 'casts');
        expect($result)->toHaveKey('email_verified_at', 'datetime');
        expect($result)->toHaveKey('is_admin', 'boolean');
    });

    test('detects casts from method', function () {
        $generator = new ModelOverviewGenerator;
        $reflection = new ReflectionClass($generator);
        $method = $reflection->getMethod('extractAssociativeArray');
        $method->setAccessible(true);

        $code = <<<'PHP'
protected function casts(): array {
    return [
        'balance' => 'decimal:2',
        'opened_at' => 'datetime',
    ];
}
PHP;

        $result = $method->invoke($generator, $code, 'casts');
        expect($result)->toHaveKey('balance', 'decimal:2');
        expect($result)->toHaveKey('opened_at', 'datetime');
    });
});
