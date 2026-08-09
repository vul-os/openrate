/*
 * smoke.c — the C ABI, exercised the way a foreign host actually exercises it.
 *
 * An ABI with no executed test is not an ABI, it is a header. This program
 * dlopen()s the shared library exactly as a Python ctypes binding or a Ruby
 * FFI binding would, resolves every documented symbol by name, and drives a
 * complete session: build an engine, load a book of rates, convert, read the
 * metadata, construct a refresher WITHOUT fetching, then close everything and
 * check the library says it is holding nothing.
 *
 * It links against no part of openrate at build time. If the symbols were
 * renamed, if a signature changed shape, if the library failed to export
 * something the header promises — this fails, and the Go test suite would not.
 *
 * Two things here are worth reading closely.
 *
 * 1. THE SOCKET CENSUS. openrate's central claim is that an engine sends no
 *    packets. The Go side counts HTTP round trips (see
 *    TestZeroPacketsThroughTheABI); this counts, at the operating-system level
 *    and through a really-loaded shared library, whether any socket exists in
 *    the process at all. Be precise about what that proves: it detects a socket
 *    OPEN at the moment of sampling, so a connection that opened and closed
 *    between samples would escape it. It is the OS-level half of the claim, not
 *    the whole of it, and the Go-level round-trip counter is the other half.
 *
 * 2. THE CONTROLS. Every assertion that something did not happen is preceded or
 *    followed by a deliberate instance of that thing, so a measurement wired to
 *    nothing fails loudly instead of reporting a clean result forever. The
 *    socket census gets a real socket() call; the JSON checks get a document
 *    that must NOT match.
 *
 * Usage: smoke /path/to/libopenrate.{so,dylib}
 */

#define _POSIX_C_SOURCE 200809L

#include <dlfcn.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <unistd.h>

#include "openrate.h"

/* ------------------------------------------------------------------ */
/* Tiny test harness                                                    */
/* ------------------------------------------------------------------ */

static int checks = 0;
static int failures = 0;

static void ok(int cond, const char *what) {
  checks++;
  if (cond) {
    printf("  ok   %s\n", what);
  } else {
    failures++;
    printf("  FAIL %s\n", what);
  }
}

static void fatal(const char *what) {
  fprintf(stderr, "smoke: fatal: %s\n", what);
  exit(2);
}

/* ------------------------------------------------------------------ */
/* The ABI, resolved by name — nothing is linked                        */
/* ------------------------------------------------------------------ */

typedef uint64_t (*fn_new)(const char *, char **);
typedef uint64_t (*fn_refresher_new)(uint64_t, const char *, char **);
typedef char *(*fn_call)(uint64_t, const char *, const char *, char **);
typedef void (*fn_close)(uint64_t);
typedef void (*fn_free)(char *);
typedef const char *(*fn_abi_version)(void);
typedef uint64_t (*fn_open_handles)(void);

static fn_new openrate_new_;
static fn_refresher_new openrate_refresher_new_;
static fn_call openrate_call_;
static fn_close openrate_close_;
static fn_free openrate_free_;
static fn_abi_version openrate_abi_version_;
static fn_open_handles openrate_open_handles_;

static void *resolve(void *lib, const char *name) {
  dlerror();
  void *sym = dlsym(lib, name);
  const char *err = dlerror();
  if (err != NULL || sym == NULL) {
    fprintf(stderr, "smoke: the library does not export %s: %s\n", name,
            err ? err : "(null symbol)");
    exit(2);
  }
  return sym;
}

/* ------------------------------------------------------------------ */
/* Socket census                                                        */
/* ------------------------------------------------------------------ */

static int count_sockets(void) {
  int max = (int)sysconf(_SC_OPEN_MAX);
  if (max <= 0 || max > 8192) {
    max = 8192;
  }
  int n = 0;
  for (int fd = 0; fd < max; fd++) {
    struct stat st;
    if (fstat(fd, &st) != 0) {
      continue;
    }
    if (S_ISSOCK(st.st_mode)) {
      n++;
    }
  }
  return n;
}

/* ------------------------------------------------------------------ */
/* Minimal JSON probing. The point is to read the numbers the library    */
/* produced, not to be a parser.                                        */
/* ------------------------------------------------------------------ */

/*
 * number_after finds the first occurrence of key whose value parses as a
 * number. It scans past occurrences that do not, because Go marshals a map
 * with its keys sorted and "rate" is both an object at the top level and a
 * number inside it — a probe that stopped at the first match would read the
 * opening brace and report failure.
 */
static int number_after(const char *doc, const char *key, double *out) {
  const char *p = doc;
  size_t n = strlen(key);
  while ((p = strstr(p, key)) != NULL) {
    const char *val = p + n;
    char *end = NULL;
    double v = strtod(val, &end);
    if (end != val) {
      *out = v;
      return 1;
    }
    p = val;
  }
  return 0;
}

/* ------------------------------------------------------------------ */

