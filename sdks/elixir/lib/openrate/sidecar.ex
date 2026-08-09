defmodule Openrate.Sidecar do
  @moduledoc """
  Singleton GenServer that owns the openrate child process.

  It opens an Erlang `Port` to the binary. The BEAM closes Ports on GenServer
  termination and on VM shutdown, so the child dies with us; `close/2` also
  sends the OS process a signal as a belt-and-braces measure for the case where
  the Port was already gone.

  Why a `Port` and not `System.cmd/3`: `System.cmd/3` blocks until the process
  exits, which is wrong for a long-lived sidecar. A Port is a non-blocking,
  supervised handle with exit notifications and automatic teardown.

  ## `start/1` waits twice, because there are two questions

  `/healthz` is liveness: the binary answers it the instant the listener binds,
  with an empty rate book behind it. `/readyz` is readiness: 200 once the
  snapshot holds a currency, and 503 with a JSON body — `reason`, plus a
  `last_error` per source — until then. Returning from `start/1` on liveness
  alone is a false green: the caller converts immediately and gets
  `unknown or unreachable currency pair` for every pair, which reads like a bad
  currency code rather than an empty book. So `start/1` returns only once
  `/readyz` says yes, and on timeout returns the server's own reasons:

      {:error, "openrate has no rates after 30s: no rates yet: no source has
       returned a usable quote (ecb: … connect: connection refused)"}

  `:timeout` bounds the first wait, `:ready_timeout` the second.
  """

  use GenServer

  defstruct port: nil, os_pid: nil, base: nil

  # --- public API -------------------------------------------------------------

  def start_link(_opts) do
    GenServer.start_link(__MODULE__, %__MODULE__{}, name: __MODULE__)
  end

  @doc "Start the sidecar (idempotent). Returns `{:ok, base_url}`."
  def start(opts \\ []) do
    GenServer.call(__MODULE__, {:start, opts}, 120_000)
  end

  @doc "Stop the sidecar if running."
  def stop do
    GenServer.call(__MODULE__, :stop)
  end

  # --- GenServer --------------------------------------------------------------

  @impl true
  def init(state), do: {:ok, state}

  @impl true
  def handle_call({:start, _opts}, _from, %{port: port} = state) when is_port(port) do
    {:reply, {:ok, state.base}, state}
  end

  def handle_call({:start, opts}, _from, state) do
    port_num = Keyword.get(opts, :port) || free_port()
    addr = "127.0.0.1:#{port_num}"
    base = "http://#{addr}"

    case binary_path() do
      {:ok, bin} ->
        port = open_port(bin, build_env(addr, opts))
        os_pid = port_os_pid(port)

        # Live first, then ready. Both, or the child is killed and the caller
        # gets the reason rather than a sidecar it cannot use.
        with :ok <- wait_healthy(base, Keyword.get(opts, :timeout, 60_000)),
             :ok <- wait_ready(base, Keyword.get(opts, :ready_timeout, 30_000)) do
          {:reply, {:ok, base}, %{state | port: port, os_pid: os_pid, base: base}}
        else
          {:error, reason} ->
            close(port, os_pid)
            {:reply, {:error, reason}, %__MODULE__{}}
        end

      {:error, reason} ->
        {:reply, {:error, reason}, state}
    end
  end

  @impl true
  def handle_call(:stop, _from, state) do
    close(state.port, state.os_pid)
    {:reply, :ok, %__MODULE__{}}
  end

  @impl true
  def handle_info({port, {:data, data}}, %{port: port} = state) do
    IO.write(data)
    {:noreply, state}
  end

  def handle_info({port, {:exit_status, _status}}, %{port: port}) do
    {:noreply, %__MODULE__{}}
  end

  def handle_info(_msg, state), do: {:noreply, state}

  @impl true
  def terminate(_reason, state) do
    close(state.port, state.os_pid)
    :ok
  end

  # --- internals --------------------------------------------------------------

  defp open_port(bin, env) do
    Port.open({:spawn_executable, bin}, [
      :binary,
      :exit_status,
      :use_stdio,
      :stderr_to_stdout,
      args: [],
      env: env
    ])
  end

  defp port_os_pid(port) do
    case Port.info(port, :os_pid) do
      {:os_pid, pid} -> pid
      _ -> nil
    end
  end

  defp close(nil, _os_pid), do: :ok

  defp close(port, os_pid) do
    if is_port(port) and Port.info(port) != nil, do: Port.close(port)
    if is_integer(os_pid), do: System.cmd("kill", ["#{os_pid}"], stderr_to_stdout: true)
    :ok
  rescue
    _ -> :ok
  end

  # The binary reads its configuration from OPENRATE_* env vars, so the SDK
  # passes options that way rather than assembling a command line.
  defp build_env(addr, opts) do
    ui = if Keyword.get(opts, :ui, false), do: "true", else: "false"

    # OPENRATE_RATELIMIT: the binary defaults to 120 API requests/minute per IP,
    # which is anti-scraping for a PUBLIC deployment. This sidecar is on loopback
    # and serves exactly one client — us — and that budget is small enough that
    # a legitimate batch of conversions can exhaust it and hand the first real
    # call an HTTP 429. Default it off; pass `ratelimit: 120` to put it back.
    ratelimit = opts |> Keyword.get(:ratelimit, 0) |> to_string()

    base = [
      {~c"OPENRATE_ADDR", String.to_charlist(addr)},
      {~c"OPENRATE_UI", String.to_charlist(ui)},
      {~c"OPENRATE_RATELIMIT", String.to_charlist(ratelimit)}
    ]

    optional =
      [
        {~c"OPENRATE_BASE", Keyword.get(opts, :base)},
        {~c"OPENRATE_SOURCES", Keyword.get(opts, :sources)},
        {~c"OPENRATE_REFRESH", Keyword.get(opts, :refresh)}
      ]
      |> Enum.reject(fn {_k, v} -> is_nil(v) end)
      |> Enum.map(fn {k, v} -> {k, String.to_charlist(to_string(v))} end)

    extra =
      opts
      |> Keyword.get(:env, [])
      |> Enum.map(fn {k, v} ->
        {String.to_charlist(to_string(k)), String.to_charlist(to_string(v))}
      end)

    base ++ optional ++ extra
  end

  defp binary_path do
    cond do
      (env = System.get_env("OPENRATE_BINARY")) not in [nil, ""] ->
        {:ok, env}

      true ->
        name = if match?({:win32, _}, :os.type()), do: "openrate.exe", else: "openrate"
        bundled = Path.join([priv_dir(), "bin", name])

        cond do
          File.regular?(bundled) ->
            {:ok, bundled}

          (found = System.find_executable("openrate")) != nil ->
            {:ok, found}

          true ->
            {:error,
             "openrate binary not found. Set OPENRATE_BINARY, or build it: " <>
               "`go build -o sdks/elixir/priv/bin/openrate ./cmd/openrate`"}
        end
    end
  end

  # :code.priv_dir returns {:error, :bad_name} before the app is loaded; fall
  # back to a path relative to this module's source location.
  defp priv_dir do
    case :code.priv_dir(:openrate) do
      {:error, _} -> Path.join([__DIR__, "..", "..", "priv"])
      dir when is_list(dir) -> List.to_string(dir)
      dir -> dir
    end
  end

  defp free_port do
    {:ok, socket} = :gen_tcp.listen(0, [:binary, ip: {127, 0, 0, 1}, active: false])
    {:ok, port} = :inet.port(socket)
    :gen_tcp.close(socket)
    port
  end

  # Liveness. This only asks whether the listener is up; the book behind it is
  # very probably still empty when this returns, which is what wait_ready/2 is
  # for.
  #
  # 50 ms, and no backoff: /healthz is outside /api/, and the binary's per-IP
  # limiter guards /api/ only, so a tight poll here spends nothing the caller
  # wanted. (An earlier comment claimed the opposite and slowed the poll to
  # 200 ms to compensate. It was wrong about the limiter.)
  defp wait_healthy(base, timeout_ms) do
    deadline = System.monotonic_time(:millisecond) + timeout_ms
    do_wait_healthy(base, deadline)
  end

  defp do_wait_healthy(base, deadline) do
    if System.monotonic_time(:millisecond) > deadline do
      {:error, "openrate did not start listening within the timeout"}
    else
      case Openrate.HTTP.get(base, "/healthz") do
        {:ok, 200, _body} ->
          :ok

        _ ->
          Process.sleep(50)
          do_wait_healthy(base, deadline)
      end
    end
  end

  # Readiness — a different question, and the one that decides whether a
  # conversion can succeed. /readyz is 200 with rates, 503 without, and the 503
  # carries the reasons; /readyz is outside /api/ too, so this poll is also
  # free of the limiter.
  @ready_poll_ms 150

  defp wait_ready(base, timeout_ms) do
    deadline = System.monotonic_time(:millisecond) + timeout_ms
    do_wait_ready(base, deadline, timeout_ms, {:transport, "openrate never answered /readyz"})
  end

  defp do_wait_ready(base, deadline, timeout_ms, {_kind, text} = why) do
    if System.monotonic_time(:millisecond) > deadline do
      {:error, "openrate has no rates after #{div(timeout_ms, 1000)}s: #{text}"}
    else
      case Openrate.HTTP.get(base, "/readyz") do
        {:ok, 200, _body} ->
          :ok

        # The 503's BODY is the whole point of polling this endpoint —
        # Openrate.HTTP hands it back whatever the status, so the reason is in
        # hand without a second request.
        {:ok, _status, body} ->
          Process.sleep(@ready_poll_ms)
          do_wait_ready(base, deadline, timeout_ms, {:server, not_ready_reason(body)})

        # Not listening: yet, or any more. This never displaces a reason the
        # server already gave us — "ecb: connection refused" explains more than
        # a closed socket does.
        {:error, reason} ->
          Process.sleep(@ready_poll_ms)
          do_wait_ready(base, deadline, timeout_ms, keep({:transport, inspect(reason)}, why))
      end
    end
  end

  defp keep({:transport, _}, {:server, _} = server), do: server
  defp keep(new, _current), do: new

  # `reason`, then `name: last_error` for every source that has one.
  #
  # `last_error` is `omitempty` on the wire: a source nobody has tried yet has
  # no such key at all, so this must degrade to the reason alone rather than
  # reporting `ecb: nil`.
  defp not_ready_reason(body) do
    with %{} = doc <- :json.decode(body) do
      reason = Map.get(doc, "reason") || "not ready, and it did not say why"

      details =
        doc
        |> Map.get("sources", [])
        |> Enum.filter(&(Map.get(&1, "last_error") not in [nil, ""]))
        |> Enum.map_join("; ", &"#{&1["name"]}: #{&1["last_error"]}")

      if details == "", do: reason, else: "#{reason} (#{details})"
    else
      _ -> "not ready (/readyz answered something that was not a JSON object)"
    end
  rescue
    _ -> "not ready (/readyz body was not JSON: #{String.slice(body, 0, 120)})"
  end
end
