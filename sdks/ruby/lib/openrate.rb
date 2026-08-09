# frozen_string_literal: true

# openrate as a managed sidecar: the gem spawns the `openrate` binary on a
# loopback port, waits for it to be READY — /readyz, not /healthz, so the first
# conversion has rates to work with — and terminates it at exit. You never run a
# server by hand.
#
#   require "openrate"
#
#   Openrate.convert("USD", "ZAR", 100)   # => {"from"=>"USD", "result"=>1842.0, ...}
#   Openrate.rates("ZAR")
#   Openrate.meta
#
# For the in-process C-ABI mode — including an engine that provably never opens
# a socket — see `require "openrate/ffi"` and README.md.

require "socket"
require "net/http"
require "uri"
require "json"
require "rbconfig"

module Openrate
  VERSION = "0.1.2"

  class Error < StandardError; end

  @mutex = Mutex.new
  @pid = nil
  @base = nil

  class << self
    # Start the sidecar (idempotent). Returns the base URL (http://host:port).
    #
    # Options mirror the binary's flags: :port, :base, :sources, :refresh, :ui,
    # :ratelimit, :env, :timeout (seconds, default 30).
    #
    # It returns when the child is READY, not merely listening: /readyz is 503
    # until the snapshot has currencies in it, so :timeout has to cover the
    # first fetch. A server that comes up and never gets a rate raises with the
    # reason /readyz gave — "ecb: connection refused" — instead of a bare
    # deadline.
    def start(port: nil, base: nil, sources: nil, refresh: nil, ui: false, ratelimit: 0,
              env: nil, timeout: 30.0)
      @mutex.synchronize do
        return @base if running?

        port ||= free_port
        addr = "127.0.0.1:#{port}"
        # OPENRATE_RATELIMIT: the binary defaults to 120 API requests/minute
        # per IP, which is anti-scraping for a PUBLIC deployment. This child
        # listens on loopback and serves exactly one client — us — so there is
        # no stranger here to throttle, while a legitimate batch of conversions
        # would sail past 120/min and take a 429 from our own sidecar. Default
        # it off; pass ratelimit: 120 to put it back. (Readiness polling is not
        # the reason: /readyz is outside /api/ and the limiter never sees it.)
        child_env = {
          "OPENRATE_ADDR" => addr,
          "OPENRATE_UI" => ui ? "true" : "false",
          "OPENRATE_RATELIMIT" => ratelimit.to_s
        }
        child_env["OPENRATE_BASE"] = base if base
        child_env["OPENRATE_SOURCES"] = sources if sources
        child_env["OPENRATE_REFRESH"] = refresh if refresh
        child_env.merge!(env) if env

        @pid = Process.spawn(child_env, binary_path, in: :in, out: :out, err: :err)
        @base = "http://#{addr}"
        begin
          poll_ready(@base, timeout)
        rescue StandardError
          stop_locked
          raise
        end
        at_exit { stop }
        @base
      end
    end

    # The running base URL, starting the sidecar if needed.
    def base_url
      running? ? @base : start
    end

    # The JSON API base (…/api/v1).
    def api_base_url
      "#{base_url}/api/v1"
    end

    # GET /api/v1/convert
    def convert(from, to, amount = 1)
      get("/convert", from: from, to: to, amount: amount)
    end

    # GET /api/v1/rates
    def rates(base = nil)
      base ? get("/rates", base: base) : get("/rates")
    end

    # GET /api/v1/meta
    def meta
      get("/meta")
    end

    # GET /healthz — liveness. True the moment the process is listening, which
    # is BEFORE it has any rates. Do not convert on the strength of it.
    def healthy?
      probe("/healthz")
    end

    # GET /readyz — readiness. True once a conversion would actually succeed.
    #
    # A managed sidecar is already ready by the time `start` returns; this is
    # for the other case, a server you were merely pointed at, which can be up
    # and still empty.
    def ready?
      probe("/readyz")
    end

    # Block until /readyz says ready, or raise with the reason it gave. Same
    # poll `start` uses, not a second implementation of readiness.
    def wait_ready(timeout: 30.0)
      poll_ready(base_url, timeout)
    end

    # One GET against the JSON API. `path` is relative to /api/v1.
    def get(path, **params)
      uri = URI("#{api_base_url}#{path}")
      uri.query = URI.encode_www_form(params) unless params.empty?

      res = Net::HTTP.start(uri.host, uri.port, open_timeout: 5, read_timeout: 30) do |http|
        http.get(uri.request_uri)
      end

      body = res.body.to_s
      parsed = begin
        JSON.parse(body)
      rescue JSON::ParserError
        nil
      end

      unless res.code == "200"
        detail = parsed.is_a?(Hash) ? parsed["error"] : body[0, 200]
        raise Error, "GET #{path}: HTTP #{res.code}: #{detail}"
      end
      raise Error, "GET #{path}: response was not a JSON object" unless parsed.is_a?(Hash)

      parsed
    end

    # Stop the sidecar if running. Idempotent.
    def stop
      @mutex.synchronize { stop_locked }
    end

    private

    # One probe request against a non-/api/ path. Neither /healthz nor /readyz
    # is rate-limited, so this costs the caller nothing.
    def probe(path)
      uri = URI("#{base_url}#{path}")
      res = Net::HTTP.start(uri.host, uri.port, open_timeout: 1, read_timeout: 2) do |http|
        http.get(uri.request_uri)
      end
      res.code.to_i == 200
    rescue StandardError
      false
    end

    def running?
      !@pid.nil? && process_alive?(@pid)
    end

    def process_alive?(pid)
      Process.kill(0, pid)
      true
    rescue Errno::ESRCH, Errno::EPERM
      false
    end

    def stop_locked
      if @pid && process_alive?(@pid)
        begin
          Process.kill("TERM", @pid)
          deadline = monotonic + 5.0
          Process.wait(@pid, Process::WNOHANG)
          while process_alive?(@pid) && monotonic < deadline
            sleep 0.05
            Process.wait(@pid, Process::WNOHANG)
          end
          Process.kill("KILL", @pid) if process_alive?(@pid)
        rescue Errno::ESRCH, Errno::ECHILD
          # already gone
        end
      end
      @pid = nil
      @base = nil
    end

    def binary_path
      env = ENV["OPENRATE_BINARY"]
      return env if env && !env.empty?

      name = windows? ? "openrate.exe" : "openrate"
      bundled = File.join(__dir__, "..", "bin", name)
      return File.expand_path(bundled) if File.exist?(bundled)

      path = which("openrate")
      return path if path

      raise Error, "openrate binary not found. Set OPENRATE_BINARY, install a " \
        "platform gem, or build it: `go build -o sdks/ruby/bin/openrate ./cmd/openrate`"
    end

    def which(cmd)
      exts = windows? ? ENV.fetch("PATHEXT", "").split(";") : [""]
      ENV.fetch("PATH", "").split(File::PATH_SEPARATOR).each do |dir|
        exts.each do |ext|
          candidate = File.join(dir, "#{cmd}#{ext}")
          return candidate if File.executable?(candidate) && !File.directory?(candidate)
        end
      end
      nil
    end

    def windows?
      RbConfig::CONFIG["host_os"] =~ /mswin|mingw|cygwin/
    end

    def free_port
      server = TCPServer.new("127.0.0.1", 0)
      port = server.addr[1]
      server.close
      port
    end

    # Poll GET /readyz until the server can actually answer a conversion.
    #
    # Not /healthz. /healthz answers the instant the listener binds, before any
    # source has been fetched, so a caller that waits on it converts against an
    # empty book and gets "unknown or unreachable currency pair" for every pair
    # — a false green wearing a bad-currency-code costume.
    #
    # 150 ms fixed, and no backoff: /readyz sits outside /api/, so the per-IP
    # limiter never sees it and there is no budget to spend by polling.
    #
    # On timeout the caller gets the cause, not a deadline: whatever the last
    # 503 body said, or the transport error if the server never answered.
    def poll_ready(base, timeout)
      deadline = monotonic + timeout
      uri = URI("#{base}/readyz")
      detail = nil     # from the last 503 body
      transport = nil
      loop do
        begin
          res = Net::HTTP.start(uri.host, uri.port, open_timeout: 1, read_timeout: 2) do |http|
            http.get(uri.request_uri)
          end
          return if res.code.to_i == 200

          transport = nil
          detail = not_ready_detail(res)
        rescue StandardError => e
          # Not listening yet (or gone). Keep the text: it is the difference
          # between "the child died" and "the child is slow".
          transport = e
          detail = nil
        end
        break if monotonic >= deadline

        sleep 0.15
      end

      raise Error, "openrate has no rates after #{timeout}s: #{detail}" if detail

      raise Error, "openrate never answered /readyz within #{timeout}s: #{transport}"
    end

    # One actionable line out of a /readyz 503: the reason, plus every source
    # that has an error to report. `last_error` is omitempty, so a source that
    # has not been tried yet has no key at all — those are skipped rather than
    # printed as "ecb: ", and if nothing failed the reason stands alone.
    def not_ready_detail(res)
      body = begin
        JSON.parse(res.body.to_s)
      rescue JSON::ParserError, TypeError
        nil
      end
      return "HTTP #{res.code}" unless body.is_a?(Hash)

      reason = body["reason"].to_s.empty? ? "not ready" : body["reason"].to_s
      failed = Array(body["sources"]).filter_map do |source|
        next unless source.is_a?(Hash) && !source["last_error"].to_s.empty?

        "#{source['name'] || '?'}: #{source['last_error']}"
      end
      failed.empty? ? reason : "#{reason} (#{failed.join('; ')})"
    end

    def monotonic
      Process.clock_gettime(Process::CLOCK_MONOTONIC)
    end
  end
end
