<?php

declare(strict_types=1);

namespace Openrate;

/**
 * openrate as a managed sidecar: the SDK spawns the `openrate` binary on a
 * loopback port, waits for it to be READY — /readyz, not /healthz, so the first
 * conversion has rates to work with — and terminates it at shutdown. You never
 * run a server by hand.
 *
 *   use Openrate\Openrate;
 *
 *   $base = Openrate::baseUrl();     // http://127.0.0.1:<port>
 *   $r = Openrate::convert('USD', 'ZAR', 100);
 *   echo $r['result'];
 *
 * This is the recommended mode for PHP. See README.md — php-fpm forks its
 * workers and the Go runtime inside libopenrate does not survive fork().
 */
final class Openrate
{
    public const VERSION = '0.1.2';

    /** @var resource|null */
    private static $proc = null;

    /** @var string|null */
    private static $base = null;

    /** @var bool */
    private static $shutdownRegistered = false;

    /**
     * Start the sidecar (idempotent). Returns the base URL (http://host:port).
     *
     * It returns when the child is READY, not merely listening: /readyz is 503
     * until the snapshot has currencies in it, so 'timeout' has to cover the
     * first fetch. A server that comes up and never gets a rate throws with the
     * reason /readyz gave — "ecb: connection refused" — rather than a bare
     * deadline.
     *
     * @param array{port?:int,base?:string,sources?:string,refresh?:string,ui?:bool,ratelimit?:int,env?:array<string,string>,timeout?:float} $opts
     */
    public static function start(array $opts = []): string
    {
        if (self::running()) {
            return self::$base;
        }

        $port = $opts['port'] ?? self::freePort();
        $addr = "127.0.0.1:{$port}";

        $env = self::inheritedEnv();
        $env['OPENRATE_ADDR'] = $addr;
        // The binary defaults to 120 API requests/minute per IP, which is
        // anti-scraping for a PUBLIC deployment. This child listens on loopback
        // and serves exactly one client — us — so there is no stranger here to
        // throttle, while a legitimate batch of conversions would sail past
        // 120/min and take a 429 from our own sidecar. Default it off here and
        // let a caller who wants it back pass ['ratelimit' => 120]. (Readiness
        // polling is not the reason: /readyz is outside /api/ and the limiter
        // never sees it.)
        $env['OPENRATE_RATELIMIT'] = (string) ($opts['ratelimit'] ?? 0);
        if (isset($opts['base'])) {
            $env['OPENRATE_BASE'] = $opts['base'];
        }
        if (isset($opts['sources'])) {
            $env['OPENRATE_SOURCES'] = $opts['sources'];
        }
        if (isset($opts['refresh'])) {
            $env['OPENRATE_REFRESH'] = $opts['refresh'];
        }
        if (isset($opts['ui'])) {
            $env['OPENRATE_UI'] = $opts['ui'] ? 'true' : 'false';
        }
        if (isset($opts['env'])) {
            $env = array_merge($env, $opts['env']);
        }

        $descriptors = [0 => STDIN, 1 => STDOUT, 2 => STDERR];

        $proc = proc_open([self::binaryPath()], $descriptors, $pipes, null, $env);
        if (!\is_resource($proc)) {
            throw new OpenrateException('failed to spawn the openrate binary');
        }
        self::$proc = $proc;
        self::$base = "http://{$addr}";

        try {
            self::waitReady(self::$base, (float) ($opts['timeout'] ?? 30.0));
        } catch (\Throwable $e) {
            self::stop();
            throw $e;
        }

        if (!self::$shutdownRegistered) {
            register_shutdown_function([self::class, 'stop']);
            self::$shutdownRegistered = true;
        }

        return self::$base;
    }

    /** The running base URL, starting the sidecar if needed. */
    public static function baseUrl(): string
    {
        return self::running() ? self::$base : self::start();
    }

    /** The JSON API base (…/api/v1). */
    public static function apiBaseUrl(): string
    {
        return self::baseUrl() . '/api/v1';
    }

    /**
     * GET /api/v1/convert.
     *
     * @return array<string,mixed>
     */
    public static function convert(string $from, string $to, float $amount = 1.0): array
    {
        return self::get('/convert?' . http_build_query([
            'from' => $from,
            'to' => $to,
            'amount' => $amount,
        ]));
    }

    /**
     * GET /api/v1/rates.
     *
     * @return array<string,mixed>
     */
    public static function rates(?string $base = null): array
    {
        return self::get('/rates' . ($base !== null ? '?' . http_build_query(['base' => $base]) : ''));
    }

