# SIDECAR — the SDK spawns the `openrate` binary on a loopback port through an
# Erlang Port, waits for /healthz, and reaps it when the GenServer stops. You
# never run a server by hand.
#
#   cd sdks/elixir && mix run examples/sidecar_convert.exs
#
# Environment:
#   OPENRATE_BINARY   path to the openrate binary (default: bundled priv/bin, then PATH)
#   OPENRATE_SOURCES  comma-separated FX sources (default here: ecb)
#
# For Elixir this is not the "fallback" mode — it is the mode. README.md sets
# out why an in-process NIF is the wrong trade for this library.

sources = System.get_env("OPENRATE_SOURCES", "ecb")

{:ok, base} =
  case Openrate.start(sources: sources, ui: false) do
    {:ok, base} ->
      {:ok, base}

    {:error, reason} ->
      IO.puts(:stderr, "could not start the sidecar: #{inspect(reason)}")
      System.halt(1)
  end

IO.puts("sidecar : #{base}")

# try/after, so a failure below still stops the child rather than leaving an
# orphaned server holding a port. Openrate.Sidecar would also reap it at VM
# shutdown; being explicit frees the port the moment we are done.
try do
  # The server answers /healthz before its first fetch completes, so wait for
  # the book rather than racing it.
  deadline = System.monotonic_time(:millisecond) + 60_000

  meta =
    Stream.repeatedly(fn ->
      Process.sleep(250)
      Openrate.meta()
    end)
    |> Stream.take_while(fn _ -> System.monotonic_time(:millisecond) < deadline end)
    |> Enum.find({:error, :timeout}, fn
      {:ok, %{"currencies" => [_ | _]}} -> true
      _ -> false
    end)

  {:ok, meta} = meta

  IO.puts(
    "meta    : base #{meta["default_base"]}, #{length(meta["currencies"])} currencies " <>
      "from #{length(meta["sources"])} source(s)"
  )

  Enum.each(meta["sources"], fn source ->
    # last_error is absent, not nil, when a source succeeded.
    detail =
      case Map.get(source, "last_error") do
        nil -> "#{source["edges"]} edges"
        "" -> "#{source["edges"]} edges"
        err -> "error: #{err}"
      end

    IO.puts("source  : #{String.pad_trailing(source["name"], 10)} #{detail}")
  end)

  # 1. Convert — the same JSON document the C ABI's "convert" method returns.
  {:ok, r} = Openrate.convert("EUR", "USD", 100)
  rate = r["rate"]

  IO.puts(
    "convert : #{r["amount"]} #{r["from"]} = #{Float.round(r["result"] / 1, 4)} #{r["to"]} " <>
      "(rate #{rate["rate"]}, #{rate["hops"]} hop, #{Enum.join(rate["sources"], ",")})"
  )

  # 2. A cross-rate the sources do not publish directly.
  {:ok, r} = Openrate.convert("EUR", "ZAR", 100)

  IO.puts(
    "crossed : 100 EUR = #{Float.round(r["result"] / 1, 2)} ZAR " <>
      "via #{Enum.join(r["rate"]["path"], "→")}"
  )

  # 3. The all-pairs snapshot.
  {:ok, rates} = Openrate.rates("ZAR")
  sample = rates["rates"] |> Map.keys() |> Enum.sort() |> Enum.take(5) |> Enum.join(", ")
  IO.puts("rates   : base #{rates["base"]}, #{map_size(rates["rates"])} currencies (#{sample}, …)")

  # 4. The error path. Over HTTP an error is a status code and a JSON body,
  #    where the C ABI hands back a plain string in *err.
  {:error, {:http, status, message}} = Openrate.convert("XXX", "YYY", 1)
  IO.puts("error   : HTTP #{status} #{message}")

  # 5. Crash isolation — the property a NIF would spend. A process that dies
  #    mid-call takes nothing with it, and the sidecar is untouched.
  {:ok, agent} = Agent.start(fn -> nil end)
  Process.unlink(agent)
  ref = Process.monitor(agent)
  Process.exit(agent, :kill)

  receive do
    {:DOWN, ^ref, :process, _, reason} ->
      IO.puts("isolate : a worker died (#{inspect(reason)}); the VM and the sidecar are fine")
  end

  {:ok, _} = Openrate.meta()
  IO.puts("          openrate still answering after that")
after
  Openrate.stop()
end

IO.puts("stopped : ok")
