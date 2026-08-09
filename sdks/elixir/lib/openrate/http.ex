defmodule Openrate.HTTP do
  @moduledoc """
  A minimal HTTP/1.1 GET over `:gen_tcp`, so this package has no dependency —
  not even `:inets`, which drags in `:ssl` and `:public_key` before it will
  answer a plain-http request.

  Everything openrate's JSON API exposes is a GET against loopback, which is why
  something this small is enough. Point a real HTTP client at
  `Openrate.base_url/0` if you want pooling, retries or TLS.
  """

  @doc """
  `GET base <> path`. Returns `{:ok, status, body}` or `{:error, reason}`.

  The socket is closed on every path, including a failed read — that is what the
  `try/after` is for.
  """
  @spec get(String.t(), String.t(), timeout()) ::
          {:ok, non_neg_integer(), binary()} | {:error, term()}
  def get(base, path, timeout \\ 30_000)

  def get("http://" <> hostport, path, timeout) do
    [host, port] = String.split(hostport, ":", parts: 2)

    case :gen_tcp.connect(
           String.to_charlist(host),
           String.to_integer(port),
           [:binary, active: false, packet: :raw],
           5_000
         ) do
      {:ok, sock} ->
        try do
          request = [
            "GET ",
            path,
            " HTTP/1.1\r\nHost: ",
            hostport,
            "\r\nAccept: application/json\r\nConnection: close\r\n\r\n"
          ]

          with :ok <- :gen_tcp.send(sock, request),
               {:ok, raw} <- read_all(sock, "", timeout) do
            parse(raw)
          end
        after
          :gen_tcp.close(sock)
        end

      {:error, reason} ->
        {:error, reason}
    end
  end

  def get(base, _path, _timeout), do: {:error, {:unsupported_scheme, base}}

  defp read_all(sock, acc, timeout) do
    case :gen_tcp.recv(sock, 0, timeout) do
      {:ok, more} -> read_all(sock, acc <> more, timeout)
      {:error, :closed} -> {:ok, acc}
      {:error, reason} -> {:error, reason}
    end
  end

  # `Connection: close` means the body runs to EOF, so the whole response is in
  # hand by the time this is called and no chunk decoding is needed.
  defp parse(raw) do
    case :binary.split(raw, "\r\n\r\n") do
      [head, body] ->
        ["HTTP/1." <> <<_::binary-size(1)>> <> " " <> <<code::binary-size(3)>> <> _ | headers] =
          String.split(head, "\r\n")

        body =
          if Enum.any?(headers, &(String.downcase(&1) =~ "transfer-encoding: chunked")) do
            dechunk(body, "")
          else
            body
          end

        {:ok, String.to_integer(code), body}

      _ ->
        {:error, :malformed_response}
    end
  end

  defp dechunk(rest, acc) do
    case :binary.split(rest, "\r\n") do
      [size_line, tail] ->
        case Integer.parse(String.trim(size_line), 16) do
          {0, _} ->
            acc

          {size, _} ->
            <<chunk::binary-size(size), "\r\n", tail::binary>> = tail
            dechunk(tail, acc <> chunk)

          :error ->
            acc
        end

      _ ->
        acc
    end
  end
end
