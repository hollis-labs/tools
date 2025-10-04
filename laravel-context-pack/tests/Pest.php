<?php

declare(strict_types=1);

use PHPUnit\Framework\ExpectationFailedException;

function withoutException(callable $callback): void
{
    try {
        $callback();
    } catch (ExpectationFailedException $exception) {
        throw $exception;
    } catch (Throwable $exception) {
        test()->fail($exception->getMessage());
    }
}
