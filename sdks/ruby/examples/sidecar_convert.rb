#!/usr/bin/env ruby
# frozen_string_literal: true

# SIDECAR (out-of-process) — the gem spawns the `openrate` binary on a loopback
# port, waits for /readyz, and shuts it down at exit. You never run a server by
# hand.
#
#   ruby sdks/ruby/examples/sidecar_convert.rb
#
# Environment:
#   OPENRATE_BINARY        path to the openrate binary (default: bundled bin/, then PATH)
#   OPENRATE_SOURCES       comma-separated FX sources (default here: ecb)
#   OPENRATE_READY_TIMEOUT seconds to wait for the first rate (default 30)
#
# This is the shape to use inside Unicorn, clustered Puma, Resque, or anything
# else that forks. sdks/ruby/README.md has the measurements behind that.
#
# Note what is different from direct mode: the sidecar HAS a refresher, by
# construction — `openrate serve` is the program whose job is to fetch. If you
# want a process that provably never reaches the network, that is the engine in
# direct mode, not this.

$LOAD_PATH.unshift(File.expand_path("../lib", __dir__))
require "openrate"

sources = ENV.fetch("OPENRATE_SOURCES", "ecb")
ready_timeout = Float(ENV.fetch("OPENRATE_READY_TIMEOUT", "30"))

# `start` returns only once /readyz says a conversion will succeed, so there is
# no "wait for the book" step here and no window in which this example could
# convert against an empty one. If no rate ever arrives it raises, naming the
# source that failed rather than timing out silently.
begin
  base = Openrate.start(sources: sources, ui: false, timeout: ready_timeout)
rescue Openrate::Error => e
  warn "could not start the sidecar: #{e.message}"
  exit 1
end
puts "sidecar : #{base}"
puts "probes  : healthy? #{Openrate.healthy?} (up), ready? #{Openrate.ready?} (has rates)"

# begin/ensure, so a failure below still stops the child rather than leaving an
# orphaned server holding a port.
begin
  meta = Openrate.meta

  puts "meta    : base #{meta['default_base']}, #{meta['currencies'].size} currencies " \
       "from #{meta['sources'].size} source(s)"
  meta["sources"].each do |source|
    failed = !source["last_error"].to_s.empty?
    detail = failed ? "error: #{source['last_error']}" : "#{source['edges']} edges"
    puts format("source  : %-10s %s", source["name"], detail)
  end

  # 1. Convert — the same JSON document the C ABI's "convert" method returns.
  r = Openrate.convert("EUR", "USD", 100)
  puts format("convert : %g %s = %.4f %s (rate %.6f, %d hop, %s)",
              r["amount"], r["from"], r["result"], r["to"],
              r["rate"]["rate"], r["rate"]["hops"], r["rate"]["sources"].join(","))

  # 2. A cross-rate the sources do not publish directly.
  r = Openrate.convert("EUR", "ZAR", 100)
  puts format("crossed : 100 EUR = %.2f ZAR via %s", r["result"], r["rate"]["path"].join("→"))

  # 3. The all-pairs snapshot.
  rates = Openrate.rates("ZAR")
  puts "rates   : base #{rates['base']}, #{rates['rates'].size} currencies " \
       "(#{rates['rates'].keys.take(5).join(', ')}, …)"

  # 4. The error path. Over HTTP an error is a status code and a JSON body,
  #    where the C ABI hands back a plain string in *err.
  begin
    Openrate.convert("XXX", "YYY", 1)
    puts "error   : UNEXPECTED — an unknown pair should fail"
  rescue Openrate::Error => e
    puts "error   : #{e.message}"
  end
ensure
  # The at_exit hook would do this too; doing it here frees the port the moment
  # we are done, on the raise path as well.
  Openrate.stop
end

puts "stopped : ok"
