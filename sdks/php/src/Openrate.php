<?php

declare(strict_types=1);

namespace Openrate;

/**
 * openrate as a managed sidecar: the SDK spawns the `openrate` binary on a
 * loopback port, waits for it to answer /healthz, and terminates it at shutdown.
 * You never run a server by hand.
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
        // anti-scraping for a PUBLIC deployment. This sidecar is on loopback and
        // serves exactly one client — us — and that budget is small enough that
        // the SDK's own startup health polling can exhaust it and hand the first
        // real call an HTTP 429. Default it off here and let a caller who wants
        // it back pass ['ratelimit' => 120].
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
            self::waitHealthy(self::$base, (float) ($opts['timeout'] ?? 15.0));
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

    private static function waitHealthy(string $base, float $timeout): void
    {
        $deadline = microtime(true) + $timeout;
        $last = 'connection refused';
        $ctx = stream_context_create(['http' => ['timeout' => 1, 'ignore_errors' => true]]);
        while (microtime(true) < $deadline) {
            $body = @file_get_contents($base . '/healthz', false, $ctx);
            $headers = self::lastResponseHeaders($http_response_header ?? null);
            if ($body !== false && $headers !== null) {
                $status = self::parseStatus($headers);
                if ($status === 200) {
                    return;
                }
                $last = "status {$status}";
            }
            // 200 ms, not 50 ms: /healthz goes through the same per-IP limiter
            // as the API, so a tight poll spends a budget the caller wanted.
            usleep(200_000);
        }
        throw new OpenrateException("openrate did not become healthy within {$timeout}s: {$last}");
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
