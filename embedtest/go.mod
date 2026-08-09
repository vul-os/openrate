// embedtest is a SEPARATE Go module on purpose.
//
// openrate's claim is that it is embeddable: a host program can take the
// library and nothing else. The only mechanical proof of that is to stand
// outside and try, so that if this module compiles, the public API really was
// sufficient. A test living inside the openrate module could not prove that:
// it can reach internal/ freely, so it would pass whether the wall existed or
// not.
//
// "Outside" means outside the import PATH, not outside the module. Go does not
// key the internal/ rule on module boundaries — see the next paragraph, which
// is the whole reason this file carries a warning.
//
// THE MODULE PATH IS LOAD-BEARING AND MUST STAY OUTSIDE github.com/vul-os/openrate/.
// The internal/ rule is applied to the import PATH, not to the module
// boundary: a module named github.com/vul-os/openrate/embedtest sits inside
// the tree rooted at openrate's internal/ parent, so it may import internal/
// freely. That was the first version of this file, and a blank import of
// github.com/vul-os/openrate/internal/redact compiled and the suite went
// green — a wall guard with no wall. internalprobe/ now holds that discovery
// as a permanent, executed check: TestG1InternalPackagesStayUnreachable builds
// it and requires the compiler to refuse.
//
// The replace below points at the checkout one directory up because openrate
// is not published under a version tag yet. It does not weaken the guard: the
// refusal above was observed through exactly this replace.
//
// No require line other than openrate itself may ever appear here. openrate is
// stdlib-only and its go.mod has no require block; a third-party dependency in
// the embeddability suite would be testing something other than openrate.
module github.com/vul-os/openrate-embedtest

go 1.25

toolchain go1.25.12

require github.com/vul-os/openrate v0.0.0

replace github.com/vul-os/openrate => ../
