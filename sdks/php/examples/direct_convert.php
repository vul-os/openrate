<?php

declare(strict_types=1);

/**
 * DIRECT (in-process) — openrate through the C ABI, no child process, no port,
 * and — for the first half of this script — no packets at all.
 *
 *   php sdks/php/examples/direct_convert.php
 *
 * Environment:
 *   OPENRATE_LIBRARY  path to libopenrate-<goos>-<goarch>.dylib/.so
 *   OPENRATE_NETWORK  set to 1 to also run the refresher section, which fetches
 *                     live rates from the configured sources. Off by default,
 *                     because an example should not open a socket you did not
 *                     ask for.
 *
 * Before you copy this into a web application, read sdks/php/README.md. Under
 * php-fpm this is measurably the wrong shape: use sidecar_convert.php.
 */

require __DIR__ . '/../src/OpenrateException.php';
require __DIR__ . '/../src/Ffi.php';

use Openrate\Ffi;
use Openrate\OpenrateException;

try {
    // openrate_new() builds an ENGINE. It starts no thread, opens no socket,
    // reads no environment variable and sends no packet. There is no code path
    // by which this line touches the network.
    $engine = new Ffi(['base' => 'ZAR', 'quiet' => true]);
} catch (OpenrateException $e) {
    fwrite(STDERR, "could not load libopenrate: {$e->getMessage()}\n");
    exit(1);
}

// try/finally: every path below closes the engine exactly once. Closing an
// engine also releases every refresher built over it, so this single finally
// covers the network section too.
try {
    echo "library : {$engine->libraryPath()}\n";
    echo "abi     : {$engine->version()}\n";
    echo "handles : {$engine->openHandles()} open\n";

    // 1. A fresh engine holds nothing, and says so rather than guessing.
    try {
        $engine->convert('USD', 'ZAR', 100);
        echo "empty   : UNEXPECTED — an empty engine should not convert\n";
    } catch (OpenrateException $e) {
        echo 'empty   : ', str_replace("\n", ' ', $e->getMessage()), "\n";
    }

    // 2. The zero-network path: install rates you obtained yourself. "load" has
    //    no HTTP counterpart, because the server is read-only.
    $loaded = $engine->call('load', [
        'edges' => [
            ['from' => 'USD', 'to' => 'ZAR', 'rate' => 18.42, 'source' => 'my-treasury-desk'],
            ['from' => 'EUR', 'to' => 'USD', 'rate' => 1.0864, 'source' => 'my-treasury-desk'],
        ],
        'built_at' => gmdate('Y-m-d\TH:i:s\Z'),
    ]);
    echo 'loaded  : ', implode(', ', $loaded['currencies']), "\n";

    // 3. Convert. Identical to GET /api/v1/convert, including the rate detail.
    $r = $engine->convert('USD', 'ZAR', 100);
    printf("convert : %s %s = %s %s (rate %.4f, %d hop)\n",
        $r['amount'], $r['from'], $r['result'], $r['to'], $r['rate']['rate'], $r['rate']['hops']);

    // 4. A pair nobody loaded directly — EUR->ZAR is EUR->USD->ZAR. The graph
    //    is the point of openrate.
    $r = $engine->convert('EUR', 'ZAR', 100);
    printf("crossed : 100 EUR = %.2f ZAR via %s\n", $r['result'], implode('→', $r['rate']['path']));

    // 5. The all-pairs snapshot and the metadata, same JSON as /api/v1/*.
    $rates = $engine->call('rates', ['base' => 'ZAR']);
    echo 'rates   : base ', $rates['base'], ', ', count($rates['rates']), " currencies\n";
    $meta = $engine->call('meta');
    echo 'meta    : default base ', $meta['default_base'],
        ', sources ', count($meta['sources']), " (an engine nobody refreshes has none)\n";

    // 6. The split is enforced by the ABI, not by documentation: an engine
    //    handle REFUSES a refresher method. This is why "openrate cannot reach
    //    the network unless you built a refresher" is checkable rather than
    //    promised.
    try {
        $engine->call('refresh', []);
        echo "refuses : UNEXPECTED — an engine should refuse to refresh\n";
    } catch (OpenrateException $e) {
        echo 'refuses : ', str_replace("\n", ' ', $e->getMessage()), "\n";
    }

    // 7. Opt in to the network, explicitly, with a second handle.
    if (getenv('OPENRATE_NETWORK') === '1') {
        $refresher = $engine->refresher(['sources' => 'ecb', 'quiet' => true]);
        echo "handles : {$engine->openHandles()} open (engine + refresher)\n";
        // Building it still opened nothing. THIS opens sockets:
        $status = $refresher->call('refresh', ['timeout_ms' => 20000]);
        foreach ($status['sources'] as $source) {
            // last_error is absent, not null, when a source succeeded.
            $failed = ($source['last_error'] ?? '') !== '';
            printf("fetch   : %s %s\n", $source['name'],
                $failed ? 'error: ' . $source['last_error'] : ($source['edges'] ?? 0) . ' edges');
        }
        $r = $engine->convert('EUR', 'USD', 1);
        printf("live    : 1 EUR = %.4f USD (%s)\n", $r['result'], implode(',', $r['rate']['sources']));
    } else {
        echo "network : skipped (set OPENRATE_NETWORK=1 to fetch from ECB)\n";
    }
} finally {
    // Closing the engine also stops and releases any refresher over it, so a
    // background loop cannot outlive this block.
    $engine->close();
}

echo "closed  : ok, {$engine->openHandles()} handles left open\n";
