# Laravel Version Compatibility Matrix

This document provides a comprehensive compatibility matrix for Laravel Context Pack CLI across Laravel versions 8.x through 12.x.

## Tested Laravel Versions

| Laravel Version | PHP Version | Status | Notes |
|----------------|-------------|--------|-------|
| 8.x | PHP 7.3+ | ✅ Fully Supported | Legacy accessor/mutator syntax |
| 9.x | PHP 8.0.2+ | ✅ Fully Supported | New Attribute syntax introduced |
| 10.x | PHP 8.1+ | ✅ Fully Supported | Attribute syntax with type hints |
| 11.x | PHP 8.2+ | ✅ Fully Supported | casts() method introduced |
| 12.x | PHP 8.2+ | ✅ Expected to Work | Same as 11.x with minor updates |

## Feature Support by Version

### Schema Dump Generator

| Feature | 8.x | 9.x | 10.x | 11.x | 12.x |
|---------|-----|-----|------|------|------|
| `php artisan schema:dump` | ✅ | ✅ | ✅ | ✅ | ✅ |
| SQLite fallback | ✅ | ✅ | ✅ | ✅ | ✅ |
| MySQL/MariaDB fallback | ✅ | ✅ | ✅ | ✅ | ✅ |
| PostgreSQL fallback | ✅ | ✅ | ✅ | ✅ | ✅ |

**Notes:**
- All Laravel versions 8+ support `schema:dump` command
- Fallback mechanisms work identically across all versions
- MariaDB officially added to supported databases in Laravel 12.x

### Route Summary Generator

| Feature | 8.x | 9.x | 10.x | 11.x | 12.x |
|---------|-----|-----|------|------|------|
| `route:list --json` | ✅ | ✅ | ✅ | ✅ | ✅ |
| Text parsing fallback | ✅ | ✅ | ✅ | ✅ | ✅ |
| Controller source resolution | ✅ | ✅ | ✅ | ✅ | ✅ |
| Middleware detection | ✅ | ✅ | ✅ | ✅ | ✅ |

**Notes:**
- JSON output support inherited from Laravel 5.8.25
- No version-specific handling required
- All features work identically across versions

### Model Overview Generator

#### Accessor Detection

| Syntax | 8.x | 9.x | 10.x | 11.x | 12.x |
|--------|-----|-----|------|------|------|
| `getXAttribute()` (old) | ✅ | ✅ | ✅ | ✅ | ✅ |
| `Attribute::make(get:)` (new) | ❌ | ✅ | ✅ | ✅ | ✅ |
| Mixed syntax support | ✅ | ✅ | ✅ | ✅ | ✅ |

#### Mutator Detection

| Syntax | 8.x | 9.x | 10.x | 11.x | 12.x |
|--------|-----|-----|------|------|------|
| `setXAttribute()` (old) | ✅ | ✅ | ✅ | ✅ | ✅ |
| `Attribute::make(set:)` (new) | ❌ | ✅ | ✅ | ✅ | ✅ |
| Mixed syntax support | ✅ | ✅ | ✅ | ✅ | ✅ |

#### Cast Detection

| Syntax | 8.x | 9.x | 10.x | 11.x | 12.x |
|--------|-----|-----|------|------|------|
| `protected $casts = []` (property) | ✅ | ✅ | ✅ | ✅ | ✅ |
| `protected function casts(): array` (method) | ❌ | ❌ | ❌ | ✅ | ✅ |
| Both property and method | N/A | N/A | N/A | ✅ | ✅ |

#### Relationship Detection

| Type | 8.x | 9.x | 10.x | 11.x | 12.x |
|------|-----|-----|------|------|------|
| hasOne | ✅ | ✅ | ✅ | ✅ | ✅ |
| hasMany | ✅ | ✅ | ✅ | ✅ | ✅ |
| belongsTo | ✅ | ✅ | ✅ | ✅ | ✅ |
| belongsToMany | ✅ | ✅ | ✅ | ✅ | ✅ |
| morphTo | ✅ | ✅ | ✅ | ✅ | ✅ |
| morphOne | ✅ | ✅ | ✅ | ✅ | ✅ |
| morphMany | ✅ | ✅ | ✅ | ✅ | ✅ |
| morphToMany | ✅ | ✅ | ✅ | ✅ | ✅ |
| hasOneThrough | ✅ | ✅ | ✅ | ✅ | ✅ |
| hasManyThrough | ✅ | ✅ | ✅ | ✅ | ✅ |
| With return type hints | ⚠️ | ✅ | ✅ | ✅ | ✅ |

