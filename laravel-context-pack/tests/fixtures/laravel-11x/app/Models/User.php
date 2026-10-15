<?php

namespace App\Models;

use Illuminate\Database\Eloquent\Casts\Attribute;
use Illuminate\Database\Eloquent\Factories\HasFactory;
use Illuminate\Foundation\Auth\User as Authenticatable;
use Illuminate\Notifications\Notifiable;

class User extends Authenticatable
{
    use HasFactory, Notifiable;

    protected $fillable = ['name', 'email', 'password'];

    protected $hidden = ['password', 'remember_token'];

    // Laravel 11 casts() method
    protected function casts(): array
    {
        return [
            'email_verified_at' => 'datetime',
            'password' => 'hashed',
        ];
    }

    // Mixed syntax: new Attribute alongside old style
    protected function fullName(): Attribute
    {
        return Attribute::make(
            get: fn (): string => trim($this->first_name . ' ' . $this->last_name),
        );
    }

    // Old style still works in Laravel 11
    public function getInitialsAttribute()
    {
        $parts = explode(' ', $this->name);
        return strtoupper(substr($parts[0] ?? '', 0, 1) . substr($parts[1] ?? '', 0, 1));
    }

    public function accounts()
    {
        return $this->hasMany(Account::class);
    }
}
