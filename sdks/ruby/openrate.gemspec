# frozen_string_literal: true

Gem::Specification.new do |spec|
  spec.name = "vulos-openrate"
  spec.version = "0.1.8"
  spec.summary = "Open FX rates from open sources — embedded locally, as a managed sidecar or in-process."
  spec.description = "Thin Ruby wrapper around openrate: spawn and supervise the binary on a " \
    "loopback port, or load libopenrate in-process through fiddle. Same JSON either way."
  spec.authors = ["openrate"]
  spec.homepage = "https://vulos.org/projects/openrate/"
  spec.license = "MIT"
  spec.required_ruby_version = ">= 2.7"

  spec.files = Dir["lib/**/*.rb", "bin/**/*", "examples/**/*.rb", "README.md"]
  spec.require_paths = ["lib"]

  # `require "openrate/ffi"` (the in-process C-ABI mode) uses fiddle, which is a
  # DEFAULT gem — present in every supported Ruby without being declared. It is
  # listed here only so a Gemfile.lock records the version actually used; no
  # third-party FFI binding is pulled in.
  spec.add_dependency "fiddle", ">= 1.0"

  spec.metadata = {
    "homepage_uri" => "https://vulos.org/projects/openrate/",
    "source_code_uri" => "https://github.com/vul-os/openrate"
  }
end
