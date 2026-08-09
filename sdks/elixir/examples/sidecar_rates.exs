# SIDECAR — the snapshot, concurrency, and what process isolation buys you.
#
#   cd sdks/elixir && mix run examples/sidecar_rates.exs
#
# Environment: OPENRATE_BINARY, OPENRATE_SOURCES (default here: ecb)
#
# The first example (sidecar_convert.exs) is the tour. This one is the argument:
# it fans a hundred conversions across a hundred BEAM processes, cancels one
# mid-flight, and times out another — three things a NIF holding a scheduler
# thread could not offer. README.md sets out why that decides the mode for
# Elixir.

sources = System.get_env("OPENRATE_SOURCES", "ecb")

# OPENRATE_RATELIMIT=0 because the sidecar rate-limits per IP (120 req/min by
# default, an anti-scraping measure for a public deployment) and step 2 below
# sends a hundred requests at once. Without this the fan-out reports about 58/100
# and the rest are HTTP 429 — which is worth knowing: the HTTP shell enforces
# limits the in-process library has no equivalent for.
{:ok, base} =
  Openrate.start(sources: sources, ui: false, env: [{"OPENRATE_RATELIMIT", "0"}])

IO.puts("sidecar : #{base}")

try do
  # Wait for the first fetch to populate the book.
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
  currencies = meta["currencies"] |> Enum.reject(&(&1 == "ZAR")) |> Enum.take(20)
  IO.puts("meta    : #{length(meta["currencies"])} currencies, built #{meta["built_at"]}")

  # 1. The snapshot. rates[X].rate reads as "1 base = rate units of X".
  {:ok, snapshot} = Openrate.rates("ZAR")

  snapshot["rates"]
  |> Enum.sort_by(fn {code, _} -> code end)
  |> Enum.take(4)
  |> Enum.each(fn {code, view} ->
    IO.puts("rate    : 1 ZAR = #{view["rate"]} #{code} (#{view["hops"]} hop)")
  end)

  # 2. A hundred conversions across a hundred processes. Each is an ordinary
  #    BEAM process doing socket I/O — there is no pool to exhaust, and a slow
  #    one blocks nothing but itself.
  started = System.monotonic_time(:millisecond)

  results =
    currencies
    |> Stream.cycle()
    |> Enum.take(100)
    |> Task.async_stream(fn code -> Openrate.convert("ZAR", code, 1000) end,
      max_concurrency: 100,
      timeout: 30_000
    )
    |> Enum.count(fn
      {:ok, {:ok, _}} -> true
      _ -> false
    end)

  elapsed = System.monotonic_time(:millisecond) - started
  IO.puts("fanout  : #{results}/100 conversions across 100 processes in #{elapsed} ms")

  # 3. Cancel one mid-flight. Process.exit/2 reaches a process blocked on a
  #    socket; it does not reach native code inside a NIF.
  task = Task.async(fn -> Enum.each(1..10_000, fn _ -> Openrate.convert("ZAR", "USD", 1) end) end)
  Process.sleep(20)
  Task.shutdown(task, :brutal_kill)
  IO.puts("cancel  : killed a busy caller mid-flight; the VM did not notice")

  # 4. Time one out. Task.await/2 can abandon a slow call because the caller is
  #    a process, not a scheduler thread stuck in C.
  slow = Task.async(fn -> Process.sleep(5_000) end)

  timed_out =
    try do
      Task.await(slow, 100)
      false
    catch
      :exit, {:timeout, _} ->
        Task.shutdown(slow, :brutal_kill)
        true
    end

  IO.puts("timeout : a 100 ms deadline was enforced: #{timed_out}")

  # 5. And the gateway is still there.
  {:ok, r} = Openrate.convert("USD", "ZAR", 1)
  IO.puts("after   : 1 USD = #{Float.round(r["result"] / 1, 4)} ZAR")
after
  Openrate.stop()
end

IO.puts("stopped : ok")
