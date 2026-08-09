<?php

declare(strict_types=1);

/**
 * The fork probe — evidence, not a claim.
 *
 * libopenrate puts the Go runtime in your process, and the Go runtime does not
 * survive fork() without exec(). php-fpm is a pre-forking server. This makes
 * the collision reproducible on your own machine:
 *
 *   php sdks/php/examples/fork_probe.php before convert   # exited 0
 *   php sdks/php/examples/fork_probe.php before refresh   # HUNG      (needs network)
 *   php sdks/php/examples/fork_probe.php after  refresh   # on darwin: CRASHED
 *
 * openrate's shape makes this sharper than it is for most libraries. Every
 * ENGINE method — convert, rates, meta, load — is arithmetic over a snapshot
 * already in memory: no sockets, no timers, nothing the Go scheduler has to
 * wake. Those survive a fork. Only the REFRESHER touches the network, and that
 * is the handle that hangs.
 *
 * So "it worked in my forked worker" is not evidence of anything, unless the
 * thing you tried was a refresh.
 *
 * And note the third line. "Load the library after the fork" is the usual
 * mitigation, and on macOS it is NOT sufficient for anything doing TLS: the
 * child segfaults inside crypto/x509's SecTrustEvaluateWithError, because
 * Apple's Security framework is itself fork-unsafe once the parent has touched
 * it. Only exec() clears that. Linux, where Go verifies certificates without
 * calling into the system, has not been tested here.
 *
 * Requires ext-pcntl (CLI only). Environment: OPENRATE_LIBRARY.
 */

require __DIR__ . '/../src/OpenrateException.php';
require __DIR__ . '/../src/Ffi.php';

use Openrate\Ffi;
use Openrate\OpenrateException;

if (!function_exists('pcntl_fork')) {
    fwrite(STDERR, "ext-pcntl is required (it is CLI-only and not built into every PHP)\n");
    exit(2);
}

$when = $argv[1] ?? 'before';       // before | after   (relative to the fork)
$method = $argv[2] ?? 'convert';    // convert | rates | meta | refresh
$timeout = (float) ($argv[3] ?? 15.0);

if (!in_array($when, ['before', 'after'], true)) {
    fwrite(STDERR, "usage: fork_probe.php [before|after] [convert|rates|meta|refresh] [timeout_seconds]\n");
    exit(2);
}

$seed = [
    'edges' => [['from' => 'USD', 'to' => 'ZAR', 'rate' => 18.42, 'source' => 'seed']],
    'built_at' => gmdate('Y-m-d\TH:i:s\Z'),
];

/** Build an engine (and, for a refresh probe, its refresher) and run $method. */
$exercise = static function (string $method, array $seed): string {
    $engine = new Ffi(['quiet' => true]);
    try {
        $engine->call('load', $seed);
        if ($method === 'refresh') {
            // The only handle that can open a socket, and the only one the fork
            // hazard actually reaches.
            $refresher = $engine->refresher(['sources' => 'ecb', 'quiet' => true]);

            return $refresher->callRaw('refresh', ['timeout_ms' => 10000]);
        }

        // '{}' rather than [], because an empty PHP array encodes as a JSON
        // array and openrate wants an object.
        $request = $method === 'convert'
            ? ['from' => 'USD', 'to' => 'ZAR', 'amount' => 1]
            : '{}';

        return $engine->callRaw($method, $request);
    } finally {
        // Closing the engine releases the refresher too.
        $engine->close();
    }
};

$parent = null;
if ($when === 'before') {
    // The php-fpm shape: the library is loaded in the parent, and every worker
    // is a fork() of it. opcache.preload does exactly this.
    $parent = new Ffi(['quiet' => true]);
    $parent->call('load', $seed);
}

$pid = pcntl_fork();
if ($pid === -1) {
    fwrite(STDERR, "fork failed\n");
    exit(2);
}

if ($pid === 0) {
    // ---- child ("worker") --------------------------------------------------
    try {
        if ($when === 'before' && $method !== 'refresh') {
            $bytes = strlen($parent->callRaw($method, $method === 'convert'
                ? ['from' => 'USD', 'to' => 'ZAR', 'amount' => 1]
                : '{}'));
        } elseif ($when === 'before') {
            $refresher = $parent->refresher(['sources' => 'ecb', 'quiet' => true]);
            $bytes = strlen($refresher->callRaw('refresh', ['timeout_ms' => 10000]));
        } else {
            $bytes = strlen($exercise($method, $seed));
        }
        fwrite(STDERR, "  child: {$method} returned {$bytes} bytes\n");
        exit(0);
    } catch (OpenrateException $e) {
        fwrite(STDERR, "  child: ERROR {$e->getMessage()}\n");
        exit(1);
    }
}

// ---- parent ("master") -----------------------------------------------------
try {
    $deadline = microtime(true) + $timeout;
    while (microtime(true) < $deadline) {
        $status = 0;
        if (pcntl_waitpid($pid, $status, WNOHANG) === $pid) {
            $verdict = pcntl_wifexited($status)
                ? 'exited ' . pcntl_wexitstatus($status)
                : 'CRASHED, signal ' . pcntl_wtermsig($status) . ' (Go dumped above)';
            printf("load=%-6s method=%-7s -> child %s\n", $when, $method, $verdict);
            exit(0);
        }
        usleep(50_000);
    }
    posix_kill($pid, SIGKILL);
    pcntl_waitpid($pid, $status);
    printf("load=%-6s method=%-7s -> child HUNG (SIGKILLed after %.0fs)\n", $when, $method, $timeout);
} finally {
    $parent?->close();
}