    /**
     * GET /api/v1/meta.
     *
     * @return array<string,mixed>
     */
    public static function meta(): array
    {
        return self::get('/meta');
    }

    /**
     * GET /healthz — liveness. True the moment the process is listening, which
     * is BEFORE it has any rates. Do not convert on the strength of it.
     */
    public static function healthy(): bool
    {
        return self::probe('/healthz');
    }

    /**
     * GET /readyz — readiness. True once a conversion would actually succeed.
     *
     * A managed sidecar is already ready by the time start() returns; this is
     * for the other case, a server you were merely pointed at, which can be up
     * and still empty.
     */
    public static function ready(): bool
    {
        return self::probe('/readyz');
    }

    /**
     * Block until /readyz says ready, or throw with the reason it gave. The
     * same poll start() uses, not a second implementation of readiness.
     */
    public static function waitReadyFor(float $timeout = 30.0): void
    {
        self::waitReady(self::baseUrl(), $timeout);
    }

    /**
     * One GET against the JSON API. $path is relative to /api/v1.
     *
     * @return array<string,mixed>
     */
    public static function get(string $path): array
    {
        $url = self::apiBaseUrl() . $path;
        $ctx = stream_context_create(['http' => ['timeout' => 30, 'ignore_errors' => true]]);
        $body = @file_get_contents($url, false, $ctx);
        if ($body === false) {
            throw new OpenrateException("GET {$path} failed");
        }
        $status = self::parseStatus(self::lastResponseHeaders($http_response_header ?? null) ?? []);
        $decoded = json_decode($body, true);
        if ($status !== 200) {
            $message = \is_array($decoded) && isset($decoded['error'])
                ? (string) $decoded['error']
                : substr($body, 0, 200);
            throw new OpenrateException("GET {$path}: HTTP {$status}: {$message}");
        }
        if (!\is_array($decoded)) {
            throw new OpenrateException("GET {$path}: response was not a JSON object");
        }

        return $decoded;
    }

    /** Stop the sidecar if running. Idempotent. */
    public static function stop(): void
    {
        if (\is_resource(self::$proc)) {
            $status = proc_get_status(self::$proc);
            if ($status['running']) {
                proc_terminate(self::$proc, \defined('SIGTERM') ? SIGTERM : 15);
                $deadline = microtime(true) + 5.0;
                while (microtime(true) < $deadline) {
                    if (!proc_get_status(self::$proc)['running']) {
                        break;
                    }
                    usleep(50_000);
                }
                if (proc_get_status(self::$proc)['running']) {
                    proc_terminate(self::$proc, \defined('SIGKILL') ? SIGKILL : 9);
                }
            }
            proc_close(self::$proc);
        }
        self::$proc = null;
        self::$base = null;
    }

    // ---------------------------------------------------------------- internals

    /**
     * One probe request against a non-/api/ path. Neither /healthz nor /readyz
     * is rate-limited, so this costs the caller nothing.
     */
    private static function probe(string $path): bool
    {
        $ctx = stream_context_create(['http' => ['timeout' => 2, 'ignore_errors' => true]]);
        $body = @file_get_contents(self::baseUrl() . $path, false, $ctx);
        $headers = self::lastResponseHeaders($http_response_header ?? null);

        return $body !== false && $headers !== null && self::parseStatus($headers) === 200;
    }

    private static function running(): bool
    {
        if (!\is_resource(self::$proc)) {
            return false;
        }

        return (bool) proc_get_status(self::$proc)['running'];
    }

    private static function binaryPath(): string
    {
        $env = getenv('OPENRATE_BINARY');
        if ($env !== false && $env !== '') {
            return $env;
        }

        $name = self::isWindows() ? 'openrate.exe' : 'openrate';
        $bundled = \dirname(__DIR__) . DIRECTORY_SEPARATOR . 'bin' . DIRECTORY_SEPARATOR . $name;
        if (is_file($bundled)) {
            return $bundled;
        }

        $found = self::which('openrate');
        if ($found !== null) {
            return $found;
        }

        throw new OpenrateException(
            'openrate binary not found. Set OPENRATE_BINARY, install a platform ' .
            'package, or build it: `go build -o sdks/php/bin/openrate ./cmd/openrate`'
        );
    }

    private static function which(string $cmd): ?string
    {
        $exts = self::isWindows() ? explode(';', (string) getenv('PATHEXT')) : [''];
        foreach (explode(PATH_SEPARATOR, (string) getenv('PATH')) as $dir) {
            foreach ($exts as $ext) {
                $candidate = $dir . DIRECTORY_SEPARATOR . $cmd . $ext;
                if (is_file($candidate) && is_executable($candidate)) {
                    return $candidate;
                }
            }
        }

        return null;
    }

