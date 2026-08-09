<?php

declare(strict_types=1);

namespace Openrate;

/**
 * openrate in-process, through the C ABI (`libopenrate`), using PHP's bundled
 * FFI extension. No child process, no port.
 *
 *   $engine = new \Openrate\Ffi();               // an ENGINE. It computes.
 *   try {
 *       $engine->call('load', ['edges' => [...]]);          // rates you obtained
 *       $r = $engine->call('convert', ['from' => 'USD', 'to' => 'ZAR', 'amount' => 100]);
 *   } finally {
 *       $engine->close();
 *   }
 *
 * The engine/refresher split is enforced at the ABI, not by convention:
 * openrate_new() builds an engine that reads no environment variable and sends
 * no packet, and an engine handle REFUSES "refresh". Fetching needs a separate
 * openrate_refresher_new() with its own handle — see Ffi::refresher().
 *
 * READ sdks/php/README.md BEFORE YOU USE THIS. The Go runtime ends up inside
 * your PHP process and it is not fork-safe, which collides head-on with
 * php-fpm. For most PHP deployments \Openrate\Openrate — the managed sidecar —
 * is the right answer.
 */
final class Ffi
{
    /**
     * The C ABI, transcribed from ffi/include/openrate.h. PHP's FFI parser does
     * not run the C preprocessor, so the header itself cannot be handed to
     * FFI::cdef. Note what is NOT here: there is no openrate_stream, because
     * openrate answers from a snapshot and has nothing to stream.
     */
    private const CDEF = <<<'C'
        const char* openrate_abi_version(void);
        uint64_t openrate_new(const char* config_json, char** err);
        uint64_t openrate_refresher_new(uint64_t engine, const char* config_json, char** err);
        char* openrate_call(uint64_t h, const char* method, const char* request_json, char** err);
        void openrate_close(uint64_t h);
        void openrate_free(char* p);
        uint64_t openrate_open_handles(void);
        C;

    /** Methods an ENGINE understands. A refresher method here is a clean error. */
    public const ENGINE_METHODS = ['convert', 'rates', 'meta', 'load'];

    /** Methods a REFRESHER understands. "refresh" and "start" open sockets. */
    public const REFRESHER_METHODS = ['status', 'refresh', 'start', 'stop', 'ready'];

    /** @var \FFI */
    private $ffi;

    /** @var int */
    private $handle = 0;

    /** @var string */
    private $libraryPath;

    /** @var bool true for a refresher handle, false for an engine */
    private $isRefresher;

    /** @var self|null the engine a refresher was built over, for close ordering */
    private $engine;

    /**
     * Build an ENGINE.
     *
     * @param array{base?:string,quiet?:bool}|string|null $config
     */
    public function __construct($config = null, ?string $libraryPath = null)
    {
        if (!\extension_loaded('FFI')) {
            throw new OpenrateException(
                'the FFI extension is not loaded. It ships with PHP >= 7.4 but is ' .
                'often disabled: enable extension=ffi in php.ini. See sdks/php/README.md.'
            );
        }

        $this->libraryPath = $libraryPath ?? self::resolveLibrary();
        $this->isRefresher = false;
        $this->engine = null;

        try {
            $this->ffi = \FFI::cdef(self::CDEF, $this->libraryPath);
        } catch (\FFI\Exception $e) {
            throw new OpenrateException(
                'FFI::cdef failed for ' . $this->libraryPath . ': ' . $e->getMessage() .
                (\strpos($e->getMessage(), 'ffi.enable') !== false
                    ? "\nffi.enable is 'preload' by default, which forbids FFI::cdef in " .
                      'any SAPI except CLI. See sdks/php/README.md — the sidecar ' .
                      '(\\Openrate\\Openrate) needs no php.ini change at all.'
                    : ''),
                0,
                $e
            );
        }

        $err = $this->ffi->new('char*');
        $handle = $this->ffi->openrate_new(self::json($config), \FFI::addr($err));
        if ($handle === 0) {
            throw new OpenrateException('openrate_new: ' . $this->takeError($err));
        }
        $this->handle = (int) $handle;
    }

    /**
     * Run $fn with an open engine and close it afterwards, whatever happens.
     * Closing an engine also closes every refresher built over it, so this one
     * `finally` is enough for both.
     *
     * @param array{base?:string,quiet?:bool}|string|null $config
     * @return mixed
     */
    public static function with($config, callable $fn, ?string $libraryPath = null)
    {
        $engine = new self($config, $libraryPath);
        try {
            return $fn($engine);
        } finally {
            $engine->close();
        }
    }

    /**
     * Build a REFRESHER over this engine. It gets its own handle and its own
     * lifetime. Constructing it still opens no socket — that starts at
     * `$refresher->call('refresh')` or `->call('start')`.
     *
     * @param array{sources?:string,interval_ms?:int,fetch_timeout_ms?:int,quiet?:bool}|string|null $config
     */
    public function refresher($config = null): self
    {
        $this->assertOpen();
        if ($this->isRefresher) {
            throw new OpenrateException('a refresher cannot own another refresher');
        }

        $err = $this->ffi->new('char*');
        $handle = $this->ffi->openrate_refresher_new($this->handle, self::json($config), \FFI::addr($err));
        if ($handle === 0) {
            throw new OpenrateException('openrate_refresher_new: ' . $this->takeError($err));
        }

        // A clone shares the loaded FFI object; only the handle differs.
        $refresher = clone $this;
        $refresher->handle = (int) $handle;
        $refresher->isRefresher = true;
        $refresher->engine = $this;

        return $refresher;
    }

