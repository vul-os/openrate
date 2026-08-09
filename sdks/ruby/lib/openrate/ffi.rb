# frozen_string_literal: true

# openrate in-process, through the C ABI (`libopenrate`), using `fiddle` from
# the standard library. No child process, no port, and no gem dependency.
#
#   require "openrate/ffi"
#
#   Openrate::Ffi.open do |engine|                 # an ENGINE. It computes.
#     engine.call("load", edges: [
#       { from: "USD", to: "ZAR", rate: 18.42, source: "my-treasury-desk" }
#     ])
#     puts engine.convert("USD", "ZAR", 100)["result"]   # => 1842.0
#   end
#
# The engine/refresher split is enforced at the ABI, not by convention:
# openrate_new builds an engine that reads no environment variable and sends no
# packet, and an engine handle REFUSES "refresh". Fetching needs a separate
# openrate_refresher_new with its own handle — see Ffi#refresher.
#
# READ sdks/ruby/README.md BEFORE YOU USE THIS. The Go runtime ends up inside
# your Ruby process and does not survive fork(), which collides with Unicorn,
# clustered Puma, Resque and Spring.
#
# Why `fiddle` and not the `ffi` gem: fiddle ships with Ruby, so the direct mode
# adds no dependency to a gem whose whole selling point is that it is thin.
# openrate's ABI has no callback at all — there is no openrate_stream — so the
# only thing needed is dlopen and a calling convention, which fiddle has.

require "fiddle"
require "json"
require "rbconfig"

