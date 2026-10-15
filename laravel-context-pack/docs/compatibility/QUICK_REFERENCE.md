# Laravel Version Compatibility - Quick Reference

Quick reference guide for Laravel Context Pack CLI version compatibility.

## Supported Versions

| Version | PHP | Status | Key Feature |
|---------|-----|--------|-------------|
| 8.x | 7.3+ | ✅ | Old accessor/mutator syntax |
| 9.x | 8.0+ | ✅ | Attribute::make() introduced |
| 10.x | 8.1+ | ✅ | Type hints enhanced |
| 11.x | 8.2+ | ✅ | casts() method added |
| 12.x | 8.2+ | ✅ | Latest features |

## What Works Where

### ✅ Works Everywhere (8.x - 12.x)

- `php artisan schema:dump`
- `php artisan route:list --json`
- Old accessor syntax: `getXAttribute()`
- Old mutator syntax: `setXAttribute()`
- Property casts: `protected $casts = []`
- All relationship types
- SoftDeletes detection
- Mermaid ERD generation

### ✅ Laravel 9+ Only

- New Attribute syntax: `Attribute::make(get:, set:)`
- Both old and new syntax in same codebase

### ✅ Laravel 11+ Only

- Method casts: `protected function casts(): array`
- Property OR method casts (both supported)

## Model Syntax Examples

### Laravel 8.x (Old Style)

```php
// Accessor
public function getFullNameAttribute() {
    return $this->first_name . ' ' . $this->last_name;
}

// Mutator
public function setEmailAttribute($value) {
    $this->attributes['email'] = strtolower($value);
}

// Casts
protected $casts = [
    'created_at' => 'datetime',
];
```

### Laravel 9.x+ (New Style)

```php
use Illuminate\Database\Eloquent\Casts\Attribute;

// Accessor + Mutator
protected function email(): Attribute {
    return Attribute::make(
        get: fn (string $value) => $value,
        set: fn (string $value) => strtolower($value),
    );
}

// Casts (still using property)
protected $casts = [
    'created_at' => 'datetime',
];
```

### Laravel 11.x+ (Method Casts)

```php
// New casts() method
protected function casts(): array {
    return [
        'created_at' => 'datetime',
        'is_active' => 'boolean',
    ];
}
```

## Do I Need to Change Anything?

**Short answer: No.**

The CLI automatically detects your Laravel version and adapts:

- Using Laravel 8 with old syntax? ✅ Detected
- Using Laravel 9+ with new syntax? ✅ Detected
- Using Laravel 11+ with casts() method? ✅ Detected
- Mixed old and new syntax? ✅ Both detected

## Troubleshooting

### Models not detected

**Check**: Is your models directory at `app/Models`?
**Fix**: Ensure models are in `app/Models/` (Laravel 8+ convention)

### Accessors/mutators missing

**Check**: Syntax matches your Laravel version?
**Fix**: Use appropriate syntax for your version (see examples above)

### Relationships not showing

**Check**: Are they using standard Eloquent relationship methods?
**Fix**: Ensure methods return `$this->hasMany()`, `$this->belongsTo()`, etc.

### Schema dump failing

**Check**: Is `schema:dump` available?
**Fallback**: CLI automatically uses sqlite3/mysqldump/pg_dump
**Fix**: Check logs in `<output>/logs/schema-dump.log`

## Quick Diagnostic

Run this in your Laravel project:

```bash
# Check Laravel version
composer show laravel/framework | grep versions

# Test CLI (safe - won't modify anything)
php /path/to/context-pack.php generate --project=. --output=/tmp/test-pack

# Check generated output
ls -la /tmp/test-pack/
cat /tmp/test-pack/metadata.json
```

## Need Help?

1. Check logs in `<output>/logs/`
2. Review [COMPATIBILITY_MATRIX.md](COMPATIBILITY_MATRIX.md)
3. See [VERSION_DIFFERENCES.md](VERSION_DIFFERENCES.md) for details
4. Check test fixtures in `tests/fixtures/` for examples

## Cheat Sheet

```bash
# Supported everywhere
✅ schema:dump
✅ route:list --json
✅ getXAttribute() / setXAttribute()
✅ protected $casts = []
✅ All relationships

# Laravel 9+ only
✅ Attribute::make(get:, set:)

# Laravel 11+ only
✅ protected function casts(): array

# Not supported
❌ Route files in non-standard locations
❌ Models outside app/Models/
❌ Custom relationship methods (non-Eloquent)
```

## Migration Checklist

### Upgrading Laravel Version?

- [ ] No action needed - CLI adapts automatically
- [ ] Old syntax still works in newer Laravel
- [ ] New syntax not detected in older Laravel
- [ ] Check `metadata.json` for detected version
- [ ] Review logs if unexpected behavior

### Best Practices

- ✅ Use consistent syntax within a model
- ✅ Migrate incrementally (model by model)
- ✅ Test generated context packs after upgrade
- ✅ Keep CLI updated to latest version

---

**TL;DR**: CLI works with Laravel 8-12 automatically. No config needed. Old syntax works everywhere. New syntax detected in Laravel 9+.