    /** The openrate version the LOADED library was built from. Never free it. */
    public function version(): string
    {
        return (string) $this->ffi->openrate_abi_version();
    }

    /** How many handles the library currently has open. Diagnostic. */
    public function openHandles(): int
    {
        return (int) $this->ffi->openrate_open_handles();
    }

    /** The path this instance actually loaded. */
    public function libraryPath(): string
    {
        return $this->libraryPath;
    }

    public function isRefresher(): bool
    {
        return $this->isRefresher;
    }

    /**
     * One call against this handle.
     *
     * @param array<string,mixed>|string|null $request
     * @return array<string,mixed> the decoded JSON response
     */
    public function call(string $method, $request = null): array
    {
        $decoded = json_decode($this->callRaw($method, $request), true);
        if (!\is_array($decoded)) {
            throw new OpenrateException("openrate returned a non-object response to '{$method}'");
        }

        return $decoded;
    }

    /**
     * The same call, returning the response JSON verbatim — the same document
     * the HTTP API publishes.
     *
     * @param array<string,mixed>|string|null $request
     */
    public function callRaw(string $method, $request = null): string
    {
        $this->assertOpen();

        // A FRESH, zeroed slot per call: openrate sets *err on failure only.
        $err = $this->ffi->new('char*');
        $res = $this->ffi->openrate_call($this->handle, $method, self::json($request), \FFI::addr($err));

        // A NULL char* return arrives in PHP as null, not as a null CData.
        if ($res === null) {
            throw new OpenrateException("openrate_call({$method}): " . $this->takeError($err));
        }

        try {
            return \FFI::string($res);
        } finally {
            $this->ffi->openrate_free($res);
        }
    }

    /** Convenience for the commonest call. */
    public function convert(string $from, string $to, float $amount = 1.0): array
    {
        return $this->call('convert', ['from' => $from, 'to' => $to, 'amount' => $amount]);
    }

    /**
     * Release this handle. Closing an ENGINE also stops and releases every
     * refresher built over it, so closing in the "wrong" order cannot leak a
     * running loop. Idempotent.
     */
    public function close(): void
    {
        if ($this->handle !== 0) {
            $this->ffi->openrate_close($this->handle);
            $this->handle = 0;
        }
    }

    /** A safety net. close() in a finally block is the mechanism. */
    public function __destruct()
    {
        $this->close();
    }

    // ---------------------------------------------------------------- internals

    private function assertOpen(): void
    {
        if ($this->handle === 0) {
            throw new OpenrateException('this Openrate\Ffi handle is closed');
        }
    }

    /**
     * Read the message out of an err slot and free it with openrate_free — an
     * error must not be a leak.
     *
     * @param \FFI\CData $err
     */
    private function takeError($err): string
    {
        if (\FFI::isNull($err)) {
            return '(no message)';
        }
        $message = \FFI::string($err);
        $this->ffi->openrate_free($err);

        return $message;
    }

    /** @param array<string,mixed>|string|null $value */
    private static function json($value): ?string
    {
        if ($value === null || \is_string($value)) {
            return $value;
        }
        $json = json_encode($value);
        if ($json === false) {
            throw new OpenrateException('could not encode the request as JSON: ' . json_last_error_msg());
        }

        return $json;
    }

    /**
     * Find libopenrate:
     *   1. OPENRATE_LIBRARY
     *   2. lib/ inside this package
     *   3. dist/ffi/ in a checkout of the openrate repo — note the naming is
     *      libopenrate-<goos>-<goarch>.<ext>, not a per-target directory
     *   4. the bare soname, letting the dynamic loader search its own paths
     */
    private static function resolveLibrary(): string
    {
        $env = getenv('OPENRATE_LIBRARY');
        if ($env !== false && $env !== '') {
            return $env;
        }

        $ext = self::libraryExtension();
        $pkg = \dirname(__DIR__);   // .../sdks/php
        $repo = \dirname($pkg, 2);  // repo root

        $candidates = [
            $pkg . '/lib/libopenrate.' . $ext,
            $repo . '/dist/ffi/libopenrate-' . self::goos() . '-' . self::goarch() . '.' . $ext,
        ];
        foreach ($candidates as $candidate) {
            if (is_file($candidate)) {
                return $candidate;
            }
        }

        return \PHP_OS_FAMILY === 'Windows' ? 'openrate.dll' : 'libopenrate.' . $ext;
    }

    private static function libraryExtension(): string
    {
        switch (\PHP_OS_FAMILY) {
            case 'Windows':
                return 'dll';
            case 'Darwin':
                return 'dylib';
            default:
                return 'so';
        }
    }

    private static function goos(): string
    {
        switch (\PHP_OS_FAMILY) {
            case 'Windows':
                return 'windows';
            case 'Darwin':
                return 'darwin';
            case 'BSD':
                return 'freebsd';
            default:
                return 'linux';
        }
    }

    private static function goarch(): string
    {
        $machine = strtolower(php_uname('m'));
        if ($machine === 'arm64' || $machine === 'aarch64') {
            return 'arm64';
        }
        if ($machine === 'x86_64' || $machine === 'amd64') {
            return 'amd64';
        }

        return $machine;
    }
}
