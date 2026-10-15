<?php

namespace App\Models;

use Illuminate\Database\Eloquent\Casts\Attribute;
use Illuminate\Database\Eloquent\Factories\HasFactory;
use Illuminate\Database\Eloquent\Model;

class Product extends Model
{
    use HasFactory;

    protected $fillable = ['name', 'price', 'stock', 'category_id'];

    protected $casts = [
        'price' => 'decimal:2',
        'stock' => 'integer',
        'is_available' => 'boolean',
    ];

    // Laravel 10 Attribute syntax with type hints
    protected function formattedPrice(): Attribute
    {
        return Attribute::make(
            get: fn (): string => '$' . number_format($this->price, 2),
        );
    }

    public function category()
    {
        return $this->belongsTo(Category::class);
    }

    public function reviews()
    {
        return $this->hasMany(Review::class);
    }
}