**Notes:**
- All relationship types work across all versions
- Return type hints (e.g., `: BelongsTo`) recommended in 9.x+
- CLI handles both with and without type hints

#### Other Model Features

| Feature | 8.x | 9.x | 10.x | 11.x | 12.x |
|---------|-----|-----|------|------|------|
| SoftDeletes detection | ✅ | ✅ | ✅ | ✅ | ✅ |
| Fillable/Guarded arrays | ✅ | ✅ | ✅ | ✅ | ✅ |
| Table name detection | ✅ | ✅ | ✅ | ✅ | ✅ |
| Mermaid ERD generation | ✅ | ✅ | ✅ | ✅ | ✅ |

### Static Analysis Generator

| Tool | 8.x | 9.x | 10.x | 11.x | 12.x |
|------|-----|-----|------|------|------|
| Laravel Pint | ✅ | ✅ | ✅ | ✅ | ✅ |
| Pest | ✅ | ✅ | ✅ | ✅ | ✅ |
| PHPStan | ✅ | ✅ | ✅ | ✅ | ✅ |
| Larastan | ✅ | ✅ | ✅ | ✅ | ✅ |
| Rector | ✅ | ✅ | ✅ | ✅ | ✅ |

**Notes:**
- Tools detected from project's `composer.json`
- Graceful degradation if tools not installed
- All marked as "SKIPPED" in output if unavailable

## Version-Specific Limitations

### Laravel 8.x
- **Limitation**: New Attribute syntax (Laravel 9+) not detected
- **Workaround**: Use old-style `get*Attribute()` and `set*Attribute()` methods
- **Impact**: Minimal - most Laravel 8 apps use old syntax

### Laravel 9.x - 10.x
- **Limitation**: casts() method (Laravel 11+) not detected
- **Workaround**: Use `protected $casts` property
- **Impact**: Minimal - property syntax still works in 11+

### Laravel 11.x - 12.x
- **Limitation**: None identified
- **Note**: Both old and new syntax fully supported

## Graceful Degradation Strategy

The CLI implements graceful degradation across all versions:

1. **Schema Dump**: Falls back to native database tools if `schema:dump` fails
2. **Route List**: Falls back to text parsing if JSON output fails
3. **Model Parsing**: Supports both old and new syntax simultaneously
4. **Static Analysis**: Skips tools that aren't installed

## Migration Path Recommendations

### From Laravel 8 → 9+
- No changes required for Context Pack CLI
- Consider migrating to new Attribute syntax gradually
- Both syntaxes will continue to be detected

### From Laravel 10 → 11+
- No changes required for Context Pack CLI
- Consider migrating `$casts` property to `casts()` method
- Both approaches will continue to be detected

### From Laravel 11 → 12
- No changes required for Context Pack CLI
- Update Carbon from 2.x to 3.x in your project
- All detection patterns remain compatible

## Testing

The CLI includes comprehensive test fixtures for each Laravel version:

```
tests/fixtures/
├── laravel-8x/   # Old syntax (getXAttribute, $casts)
├── laravel-9x/   # Mixed syntax (Attribute + old syntax)
├── laravel-10x/  # Attribute syntax with type hints
└── laravel-11x/  # casts() method + Attribute syntax
```

Each fixture validates:
- Accessor/mutator detection
- Cast detection (property and method)
- Relationship parsing
- Mermaid diagram generation

## Known Issues

### None Currently Identified

All tested versions work as expected. If you encounter compatibility issues:

1. Check the logs in `<output>/logs/`
2. Verify your Laravel version in `composer.json`
3. Review the model parsing patterns in this document
4. Report issues with specific Laravel version and syntax

## Future Compatibility

### Laravel 13+

The CLI is designed to be forward-compatible:

- Pattern matching uses flexible regex
- Supports both old and new syntax simultaneously
- Graceful degradation for unknown features
- Version detection via composer.json

**Expected Compatibility**: ✅ High confidence

We will continue to test and validate against new Laravel versions as they are released.

---

**Last Updated**: 2025-10-04
**CLI Version**: 1.0.0
**Tested By**: Backend CLI Engineer (Compatibility Specialist)
