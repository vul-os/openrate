# frozen_string_literal: true

# openrate as a managed sidecar: the gem spawns the `openrate` binary on a
# loopback port, waits for it to answer /healthz, and terminates it at exit.
# You never run a server by hand.
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
    # :env, :timeout (seconds, default 15).
    def start(port: nil, base: nil, sources: nil, refresh: nil, ui: false, env: nil, timeout: 15.0)
      @mutex.synchronize do
        return @base if running?

        port ||= free_port
        addr = "127.0.0.1:#{port}"
        child_env = { "OPENRATE_ADDR" => addr, "OPENRATE_UI" => ui ? "true" : "false" }
        child_env["OPENRATE_BASE"] = base if base
        child_env["OPENRATE_SOURCES"] = sources if sources
        child_env["OPENRATE_REFRESH"] = refresh if refresh
        child_env.merge!(env) if env

        @pid = Process.spawn(child_env, binary_path, in: :in, out: :out, err: :err)
        @base = "http://#{addr}"
        begin
          wait_healthy(@base, timeout)
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

    def wait_healthy(base, timeout)
      deadline = monotonic + timeout
      last = nil
      uri = URI("#{base}/healthz")
      while monotonic < deadline
        begin
          res = Net::HTTP.start(uri.host, uri.port, open_timeout: 1, read_timeout: 1) do |http|
            http.get(uri.request_uri)
          end
          return if res.code.to_i == 200
        rescue StandardError => e
          last = e
        end
        sleep 0.05
      end
      raise Error, "openrate did not become healthy within #{timeout}s: #{last}"
    end

    def monotonic
      Process.clock_gettime(Process::CLOCK_MONOTONIC)
    end
  end
end
