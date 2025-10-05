<?php

namespace App\Models;

use Illuminate\Database\Eloquent\Casts\Attribute;
use Illuminate\Database\Eloquent\Model;

class Transaction extends Model
{
    protected $fillable = ['account_id', 'amount', 'type', 'description', 'processed_at'];

    // Laravel 11+ casts() method
    protected function casts(): array
    {
        return [
            'amount' => 'decimal:2',
            'processed_at' => 'datetime',
        ];
    }

    protected function formattedAmount(): Attribute
    {
        return Attribute::make(
            get: fn (): string => $this->type === 'credit' ? '+$' . $this->amount : '-$' . $this->amount,
        );
    }

    public function account()
    {
        return $this->belongsTo(Account::class);
    }
}
