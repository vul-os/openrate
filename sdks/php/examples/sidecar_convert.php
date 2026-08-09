<?php

declare(strict_types=1);

/**
 * SIDECAR (out-of-process) — the SDK spawns the `openrate` binary on a loopback
 * port, waits for /healthz, and shuts it down at exit. You never run a server
 * by hand.
 *
 *   php sdks/php/examples/sidecar_convert.php
 *
 * Environment:
 *   OPENRATE_BINARY   path to the openrate binary (default: bundled bin/openrate, then PATH)
 *   OPENRATE_SOURCES  comma-separated FX sources (default: ecb,coinbase,luno,sarb)
 *
 * This is the recommended shape for PHP. sdks/php/README.md explains why, with
 * the php-fpm measurements behind the claim.
 *
 * Note what is different from direct mode: the sidecar HAS a refresher, by
 * construction — `openrate serve` is the program whose job is to fetch. If you
 * want a process that provably never reaches the network, that is the engine
 * in direct mode, not this.
 */

require __DIR__ . '/../src/OpenrateException.php';
require __DIR__ . '/../src/Openrate.php';

use Openrate\Openrate;
use Openrate\OpenrateException;

$sources = getenv('OPENRATE_SOURCES') ?: 'ecb';

try {
    $base = Openrate::start(['sources' => $sources, 'ui' => false, 'timeout' => 60.0]);
    echo "sidecar : {$base}\n";
} catch (OpenrateException $e) {
    fwrite(STDERR, "could not start the sidecar: {$e->getMessage()}\n");
    exit(1);
}

// try/finally, so a failure below still stops the child rather than leaving an
// orphaned server on a port.
try {
    // The server starts answering /healthz before its first fetch completes, so
    // wait for the book to be populated rather than racing it.
    $deadline = microtime(true) + 60.0;
    $meta = Openrate::meta();
    while (count($meta['currencies'] ?? []) === 0 && microtime(true) < $deadline) {
        usleep(250_000);
        $meta = Openrate::meta();
    }

    echo 'meta    : base ', $meta['default_base'],
        ', ', count($meta['currencies']), ' currencies from ', count($meta['sources']), " source(s)\n";
    foreach ($meta['sources'] as $source) {
        $failed = ($source['last_error'] ?? '') !== '';
        printf("source  : %-10s %s\n", $source['name'],
            $failed ? 'error: ' . $source['last_error'] : ($source['edges'] ?? 0) . ' edges');
    }

    // 1. Convert — the same JSON document the C ABI's "convert" method returns.
    $r = Openrate::convert('EUR', 'USD', 100);
    printf("convert : %s %s = %.4f %s (rate %.6f, %d hop, %s)\n",
        $r['amount'], $r['from'], $r['result'], $r['to'],
        $r['rate']['rate'], $r['rate']['hops'], implode(',', $r['rate']['sources']));

    // 2. A cross-rate the sources do not publish directly.
    $r = Openrate::convert('EUR', 'ZAR', 100);
    printf("crossed : 100 EUR = %.2f ZAR via %s\n", $r['result'], implode('→', $r['rate']['path']));

    // 3. The all-pairs snapshot.
    $rates = Openrate::rates('ZAR');
    $sample = array_slice(array_keys($rates['rates']), 0, 5);
    echo 'rates   : base ', $rates['base'], ', ', count($rates['rates']),
        ' currencies (', implode(', ', $sample), ", …)\n";

    // 4. The error path. Over HTTP an error is a status code and a JSON body,
    //    where the C ABI hands back a plain string in *err.
    try {
        Openrate::convert('XXX', 'YYY', 1);
        echo "error   : UNEXPECTED — an unknown pair should fail\n";
    } catch (OpenrateException $e) {
        echo 'error   : ', str_replace("\n", ' ', $e->getMessage()), "\n";
    }
} finally {
    // The shutdown hook would do this too; doing it explicitly frees the port
    // the moment we are done, on the throw path as well.
    Openrate::stop();
}

echo "stopped : ok\n";
