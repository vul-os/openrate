defmodule Openrate.MixProject do
  use Mix.Project

  # Read from the workspace's VERSION file so this package cannot drift
  # from the engine it binds. Six of these manifests sat at 0.1.0 behind a
  # shipped release because each carried its own literal.
  @version Path.join(__DIR__, "../../VERSION") |> File.read!() |> String.trim()

  def project do
    [
      app: :openrate,
      version: @version,
      elixir: "~> 1.12",
      start_permanent: Mix.env() == :prod,
      description: "Open FX rates from open sources — the openrate server, managed for you.",
      package: package(),
      deps: deps()
    ]
  end

  def application do
    # No application callback needed: the sidecar GenServer starts lazily on
    # first use.
    [extra_applications: [:logger]]
  end

  defp deps do
    # The sidecar uses only OTP (Port + :gen_tcp + :json). No runtime deps.
    []
  end

  defp package do
    [
      # OTP app atom stays :openrate; only the Hex name is scoped.
      name: "vul_os_openrate",
      licenses: ["MIT"],
      links: %{
        "Homepage" => "https://vulos.org/projects/openrate/",
        "GitHub" => "https://github.com/vul-os/openrate"
      },
      # priv/bin holds the bundled binary (gitignored; built with
      # `go build -o sdks/elixir/priv/bin/openrate ./cmd/openrate`).
      files: ~w(lib mix.exs README.md priv examples)
    ]
  end
end
