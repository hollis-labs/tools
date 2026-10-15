<?php

namespace App\Models;

use Illuminate\Database\Eloquent\Casts\Attribute;
use Illuminate\Database\Eloquent\Factories\HasFactory;
use Illuminate\Database\Eloquent\Model;

class Account extends Model
{
    use HasFactory;

    protected $fillable = ['account_number', 'balance', 'user_id', 'opened_at'];

    // Laravel 11+ casts() method instead of property
    protected function casts(): array
    {
        return [
            'balance' => 'decimal:2',
            'opened_at' => 'datetime',
            'is_active' => 'boolean',
        ];
    }

    // Laravel 11 Attribute syntax
    protected function maskedAccountNumber(): Attribute
    {
        return Attribute::make(
            get: fn (): string => '****' . substr($this->account_number, -4),
        );
    }

    public function user()
    {
        return $this->belongsTo(User::class);
    }

    public function transactions()
    {
        return $this->hasMany(Transaction::class);
    }
}