    private static function isWindows(): bool
    {
        return \PHP_OS_FAMILY === 'Windows';
    }

    /** @return array<string,string> */
    private static function inheritedEnv(): array
    {
        $env = [];
        foreach ($_ENV as $k => $v) {
            $env[(string) $k] = (string) $v;
        }
        if ($env === []) {
            foreach (\array_keys(getenv()) as $k) {
                $env[(string) $k] = (string) getenv((string) $k);
            }
        }

        return $env;
    }

    private static function freePort(): int
    {
        $sock = @stream_socket_server('tcp://127.0.0.1:0', $errno, $errstr);
        if ($sock === false) {
            throw new OpenrateException("could not allocate a free port: {$errstr}");
        }
        $name = (string) stream_socket_get_name($sock, false);
        fclose($sock);

        return (int) substr($name, (int) strrpos($name, ':') + 1);
    }

    /**
     * Poll GET /readyz until the server can actually answer a conversion.
     *
     * Not /healthz. /healthz answers the instant the listener binds, before any
     * source has been fetched, so a caller that waits on it converts against an
     * empty book and gets "unknown or unreachable currency pair" for every pair
     * — a false green wearing a bad-currency-code costume.
     *
     * 150 ms fixed, and no backoff: /readyz sits outside /api/, so the per-IP
     * limiter never sees it and there is no budget to spend by polling.
     *
     * On timeout the caller gets the cause, not a deadline: whatever the last
     * 503 body said, or the transport error if the server never answered.
     */
    private static function waitReady(string $base, float $timeout): void
    {
        $deadline = microtime(true) + $timeout;
        $detail = null;      // from the last 503 body
        $transport = 'connection refused';
        // ignore_errors, or the 503 arrives as `false` with its body — the part
        // that says WHY — thrown away.
        $ctx = stream_context_create(['http' => ['timeout' => 2, 'ignore_errors' => true]]);
        while (true) {
            $body = @file_get_contents($base . '/readyz', false, $ctx);
            $headers = self::lastResponseHeaders($http_response_header ?? null);
            if ($body !== false && $headers !== null) {
                $status = self::parseStatus($headers);
                if ($status === 200) {
                    return;
                }
                $detail = self::notReadyDetail($status, $body);
            } else {
                // Not listening yet (or gone).
                $error = error_get_last();
                $transport = $error['message'] ?? 'no response';
                $detail = null;
            }
            if (microtime(true) >= $deadline) {
                break;
            }
            usleep(150_000);
        }

        if ($detail !== null) {
            throw new OpenrateException("openrate has no rates after {$timeout}s: {$detail}");
        }

        throw new OpenrateException(
            "openrate never answered /readyz within {$timeout}s: {$transport}"
        );
    }

    /**
     * One actionable line out of a /readyz 503: the reason, plus every source
     * that has an error to report. `last_error` is omitempty, so a source that
     * has not been tried yet has no key at all — those are skipped rather than
     * printed as "ecb: ", and if nothing failed the reason stands alone.
     */
    private static function notReadyDetail(int $status, string $body): string
    {
        $decoded = json_decode($body, true);
        if (!\is_array($decoded)) {
            return "HTTP {$status}";
        }

        $reason = (string) ($decoded['reason'] ?? '');
        if ($reason === '') {
            $reason = 'not ready';
        }

        $failed = [];
        foreach ($decoded['sources'] ?? [] as $source) {
            if (\is_array($source) && ($source['last_error'] ?? '') !== '') {
                $failed[] = ($source['name'] ?? '?') . ': ' . $source['last_error'];
            }
        }

        return $failed === [] ? $reason : $reason . ' (' . implode('; ', $failed) . ')';
    }

    /**
     * PHP 8.4 deprecated the magic locally-scoped $http_response_header in
     * favour of http_get_last_response_headers().
     *
     * @param array<int,string>|null $magic
     * @return array<int,string>|null
     */
    private static function lastResponseHeaders(?array $magic): ?array
    {
        if (\function_exists('http_get_last_response_headers')) {
            return \http_get_last_response_headers();
        }

        return $magic;
    }

    /** @param array<int,string> $headers */
    private static function parseStatus(array $headers): int
    {
        foreach ($headers as $h) {
            if (preg_match('#^HTTP/\S+\s+(\d+)#', $h, $m)) {
                return (int) $m[1];
            }
        }

        return 0;
    }
}