static const char *EDGES =
    "{\"built_at\":\"2026-08-09T12:00:00Z\",\"edges\":["
    "{\"from\":\"USD\",\"to\":\"ZAR\",\"rate\":18.5,\"source\":\"alpha\","
    "\"time\":\"2026-08-09T11:00:00Z\"},"
    "{\"from\":\"EUR\",\"to\":\"USD\",\"rate\":1.09,\"source\":\"alpha\","
    "\"time\":\"2026-08-09T10:00:00Z\"}]}";

int main(int argc, char **argv) {
  if (argc != 2) {
    fprintf(stderr, "usage: %s /path/to/libopenrate.{so,dylib}\n", argv[0]);
    return 2;
  }

  printf("smoke: dlopen %s\n", argv[1]);
  void *lib = dlopen(argv[1], RTLD_NOW | RTLD_LOCAL);
  if (lib == NULL) {
    fprintf(stderr, "smoke: dlopen failed: %s\n", dlerror());
    return 2;
  }

  openrate_new_ = (fn_new)resolve(lib, "openrate_new");
  openrate_refresher_new_ = (fn_refresher_new)resolve(lib, "openrate_refresher_new");
  openrate_call_ = (fn_call)resolve(lib, "openrate_call");
  openrate_close_ = (fn_close)resolve(lib, "openrate_close");
  openrate_free_ = (fn_free)resolve(lib, "openrate_free");
  openrate_abi_version_ = (fn_abi_version)resolve(lib, "openrate_abi_version");
  openrate_open_handles_ = (fn_open_handles)resolve(lib, "openrate_open_handles");
  printf("smoke: every documented symbol resolved\n");

  char *err = NULL;
  char *out = NULL;
  double v = 0;

  /* --- version -------------------------------------------------------- */
  printf("\nversion\n");
  const char *runtime_version = openrate_abi_version_();
  ok(runtime_version != NULL && runtime_version[0] != '\0',
     "openrate_abi_version returns a non-empty string");
  printf("       header says %s, library says %s\n", OPENRATE_ABI_VERSION,
         runtime_version ? runtime_version : "(null)");
  ok(runtime_version != NULL && strcmp(runtime_version, OPENRATE_ABI_VERSION) == 0,
     "the loaded library's version matches the header this program was built against");

  /* --- the zero-socket window ---------------------------------------- */
  /*
   * Everything between the two censuses is the engine path: the sequence a
   * host performs when it has its own rates and wants openrate as a
   * calculator. Constructing a refresher is inside the window too, because
   * constructing one is not fetching — only its "refresh" and "start"
   * methods are, and this program never calls them.
   */
  printf("\nengine path, with a socket census around it\n");
  int sockets_before = count_sockets();

  uint64_t eng = openrate_new_("{\"base\":\"ZAR\",\"quiet\":true}", &err);
  ok(eng != 0, "openrate_new returned a handle");
  ok(err == NULL, "openrate_new set no error");
  if (eng == 0) {
    fprintf(stderr, "smoke: %s\n", err ? err : "(no message)");
    fatal("cannot continue without an engine");
  }

  out = openrate_call_(eng, "load", EDGES, &err);
  ok(out != NULL && err == NULL, "load accepted a book of rates");
  ok(out != NULL && strstr(out, "\"ZAR\"") != NULL,
     "load reported the currencies it materialized");
  openrate_free_(out);

  out = openrate_call_(eng, "convert", "{\"from\":\"USD\",\"to\":\"ZAR\",\"amount\":100}", &err);
  ok(out != NULL && err == NULL, "convert answered");
  if (out == NULL) {
    fprintf(stderr, "smoke: %s\n", err ? err : "(no message)");
    fatal("the one conversion this test exists to perform did not happen");
  }
  ok(number_after(out, "\"result\":", &v) && v == 1850.0,
     "USD 100 -> ZAR 1850 exactly (18.5 * 100)");
  ok(number_after(out, "\"rate\":", &v) && v == 18.5, "the rate came back as 18.5");
  ok(strstr(out, "\"alpha\"") != NULL, "the answer carries its provenance");

  /* CONTROL for the JSON probing above: a value that is NOT there must not
   * be found. Otherwise number_after returning a stale success would make
   * every assertion above meaningless. */
  ok(!number_after(out, "\"nonexistent_field\":", &v),
     "control: the JSON probe does not find a field that is absent");
  openrate_free_(out);

  /* Triangulation, so the graph search is exercised and not just a lookup. */
  out = openrate_call_(eng, "convert", "{\"from\":\"EUR\",\"to\":\"ZAR\",\"amount\":1}", &err);
  ok(out != NULL && err == NULL, "a triangulated pair converts");
  if (out != NULL) {
    ok(number_after(out, "\"result\":", &v) && v == 1.09 * 18.5,
       "EUR -> ZAR is the product of the two legs, bit for bit");
    openrate_free_(out);
  }

  out = openrate_call_(eng, "meta", NULL, &err);
  ok(out != NULL && err == NULL, "meta answers with a NULL request document");
  ok(out != NULL && strstr(out, "\"sources\":[]") != NULL,
     "an engine nobody refreshes reports no sources");
  openrate_free_(out);

  /* Constructing a refresher is inside the census window on purpose. */
  uint64_t ref = openrate_refresher_new_(eng, "{\"sources\":\"ecb,coinbase\",\"quiet\":true}", &err);
  ok(ref != 0 && err == NULL, "openrate_refresher_new returned a second, separate handle");
  ok(ref != eng, "the refresher handle is not the engine handle");

  out = openrate_call_(ref, "status", NULL, &err);
  ok(out != NULL && err == NULL, "the refresher reports its status");
  ok(out != NULL && strstr(out, "\"ecb\"") != NULL && strstr(out, "\"coinbase\"") != NULL,
     "status names the sources it was configured with");
  ok(out != NULL && strstr(out, "\"edges\":0") != NULL,
     "no source has fetched anything");
  openrate_free_(out);

  int sockets_after = count_sockets();
  printf("       sockets open before: %d, after: %d\n", sockets_before, sockets_after);
  ok(sockets_after == sockets_before,
     "the engine path left no socket open in this process");

  /* CONTROL for the census. A counter that cannot see a socket appear would
   * report equality forever, which reads exactly like a pass. */
  int probe = socket(AF_INET, SOCK_STREAM, 0);
  if (probe < 0) {
    fatal("could not create a socket for the census control");
  }
  int sockets_with_probe = count_sockets();
  ok(sockets_with_probe == sockets_before + 1,
     "control: the census sees a socket this program deliberately opened");
  close(probe);

  /* --- errors are strings, not crashes -------------------------------- */
  printf("\nerror paths\n");

  err = NULL;
  out = openrate_call_(eng, "covnert", "{}", &err);
  ok(out == NULL, "a misspelled method returns NULL");
  ok(err != NULL, "a misspelled method sets *err");
  if (err != NULL) {
    ok(strstr(err, "convert") != NULL, "the message lists the methods that do exist");
    openrate_free_(err);
    err = NULL;
  }

  out = openrate_call_(eng, "convert", "{\"from\":\"USD\",\"to\":\"XXX\"}", &err);
  ok(out == NULL && err != NULL, "an unreachable pair is an error, not a zero");
  if (err != NULL) {
    ok(strstr(err, "unknown or unreachable currency pair") != NULL,
       "the message is fx's own sentinel text, so a binding can match on it");
    openrate_free_(err);
    err = NULL;
  }

  out = openrate_call_(eng, "convert", "{\"from\":", &err);
  ok(out == NULL && err != NULL, "truncated request JSON is refused");
  openrate_free_(err);
  err = NULL;

  uint64_t bogus = openrate_new_("{\"base\":", &err);
  ok(bogus == 0 && err != NULL, "truncated config JSON returns handle 0 with a message");
  openrate_free_(err);
  err = NULL;

  out = openrate_call_(999999, "meta", "{}", &err);
  ok(out == NULL && err != NULL, "a handle that was never issued is a clean error");
  openrate_free_(err);
  err = NULL;

  /* The engine's methods are not the refresher's, and vice versa. This is the
   * Engine/Refresher split, enforced at the ABI rather than merely documented. */
  out = openrate_call_(eng, "refresh", "{}", &err);
  ok(out == NULL && err != NULL,
     "an engine handle refuses \"refresh\" — fetching needs a refresher, always");
  openrate_free_(err);
  err = NULL;

  out = openrate_call_(ref, "convert", "{}", &err);
  ok(out == NULL && err != NULL, "a refresher handle refuses \"convert\"");
  openrate_free_(err);
  err = NULL;

  /* err may be NULL: the failure is still reported by the return value. */
  out = openrate_call_(eng, "nope", "{}", NULL);
  ok(out == NULL, "a NULL err out-parameter is allowed");

  /* --- lifetime -------------------------------------------------------- */
  printf("\nlifetime\n");

  uint64_t live = openrate_open_handles_();
  ok(live >= 2, "the library reports the handles this program opened");

  openrate_close_(eng); /* also closes the refresher built over it */
  ok(openrate_open_handles_() == live - 2,
     "closing the engine released the refresher with it");

  out = openrate_call_(eng, "meta", "{}", &err);
  ok(out == NULL && err != NULL, "use after close is an error, not a crash");
  if (err != NULL) {
    ok(strstr(err, "not open") != NULL, "the message says the handle is not open");
    openrate_free_(err);
    err = NULL;
  }

  openrate_close_(eng);  /* double close */
  openrate_close_(ref);  /* already gone */
  openrate_close_(0);    /* never valid */
  openrate_close_(12345);
  ok(1, "double closes and invented handles are survivable");

  openrate_free_(NULL);
  ok(1, "openrate_free(NULL) is safe");

  /* --- verdict --------------------------------------------------------- */
  printf("\nsmoke: %d checks, %d failure(s)\n", checks, failures);

  /*
   * Coverage floor. A smoke test that stopped executing its body — an early
   * return, a #if that excluded everything — would print "0 failures" and exit
   * 0, which is the exact shape of a green tick over nothing.
   */
  if (checks < 35) {
    fprintf(stderr,
            "smoke: only %d checks ran; at least 35 are expected. This run did not "
            "exercise the ABI, whatever its exit code says.\n",
            checks);
    return 3;
  }
  return failures == 0 ? 0 : 1;
}
