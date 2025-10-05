# Laravel Version Differences (8.x - 12.x)

This document outlines key differences across Laravel versions 8.x through 12.x that impact the Laravel Context Pack CLI tool.

## PHP Requirements

| Laravel Version | Minimum PHP Version | Notes |
|----------------|---------------------|-------|
| 8.x | PHP 7.3.0 | |
| 9.x | PHP 8.0.2 | First version requiring PHP 8.0+ |
| 10.x | PHP 8.1.0 | Also requires Composer 2.2.0+ |
| 11.x | PHP 8.2.0 | |
| 12.x | PHP 8.2.0 | Requires Carbon 3.x |

## Artisan Command Availability

### schema:dump Command

**Introduced:** Laravel 8.x (PR #32275)
**Status:** Available in all versions 8.x - 12.x

**Supported Databases:**
- Laravel 8.x - 11.x: MySQL, PostgreSQL, SQLite
- Laravel 12.x: MariaDB, MySQL, PostgreSQL, SQLite (MariaDB added)

**Usage:**
```bash
php artisan schema:dump
php artisan schema:dump --prune
```

**Options:**
- `--prune`: Delete existing migrations after dumping (all versions)
- `--schema-path`: Specify custom output path (requires verification per version)

**Implementation Notes:**
- Uses database-specific tools (mysqldump, pg_dump, sqlite3)
- Writes to `database/schema/` directory by default
- Schema dumps may not be restored to in-memory SQLite databases
- Context Pack CLI already implements fallback strategies for all supported databases

### route:list Command

**JSON Output Support:**
**Introduced:** Laravel 5.8.25 (PR #28894)
**Status:** Available in all versions 8.x - 12.x

**Usage:**
```bash
php artisan route:list --json
```

**Implementation Notes:**
- JSON output is consistently available across all target versions
- Context Pack CLI already implements fallback to text parsing if JSON fails
- No version-specific handling needed for route:list

## Eloquent Model Syntax Changes

### Accessors & Mutators

**Laravel 8.x (Old Syntax):**
```php
// Accessor
public function getFullNameAttribute() {
    return "{$this->first_name} {$this->last_name}";
}

// Mutator
public function setFullNameAttribute($value) {
    [$this->first_name, $this->last_name] = explode(' ', $value);
}
```

**Laravel 9.x+ (New Syntax):**
```php
use Illuminate\Database\Eloquent\Casts\Attribute;

protected function fullName(): Attribute {
    return Attribute::make(
        get: fn (string $value) => ucfirst($value),
        set: fn (string $value) => strtolower($value),
    );
}
```

**Impact on Context Pack CLI:**
- ModelOverviewGenerator uses regex parsing to extract accessors/mutators
- Current patterns: `/function\s+get([A-Za-z0-9_]+)Attribute/` and `/function\s+set([A-Za-z0-9_]+)Attribute/`
- **REQUIRED:** Add detection for new Attribute::make() syntax (Laravel 9+)
- Both patterns may coexist in same codebase during migration periods

### $casts Property vs casts() Method

**Laravel 8.x - 10.x:**
```php
protected $casts = [
    'email_verified_at' => 'datetime',
    'is_admin' => 'boolean',
];
```

**Laravel 11.x - 12.x:**
```php
protected function casts(): array {
    return [
        'email_verified_at' => 'datetime',
        'is_admin' => 'boolean',
    ];
}
```

**Impact on Context Pack CLI:**
- ModelOverviewGenerator currently extracts `protected $casts` property
- **REQUIRED:** Add extraction for `casts()` method (Laravel 11+)
- Both patterns may coexist in same codebase

## Breaking Changes by Version

### Laravel 9.x
- PHP 8.0.2+ required
- Flysystem 3.x (filesystem driver changes)
- First-party packages received major version bumps

### Laravel 10.x
- PHP 8.1.0+ required
- `lang/` directory no longer included by default (use `php artisan lang:publish`)
- All `make:*` commands now prompt for required arguments
- `Bus::dispatchNow()` removed (use `Bus::dispatchSync()`)
- New `--profile` option for `php artisan test` command

### Laravel 11.x
- PHP 8.2.0+ required
- New default application structure with fewer default files
- `api.php` and `channels.php` routes not present by default
- Migration files no longer auto-loaded from package directories
- Per-second rate limiting (vs per-minute)
- Service provider consolidation

### Laravel 12.x
- Carbon 3.x required (Carbon 2.x removed)
- Several Laravel 11 deprecated methods removed
- `DatabaseTokenRepository` constructor `$expires` parameter now expects seconds (not minutes)

## Application Structure Differences

### Route Files (Laravel 11+)
- `api.php` and `channels.php` no longer present by default
- Can be created with artisan commands if needed
- May impact route detection if apps don't have these files

### Migrations (Laravel 11+)
- Packages no longer auto-load migrations
- Must publish migrations explicitly: `php artisan vendor:publish --tag=passport-migrations`

### Service Providers (Laravel 11+)
- Consolidated into fewer providers
- May impact auto-discovery of features

## Recommendations for Context Pack CLI

### Critical Compatibility Issues

1. **ModelOverviewGenerator Enhancements:**
   - Add detection for Attribute::make() syntax (Laravel 9+)
   - Add extraction for casts() method (Laravel 11+)
   - Support both old and new patterns simultaneously

2. **Version Detection:**
   - Parse `composer.json` in target Laravel project
   - Extract Laravel framework version
   - Use version to adapt extraction patterns

3. **Graceful Degradation:**
   - All current commands (schema:dump, route:list --json) work across all versions
   - Fallback mechanisms already in place
   - Focus on model syntax detection improvements

### Low Priority (Already Compatible)

1. **SchemaDumpGenerator:**
   - Already has fallback mechanisms
   - Works across all versions (8.x - 12.x)
   - No changes needed

2. **RouteSummaryGenerator:**
   - JSON support available in all target versions
   - Text parsing fallback already implemented
   - No changes needed

### Testing Strategy

1. Create minimal test fixtures for each Laravel version (8.x, 9.x, 10.x, 11.x, 12.x)
2. Focus fixtures on model syntax variations
3. Test model parsing across different accessor/mutator/cast syntaxes
4. Verify metadata generation includes Laravel version

## Version Support Matrix

| Feature | 8.x | 9.x | 10.x | 11.x | 12.x | Notes |
|---------|-----|-----|------|------|------|-------|
| schema:dump | ✅ | ✅ | ✅ | ✅ | ✅ | All versions supported |
| route:list --json | ✅ | ✅ | ✅ | ✅ | ✅ | All versions supported |
| Old accessor syntax | ✅ | ✅ | ✅ | ✅ | ✅ | Still supported |
| New Attribute syntax | ❌ | ✅ | ✅ | ✅ | ✅ | Need to add detection |
| $casts property | ✅ | ✅ | ✅ | ✅ | ✅ | Still supported |
| casts() method | ❌ | ❌ | ❌ | ✅ | ✅ | Need to add detection |

✅ = Fully supported | ❌ = Not available | ⚠️ = Partial/requires fallback
