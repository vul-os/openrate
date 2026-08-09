defmodule Openrate.MixProject do
  use Mix.Project

  def project do
    [
      app: :openrate,
      version: "0.1.2",
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
