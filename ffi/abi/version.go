package abi

// Version is the string openrate_abi_version() returns. It is the openrate
// package version, so a host that has loaded a stale .so from its library path
// can find out before it makes a call that means something different than it
// used to.
//
// It is a compiled-in constant rather than something read from VERSION at
// runtime, because a shared library has no checkout to read from. That makes
// drift possible, so it is pinned: TestVersionMatchesTheVERSIONFile compares it
// to the repository's VERSION file, and TestHeaderDeclaresTheSameVersion
// compares it to the OPENRATE_ABI_VERSION macro in ffi/include/openrate.h, so a
// release that bumps one and not the others fails the build.
//
// KEEP IN SYNC WITH: /VERSION and ffi/include/openrate.h.
const Version = "0.1.2"