module Openrate
  class Error < StandardError; end unless defined?(Error)

  # The C ABI of libopenrate (ffi/include/openrate.h) bound with fiddle.
  class Ffi
    PTR = Fiddle::TYPE_VOIDP
    U64 = Fiddle::TYPE_LONG_LONG   # fiddle has no unsigned 64-bit constant;
    VOID = Fiddle::TYPE_VOID       # handles are small counters, so this is exact.

    # Methods an ENGINE understands. A refresher method here is a clean error.
    ENGINE_METHODS = %w[convert rates meta load].freeze

    # Methods a REFRESHER understands. "refresh" and "start" open sockets.
    REFRESHER_METHODS = %w[status refresh start stop ready].freeze

    # Open an engine, yield it, and close it however the block exits. Closing an
    # engine also releases every refresher built over it, so one block is enough
    # for both.
    def self.open(config: nil, library: nil)
      engine = new(config: config, library: library)
      begin
        yield engine
      ensure
        engine.close
      end
    end

    # Build an ENGINE. config: {base: "ZAR", quiet: true}, a JSON String, or nil.
    def initialize(config: nil, library: nil)
      @library_path = library || self.class.resolve_library
      begin
        @dl = Fiddle.dlopen(@library_path)
      rescue Fiddle::DLError => e
        raise Error, "could not load #{@library_path}: #{e.message}"
      end

      bind!
      @refresher = false
      @mutex = Mutex.new

      slot = err_slot
      handle = @fn_new.call(self.class.json(config), slot)
      raise Error, "openrate_new: #{take_error(slot)}" if handle.zero?

      @handle = handle
    end

    attr_reader :library_path

    # Build a REFRESHER over this engine. It gets its own handle and its own
    # lifetime. Constructing it still opens no socket — that starts at
    # #call("refresh") or #call("start").
    def refresher(config: nil)
      assert_open!
      raise Error, "a refresher cannot own another refresher" if @refresher

      slot = err_slot
      handle = @fn_refresher_new.call(@handle, self.class.json(config), slot)
      raise Error, "openrate_refresher_new: #{take_error(slot)}" if handle.zero?

      child = clone            # shares the loaded library; only the handle differs
      child.send(:adopt_refresher_handle, handle)
      child
    end

    def refresher?
      @refresher
    end

    # The openrate version the LOADED library was built from. Static inside the
    # library — do NOT free.
    def version
      @fn_abi_version.call.to_s
    end

    # How many handles the library currently has open. Diagnostic — useful in a
    # test that wants to assert it closed what it opened.
    def open_handles
      @fn_open_handles.call
    end

    # One call against this handle. Returns the parsed JSON response.
    #
    #   engine.call("convert", from: "USD", to: "ZAR", amount: 100)
    #   engine.call("meta")
    def call(method, request = nil, **kwargs)
      request = kwargs unless kwargs.empty?
      JSON.parse(call_raw(method, request))
    end

    # The same call, returning the response JSON verbatim — the same document
    # the HTTP API publishes.
    def call_raw(method, request = nil)
      assert_open!

      # A FRESH, zeroed slot per call: openrate sets *err on failure only.
      slot = err_slot
      res = @fn_call.call(@handle, method.to_s, self.class.json(request), slot)
      raise Error, "openrate_call(#{method}): #{take_error(slot)}" if res.null?

      begin
        res.to_s   # Fiddle::Pointer#to_s copies up to the NUL into a Ruby String
      ensure
        # Everything the library returns — results AND error messages — is
        # released with openrate_free and nothing else.
        @fn_free.call(res)
      end
    end

    # Convenience for the commonest call.
    def convert(from, to, amount = 1)
      call("convert", from: from, to: to, amount: amount)
    end

    # Release this handle. Closing an ENGINE also stops and releases every
    # refresher built over it, so closing in the "wrong" order cannot leak a
    # running loop. Idempotent.
    def close
      @mutex&.synchronize do
        return if @handle.nil? || @handle.zero?

        @fn_close.call(@handle)
        @handle = 0
      end
      nil
    end

    def closed?
      @handle.nil? || @handle.zero?
    end

    # ---------------------------------------------------------------- internals

    private

    def adopt_refresher_handle(handle)
      @handle = handle
      @refresher = true
      @mutex = Mutex.new
      self
    end

    def bind!
      # need_gvl: is left at its default (false), so fiddle routes the call
      # through rb_thread_call_without_gvl and other Ruby threads keep running
      # during a refresh that is waiting on a central bank. Nothing in
      # openrate's ABI calls back into Ruby — there is no openrate_stream — so
      # there is no closure to worry about here at all.
      @fn_abi_version = Fiddle::Function.new(@dl["openrate_abi_version"], [], PTR,
                                             name: "openrate_abi_version")
      @fn_new = Fiddle::Function.new(@dl["openrate_new"], [PTR, PTR], U64, name: "openrate_new")
      @fn_refresher_new = Fiddle::Function.new(@dl["openrate_refresher_new"], [U64, PTR, PTR], U64,
                                               name: "openrate_refresher_new")
      @fn_call = Fiddle::Function.new(@dl["openrate_call"], [U64, PTR, PTR, PTR], PTR,
                                      name: "openrate_call")
      @fn_close = Fiddle::Function.new(@dl["openrate_close"], [U64], VOID, name: "openrate_close")
      @fn_free = Fiddle::Function.new(@dl["openrate_free"], [PTR], VOID, name: "openrate_free")
      @fn_open_handles = Fiddle::Function.new(@dl["openrate_open_handles"], [], U64,
                                              name: "openrate_open_handles")
    rescue Fiddle::DLError => e
      raise Error, "#{@library_path} is missing a symbol the ABI promises: #{e.message}"
    end

    def assert_open!
      raise Error, "this Openrate::Ffi handle is closed" if closed?
    end

    # A zeroed `char*` slot for the trailing `char** err` argument.
    def err_slot
      slot = Fiddle::Pointer.malloc(Fiddle::SIZEOF_VOIDP, Fiddle::RUBY_FREE)
      slot[0, Fiddle::SIZEOF_VOIDP] = "\x00".b * Fiddle::SIZEOF_VOIDP
      slot
    end

    # Read the message out of an err slot and free it with openrate_free — an
    # error must not be a leak.
    def take_error(slot)
      ptr = slot.ptr
      return "(no message)" if ptr.null?

      message = ptr.to_s
      @fn_free.call(ptr)
      message
    end

    class << self
      def json(value)
        case value
        when nil, String then value
        else JSON.generate(value)
        end
      end

      # 1. OPENRATE_LIBRARY
      # 2. lib/ inside this gem (where a platform gem would put it)
      # 3. dist/ffi/ in a checkout — note the naming is
      #    libopenrate-<goos>-<goarch>.<ext>, not a per-target directory
      # 4. the bare soname, letting the dynamic loader search its own paths
      def resolve_library
        env = ENV["OPENRATE_LIBRARY"]
        return env if env && !env.empty?

        ext = library_extension
        gem_root = File.expand_path("../..", __dir__) # …/sdks/ruby
        repo = File.expand_path("../..", gem_root)    # …/repo root

        candidates = [
          File.join(gem_root, "lib", "libopenrate.#{ext}"),
          File.join(repo, "dist", "ffi", "libopenrate-#{goos}-#{goarch}.#{ext}")
        ]
        candidates.find { |path| File.file?(path) } ||
          (goos == "windows" ? "openrate.dll" : "libopenrate.#{ext}")
      end

      def library_extension
        case RbConfig::CONFIG["host_os"]
        when /mswin|mingw|cygwin/ then "dll"
        when /darwin/ then "dylib"
        else "so"
        end
      end

      def goos
        case RbConfig::CONFIG["host_os"]
        when /mswin|mingw|cygwin/ then "windows"
        when /darwin/ then "darwin"
        when /freebsd/ then "freebsd"
        else "linux"
        end
      end

      def goarch
        case RbConfig::CONFIG["host_cpu"]
        when /arm64|aarch64/ then "arm64"
        when /x86_64|amd64/ then "amd64"
        else RbConfig::CONFIG["host_cpu"]
        end
      end
    end
  end
end
