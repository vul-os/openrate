# Makefile for the openrate C examples.
#
#   make            build both
#   make run        build both and run them
#   make clean
#
# direct_convert LINKS against libopenrate, which is the shape a program with an
# installed library has. (ffi/test/smoke.c dlopen()s it instead, because a test
# that resolves every symbol by name is what catches a missing //export.)
#
# The library lives in dist/ffi/ after scripts/build-ffi.sh; set OPENRATE_LIB_DIR
# to override. -rpath is baked in so the built example runs from a checkout
# without DYLD_LIBRARY_PATH.

CC      ?= cc
CFLAGS  ?= -std=c11 -O2 -Wall -Wextra -Wpedantic
ROOT    := $(abspath ../..)

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
LIBNAME         := libopenrate-$(GOOS)-$(GOARCH).$(EXT)
OPENRATE_LIB_DIR ?= $(ROOT)/dist/ffi
LIB             := $(OPENRATE_LIB_DIR)/$(LIBNAME)
INCLUDE         := -I$(ROOT)/ffi/include -I.

BINS := direct_convert sidecar_convert

.PHONY: all run clean check-lib

all: $(BINS)

check-lib:
	@test -f "$(LIB)" || { \
	  echo "no $(LIBNAME) in $(OPENRATE_LIB_DIR)"; \
	  echo "build it:  (cd $(ROOT) && scripts/build-ffi.sh --host-only)"; \
	  echo "or point at one:  make OPENRATE_LIB_DIR=/path/to/dir"; \
	  exit 1; }

direct_convert: direct_convert.c jsonpeek.c jsonpeek.h | check-lib
	$(CC) $(CFLAGS) $(INCLUDE) -o $@ direct_convert.c jsonpeek.c \
	  $(LIB) -Wl,-rpath,$(OPENRATE_LIB_DIR)
ifeq ($(UNAME_S),Darwin)
	@# `go build -buildmode=c-shared` gives the dylib a bare install name with
	@# no @rpath prefix, so the -rpath above is never consulted and the loader
	@# fails with "Library not loaded: $(LIBNAME)". This rewrites the reference
	@# in OUR executable, which needs no change to the library. A packager
	@# should instead ship it with a proper install name:
	@#   install_name_tool -id @rpath/$(LIBNAME) $(LIBNAME)
	install_name_tool -change $(LIBNAME) @rpath/$(LIBNAME) $@
endif

# No libopenrate here on purpose: the sidecar example forks, and a process that
# has loaded the Go runtime must not.
sidecar_convert: sidecar_convert.c mini_http.c mini_http.h jsonpeek.c jsonpeek.h
	$(CC) $(CFLAGS) $(INCLUDE) -o $@ sidecar_convert.c mini_http.c jsonpeek.c

run: $(BINS)
	./direct_convert
	@echo
	./sidecar_convert

clean:
	rm -f $(BINS)
	rm -rf *.dSYM
