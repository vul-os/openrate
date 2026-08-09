defmodule Openrate do
  @moduledoc """
  openrate — open FX rates from open sources, embedded locally for Elixir.

  Instead of running a server yourself, this starts the `openrate` binary as a
  local OS process (via an Erlang `Port`) on `127.0.0.1` and answers questions
  against it:

      {:ok, r} = Openrate.convert("USD", "ZAR", 100)
      r["result"]                     # => 1842.0

      {:ok, rates} = Openrate.rates("ZAR")
      {:ok, meta}  = Openrate.meta()
      {:ok, base}  = Openrate.base_url()   # "http://127.0.0.1:<port>"

  The sidecar is managed by a singleton `GenServer` (`Openrate.Sidecar`). It
  starts lazily on first use and is terminated when the GenServer stops — which
  the BEAM does on shutdown, closing the Port and reaping the child.

  ## There is no in-process mode here

  openrate ships a C ABI and every other SDK binds it. Elixir does not,
  deliberately: OTP has no FFI, so in-process would mean a NIF, and a NIF trades
  away the isolation that is the reason to be on the BEAM at all. README.md sets
  out the reasoning in full.
  """

  @type response :: {:ok, map()} | {:error, term()}

  @doc """
  Start the sidecar (idempotent). Returns `{:ok, base_url}`.

  It returns only once the child answers `GET /readyz` — i.e. once a conversion
  would actually succeed. Waiting only for the listener (`/healthz`) is a false
  green: the book is still empty, and every pair comes back as
  `unknown or unreachable currency pair`. If the rates never arrive the error is
  the server's own explanation, not a bare timeout:

      {:error, "openrate has no rates after 30s: no rates yet: no source has
       returned a usable quote (ecb: … connect: connection refused)"}

  Options: `:port`, `:base` (default presentation currency), `:sources`
  (comma-separated), `:refresh` (a Go duration such as `"1h"`), `:ui`
  (default `false`), `:ratelimit` (API requests/minute per IP, default `0` —
  off; see `Openrate.Sidecar`), `:env`, `:timeout` (ms to wait for the listener,
  default 60_000), `:ready_timeout` (ms to then wait for rates, default 30_000).
  """
  @spec start(keyword()) :: {:ok, String.t()} | {:error, term()}
  def start(opts \\ []) do
    ensure_started()
    Openrate.Sidecar.start(opts)
  end

  @doc "The running base URL, starting the sidecar if needed."
  @spec base_url(keyword()) :: {:ok, String.t()} | {:error, term()}
  def base_url(opts \\ []), do: start(opts)

  @doc "The JSON API base (`…/api/v1`)."
  @spec api_base_url(keyword()) :: {:ok, String.t()} | {:error, term()}
  def api_base_url(opts \\ []) do
    with {:ok, base} <- base_url(opts), do: {:ok, base <> "/api/v1"}
  end

  @doc """
  `GET /api/v1/convert`. Amount defaults to 1.

      {:ok, %{"result" => r}} = Openrate.convert("EUR", "USD", 100)
  """
  @spec convert(String.t(), String.t(), number()) :: response()
  def convert(from, to, amount \\ 1) do
    get("/convert", from: from, to: to, amount: amount)
  end

  @doc "`GET /api/v1/rates` — the all-pairs snapshot for a base currency."
  @spec rates(String.t() | nil) :: response()
  def rates(base \\ nil) do
    if base, do: get("/rates", base: base), else: get("/rates", [])
  end

  @doc "`GET /api/v1/meta` — sources, freshness and the currency list."
  @spec meta() :: response()
  def meta, do: get("/meta", [])

  @doc """
  One GET against the JSON API. `path` is relative to `/api/v1`.

  A non-200 is `{:error, {:http, status, message}}` — openrate answers errors as
  `{"error": "..."}`, where the C ABI hands back a plain string in `*err`.
  """
  @spec get(String.t(), keyword()) :: response()
  def get(path, params \\ []) do
    with {:ok, api} <- api_base_url(),
         "http://" <> _ = base <- base_of(api),
         {:ok, status, body} <- Openrate.HTTP.get(base, "/api/v1" <> path <> query(params)) do
      decode(status, body)
    else
      {:error, reason} -> {:error, reason}
      other -> {:error, other}
    end
  end

  @doc "Stop the sidecar if running."
  @spec stop() :: :ok
  def stop do
    ensure_started()
    Openrate.Sidecar.stop()
  end

  # --- internals --------------------------------------------------------------

  defp base_of(api), do: String.replace_suffix(api, "/api/v1", "")

  defp query([]), do: ""

  defp query(params) do
    "?" <>
      Enum.map_join(params, "&", fn {k, v} ->
        "#{URI.encode_www_form(to_string(k))}=#{URI.encode_www_form(to_string(v))}"
      end)
  end

  defp decode(status, body) do
    case :json.decode(body) do
      decoded when status == 200 and is_map(decoded) ->
        {:ok, decoded}

      %{"error" => message} ->
        {:error, {:http, status, message}}

      _ ->
        {:error, {:http, status, String.slice(body, 0, 200)}}
    end
  rescue
    _ -> {:error, {:http, status, "response was not JSON: " <> String.slice(body, 0, 200)}}
  end

  # Start the singleton GenServer on demand; no supervision tree required for
  # simple usage.
  defp ensure_started do
    case Process.whereis(Openrate.Sidecar) do
      nil ->
        case Openrate.Sidecar.start_link([]) do
          {:ok, _pid} -> :ok
          {:error, {:already_started, _pid}} -> :ok
          other -> other
        end

      _pid ->
        :ok
    end
  end
end
