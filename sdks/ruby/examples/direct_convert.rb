#!/usr/bin/env ruby
# frozen_string_literal: true

# DIRECT (in-process) — openrate through the C ABI, no child process, no port,
# and — for the first half of this script — no packets at all.
#
#   ruby sdks/ruby/examples/direct_convert.rb
#
# Environment:
#   OPENRATE_LIBRARY  path to libopenrate-<goos>-<goarch>.dylib/.so
#   OPENRATE_NETWORK  set to 1 to also run the refresher section, which fetches
#                     live rates. Off by default, because an example should not
#                     open a socket you did not ask for.
#
# Before you put this in a Rails app, read sdks/ruby/README.md: Unicorn and
# clustered Puma fork their workers.

$LOAD_PATH.unshift(File.expand_path("../lib", __dir__))
require "openrate/ffi"

# openrate_new builds an ENGINE. It starts no thread, opens no socket, reads no
# environment variable and sends no packet. Ffi.open closes it however the block
# exits — including closing any refresher built over it.
Openrate::Ffi.open(config: { base: "ZAR", quiet: true }) do |engine|
  puts "library : #{engine.library_path}"
  puts "abi     : #{engine.version}"
  puts "handles : #{engine.open_handles} open"

  # 1. A fresh engine holds nothing, and says so rather than guessing.
  begin
    engine.convert("USD", "ZAR", 100)
    puts "empty   : UNEXPECTED — an empty engine should not convert"
  rescue Openrate::Error => e
    puts "empty   : #{e.message}"
  end

  # 2. The zero-network path: install rates you obtained yourself. "load" has no
  #    HTTP counterpart, because the server is read-only.
  loaded = engine.call("load",
                       edges: [
                         { from: "USD", to: "ZAR", rate: 18.42, source: "my-treasury-desk" },
                         { from: "EUR", to: "USD", rate: 1.0864, source: "my-treasury-desk" }
                       ],
                       built_at: Time.now.utc.strftime("%Y-%m-%dT%H:%M:%SZ"))
  puts "loaded  : #{loaded['currencies'].join(', ')}"

  # 3. Convert. Identical to GET /api/v1/convert, rate detail and all.
  r = engine.convert("USD", "ZAR", 100)
  # %.2f, not %s: the result is a JSON float and 100 * 18.42 prints as
  # 1842.0000000000002 raw. openrate returns the number, not a rounding policy.
  puts format("convert : %g %s = %.2f %s (rate %.4f, %d hop)",
              r["amount"], r["from"], r["result"], r["to"], r["rate"]["rate"], r["rate"]["hops"])

  # 4. A pair nobody loaded directly — EUR→ZAR is EUR→USD→ZAR. The graph is the
  #    point of openrate.
  r = engine.convert("EUR", "ZAR", 100)
  puts format("crossed : 100 EUR = %.2f ZAR via %s", r["result"], r["rate"]["path"].join("→"))

  # 5. The all-pairs snapshot and the metadata, same JSON as /api/v1/*.
  rates = engine.call("rates", base: "ZAR")
  puts "rates   : base #{rates['base']}, #{rates['rates'].size} currencies"
  meta = engine.call("meta")
  puts "meta    : default base #{meta['default_base']}, sources #{meta['sources'].size} " \
       "(an engine nobody refreshes has none)"

  # 6. The split is enforced by the ABI, not by documentation: an engine handle
  #    REFUSES a refresher method. This is why "openrate cannot reach the
  #    network unless you built a refresher" is checkable rather than promised.
  begin
    engine.call("refresh", {})
    puts "refuses : UNEXPECTED — an engine should refuse to refresh"
  rescue Openrate::Error => e
    puts "refuses : #{e.message}"
  end

  # 7. Opt in to the network, explicitly, with a second handle.
  if ENV["OPENRATE_NETWORK"] == "1"
    refresher = engine.refresher(config: { sources: "ecb", quiet: true })
    puts "handles : #{engine.open_handles} open (engine + refresher)"
    # Building it still opened nothing. THIS opens sockets:
    status = refresher.call("refresh", timeout_ms: 20_000)
    status["sources"].each do |source|
      # last_error is absent, not nil, when a source succeeded.
      failed = !source["last_error"].to_s.empty?
      detail = failed ? "error: #{source['last_error']}" : "#{source['edges']} edges"
      puts "fetch   : #{source['name']} #{detail}"
    end
    r = engine.convert("EUR", "USD", 1)
    puts format("live    : 1 EUR = %.4f USD (%s)", r["result"], r["rate"]["sources"].join(","))
  else
    puts "network : skipped (set OPENRATE_NETWORK=1 to fetch from ECB)"
  end
end

# A fresh engine only to read the counter: it is a property of the library, not
# of a handle, so this reports what the block above left behind.
Openrate::Ffi.open(config: { quiet: true }) do |probe|
  puts "closed  : ok, #{probe.open_handles - 1} handles left open by the block above"
end
