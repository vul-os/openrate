# Makefile for the openrate C++ examples.
#
#   make            build both
#   make run        build both and run them
#   make clean
#
# openrate.hpp is header-only; there is nothing to build but the examples.
# direct_convert links libopenrate, sidecar_convert deliberately does not (it
# forks).

CXX      ?= c++
CXXFLAGS ?= -std=c++17 -O2 -Wall -Wextra -Wpedantic
ROOT     := $(abspath ../..)

UNAME_S := $(shell uname -s)
UNAME_M := $(shell uname -m)
ifeq ($(UNAME_S),Darwin)
  GOOS := darwin
  EXT := dylib
else
  GOOS := linux
  EXT := so
endif
ifeq ($(UNAME_M),arm64)
  GOARCH := arm64
else ifeq ($(UNAME_M),aarch64)
  GOARCH := arm64
else
  GOARCH := amd64
endif

# build-ffi.sh names the artifact per target, so there is no plain -lopenrate to
# link: the file is passed by path.
LIBNAME          := libopenrate-$(GOOS)-$(GOARCH).$(EXT)
OPENRATE_LIB_DIR ?= $(ROOT)/dist/ffi
LIB              := $(OPENRATE_LIB_DIR)/$(LIBNAME)
INCLUDE          := -I$(ROOT)/ffi/include -I.

BINS := direct_convert sidecar_convert

.PHONY: all run clean check-lib

all: $(BINS)

check-lib:
	@test -f "$(LIB)" || { \
	  echo "no $(LIBNAME) in $(OPENRATE_LIB_DIR)"; \
	  echo "build it:  (cd $(ROOT) && scripts/build-ffi.sh --host-only)"; \
	  echo "or point at one:  make OPENRATE_LIB_DIR=/path/to/dir"; \
	  exit 1; }

direct_convert: direct_convert.cpp openrate.hpp | check-lib
	$(CXX) $(CXXFLAGS) $(INCLUDE) -o $@ direct_convert.cpp \
	  $(LIB) -Wl,-rpath,$(OPENRATE_LIB_DIR)
ifeq ($(UNAME_S),Darwin)
	@# cgo gives the dylib a bare install name with no @rpath prefix, so the
	@# -rpath above is never consulted. See ../c/Makefile.
	install_name_tool -change $(LIBNAME) @rpath/$(LIBNAME) $@
endif

sidecar_convert: sidecar_convert.cpp
	$(CXX) $(CXXFLAGS) -o $@ sidecar_convert.cpp

run: $(BINS)
	./direct_convert
	@echo
	./sidecar_convert

clean:
	rm -f $(BINS)
	rm -rf *.dSYM
