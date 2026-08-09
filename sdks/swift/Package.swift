// swift-tools-version:5.9
//
// No `unsafeFlags`, no module map, no system-library target, and no bridging
// header. The C ABI is reached with `dlopen`/`dlsym` plus `@convention(c)`
// function types, which means:
//
//   - `swift build` works with nothing on the machine but a Swift toolchain.
//   - The library is located at RUN time, so one build works whether
//     `libopenrate-<goos>-<goarch>.dylib` sits in `dist/ffi/`, on
//     `DYLD_LIBRARY_PATH`, or at whatever `$OPENRATE_LIBRARY` names.
//   - This package can be a dependency of another package. A target with
//     `unsafeFlags` cannot be, which rules out the link-time approach for
//     anything published.
//
// Zero external dependencies.

import PackageDescription

let package = Package(
    name: "OpenRate",
    platforms: [
        // Tested on macOS 15.7.3 (Apple silicon) with Swift 6.1.2.
        .macOS(.v13)
    ],
    products: [
        .library(name: "OpenRate", targets: ["OpenRate"]),
        .executable(name: "openrate-direct-example", targets: ["openrate-direct-example"]),
        .executable(name: "openrate-sidecar-example", targets: ["openrate-sidecar-example"]),
    ],
    targets: [
        .target(name: "OpenRate"),
        .executableTarget(name: "openrate-direct-example", dependencies: ["OpenRate"]),
        .executableTarget(name: "openrate-sidecar-example", dependencies: ["OpenRate"]),
        .testTarget(name: "OpenRateTests", dependencies: ["OpenRate"]),
    ]
)
