#!/usr/bin/env ruby
# frozen_string_literal: true

# The fork probe — evidence, not a claim.
#
# libopenrate puts the Go runtime in your process, and the Go runtime does not
# survive fork() without exec(). Unicorn, clustered Puma, Resque and Spring all
# fork. This makes the collision reproducible on your own machine:
#
#   ruby sdks/ruby/examples/fork_probe.rb before convert   # exited 0
#   ruby sdks/ruby/examples/fork_probe.rb before refresh   # HUNG      (needs network)
#   ruby sdks/ruby/examples/fork_probe.rb after  refresh   # on darwin: CRASHED
#
# openrate's shape makes this sharper than it is for most libraries. Every
# ENGINE method — convert, rates, meta, load — is arithmetic over a snapshot
# already in memory: no sockets, no timers, nothing the Go scheduler has to
# wake. Those survive a fork. Only the REFRESHER touches the network, and that
# is the handle that hangs. "It worked in my forked worker" proves nothing
# unless what you tried was a refresh.
#
# And note the third line. "Load the library after the fork" is the usual
# mitigation, and on macOS it is NOT sufficient for anything doing TLS: the
# child segfaults inside crypto/x509's SecTrustEvaluateWithError, because
# Apple's Security framework is itself fork-unsafe once the parent has touched
# it. Only exec() clears that. Linux, where Go verifies certificates without
# calling into the system, has not been tested here.

$LOAD_PATH.unshift(File.expand_path("../lib", __dir__))
require "openrate/ffi"

unless Process.respond_to?(:fork)
  warn "this platform has no fork(2) — nothing to probe"
  exit 2
end

when_to_load = ARGV[0] || "before"    # before | after   (relative to the fork)
method = ARGV[1] || "convert"         # convert | rates | meta | refresh
timeout = (ARGV[2] || "15").to_f

unless %w[before after].include?(when_to_load)
  warn "usage: fork_probe.rb [before|after] [convert|rates|meta|refresh] [timeout_seconds]"
  exit 2
end

SEED = {
  edges: [{ from: "USD", to: "ZAR", rate: 18.42, source: "seed" }],
  built_at: Time.now.utc.strftime("%Y-%m-%dT%H:%M:%SZ")
}.freeze

def exercise(engine, method)
  if method == "refresh"
    # The only handle that can open a socket, and the only one the fork hazard
    # actually reaches.
    engine.refresher(config: { sources: "ecb", quiet: true })
          .call_raw("refresh", timeout_ms: 10_000)
  else
    request = method == "convert" ? { from: "USD", to: "ZAR", amount: 1 } : {}
    engine.call_raw(method, request)
  end
end

# The Unicorn shape: the library is loaded in the master, and every worker is a
# fork() of it. `preload_app true` does exactly this.
parent =
  if when_to_load == "before"
    e = Openrate::Ffi.new(config: { quiet: true })
    e.call("load", SEED)
    e
  end

pid = fork do
  # ---- child ("worker") ------------------------------------------------------
  begin
    engine = parent
    unless engine
      engine = Openrate::Ffi.new(config: { quiet: true })
      engine.call("load", SEED)
    end

    begin
      bytes = exercise(engine, method).bytesize
      warn "  child: #{method} returned #{bytes} bytes"
    ensure
      # Closing the engine releases the refresher too.
      engine.close
    end
    exit!(0)
  rescue Openrate::Error => e
    warn "  child: ERROR #{e.message}"
    exit!(1)
  end
end

# ---- parent ("master") -------------------------------------------------------
begin
  deadline = Process.clock_gettime(Process::CLOCK_MONOTONIC) + timeout
  verdict = nil
  while Process.clock_gettime(Process::CLOCK_MONOTONIC) < deadline
    reaped, status = Process.waitpid2(pid, Process::WNOHANG)
    if reaped
      verdict =
        if status.exited?
          "exited #{status.exitstatus}"
        else
          "CRASHED, signal #{status.termsig} (Go dumped above)"
        end
      break
    end
    sleep 0.05
  end

  unless verdict
    Process.kill("KILL", pid)
    Process.waitpid(pid)
    verdict = format("HUNG (SIGKILLed after %.0fs)", timeout)
  end

  puts format("load=%-6s method=%-7s -> child %s", when_to_load, method, verdict)
ensure
  parent&.close
end
