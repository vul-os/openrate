/*
 * bench.c — how much does the sidecar cost?
 *
 * The honest question behind "you can use openrate in-process" is not whether
 * a function call beats a socket — of course it does — but by how much, for
 * THIS workload, so a reader can decide whether the costs of loading a Go
 * runtime into their process (see ffi/README.md) buy them anything they care
 * about.
 *
 * So both sides are measured here, from the same C program, against the same
 * snapshot, asking for the same conversion:
 *
 *   IN-PROCESS  openrate_call(h, "convert", ...) on a dlopen'd shared library.
 *   LOOPBACK    GET /api/v1/convert on openrate's real HTTP server, over a
 *               real TCP connection to 127.0.0.1, with a hand-rolled HTTP/1.1
 *               client.
 *
 * The HTTP side is deliberately flattered. The connection is opened once and
 * reused (keep-alive), there is no TLS, there is no proxy, the server is on the
 * same machine, and the client is about as thin as an HTTP client can be — no
 * libcurl, no allocation per request, no header parsing beyond Content-Length.
 * A real sidecar deployment is slower than this. If in-process still wins, it
 * wins by at least this much.
 *
 * Both sides parse the response enough to prove it arrived and is the right
 * answer, because a benchmark that measures a failing call measures nothing.
 *
 * Usage: bench /path/to/libopenrate.so /path/to/snapshot.json http://127.0.0.1:PORT [iterations]
 */

#define _POSIX_C_SOURCE 200809L
/*
 * macOS hides its non-POSIX clock ids (CLOCK_MONOTONIC_RAW among them) when
 * _POSIX_C_SOURCE is set, and CLOCK_MONOTONIC there ticks in whole
 * microseconds. Without this the timer silently degrades to a 1000 ns
 * granularity and the percentiles below become artefacts of the clock — which
 * is what the first run of this benchmark actually reported.
 */
#if defined(__APPLE__)
#define _DARWIN_C_SOURCE
#endif

#include <arpa/inet.h>
#include <dlfcn.h>
#include <errno.h>
#include <netinet/in.h>
#include <netinet/tcp.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <strings.h>
#include <sys/socket.h>
#include <time.h>
#include <unistd.h>

#include "openrate.h"

#define DEFAULT_ITERATIONS 20000

/*
 * USD 100 -> ZAR at the snapshot's 18.44. The comparison below is a relative
 * one, not equality: 18.44 has no exact binary representation, so the product
 * is 1844.0000000000002 and an == against 1844.0 fails while printf("%f")
 * prints "1844.000000" for both. That is a good bug to have hit here and a bad
 * one to ship — it is exactly how a display convinces a reader that two
 * different numbers are the same.
 */
#define EXPECTED_RESULT 1844.0
#define TOLERANCE 1e-9

typedef uint64_t (*fn_new)(const char *, char **);
typedef char *(*fn_call)(uint64_t, const char *, const char *, char **);
typedef void (*fn_close)(uint64_t);
typedef void (*fn_free)(char *);

static void die(const char *what) {
  fprintf(stderr, "bench: %s: %s\n", what, strerror(errno));
  exit(2);
}

static void diem(const char *what) {
  fprintf(stderr, "bench: %s\n", what);
  exit(2);
}

/*
 * CLOCK_MONOTONIC_RAW where it exists, because macOS's CLOCK_MONOTONIC ticks in
 * whole microseconds. An in-process call here costs a few microseconds, so a
 * 1us timer quantises the measurement into a handful of buckets and the
 * percentiles become artefacts of the clock. The first version of this file had
 * exactly that: every sample was a multiple of 1000 ns. measure_granularity()
 * below prints what the clock actually resolves, so the reader can see whether
 * to believe the numbers instead of taking this comment's word for it.
 */
#ifdef CLOCK_MONOTONIC_RAW
#define BENCH_CLOCK CLOCK_MONOTONIC_RAW
#else
#define BENCH_CLOCK CLOCK_MONOTONIC
#endif

static double now_ns(void) {
  struct timespec ts;
  clock_gettime(BENCH_CLOCK, &ts);
  return (double)ts.tv_sec * 1e9 + (double)ts.tv_nsec;
}

/* The smallest non-zero interval this clock can report, over 10000 tries. */
static double measure_granularity(void) {
  double best = 1e9;
  for (int i = 0; i < 10000; i++) {
    double a = now_ns();
    double b = now_ns();
    double d = b - a;
    if (d > 0 && d < best) {
      best = d;
    }
  }
  return best;
}

static int cmp_double(const void *a, const void *b) {
  double x = *(const double *)a, y = *(const double *)b;
  return (x > y) - (x < y);
}

typedef struct {
  double mean, p50, p99, min;
} stats;

static stats summarize(double *samples, int n) {
  stats s = {0, 0, 0, 0};
  double total = 0;
  for (int i = 0; i < n; i++) {
    total += samples[i];
  }
  s.mean = total / n;
  qsort(samples, (size_t)n, sizeof(double), cmp_double);
  s.min = samples[0];
  s.p50 = samples[n / 2];
  s.p99 = samples[(int)((double)n * 0.99)];
  return s;
}

static void report(const char *label, stats s) {
  printf("  %-24s mean %9.0f ns   p50 %9.0f ns   p99 %9.0f ns   min %9.0f ns\n", label, s.mean,
         s.p50, s.p99, s.min);
}

/* find a number after a key, skipping matches whose value is not numeric */
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

static int close_enough(double got) {
  double diff = got - (double)EXPECTED_RESULT;
  if (diff < 0) {
    diff = -diff;
  }
  return diff <= TOLERANCE * (double)EXPECTED_RESULT;
}

static char *read_file(const char *path) {
  FILE *f = fopen(path, "rb");
  if (f == NULL) {
    die("open snapshot");
  }
  if (fseek(f, 0, SEEK_END) != 0) {
    die("seek snapshot");
  }
  long size = ftell(f);
  if (size < 0) {
    die("tell snapshot");
  }
  rewind(f);
  char *buf = malloc((size_t)size + 1);
  if (buf == NULL) {
    diem("out of memory reading the snapshot");
  }
  if (fread(buf, 1, (size_t)size, f) != (size_t)size) {
    diem("short read on the snapshot");
  }
  buf[size] = '\0';
  fclose(f);
  return buf;
}

/*
 * find_ci is a case-insensitive substring search. strcasestr would do, but it
 * is a GNU/BSD extension that _POSIX_C_SOURCE hides on macOS and that needs
 * _GNU_SOURCE on glibc — a portability fight not worth having for six lines.
 */
static const char *find_ci(const char *hay, const char *needle) {
  size_t n = strlen(needle);
  for (const char *p = hay; *p != '\0'; p++) {
    if (strncasecmp(p, needle, n) == 0) {
      return p;
    }
  }
  return NULL;
}

/* ------------------------------------------------------------------ */
/* A very small HTTP/1.1 client. No allocation per request.            */
/* ------------------------------------------------------------------ */

typedef struct {
  int fd;
  char buf[65536];
  size_t len; /* bytes currently buffered */
} http_conn;

/* parse "http://127.0.0.1:8080" into host and port */
static void parse_url(const char *url, char *host, size_t hostsz, int *port) {
  const char *p = strstr(url, "://");
  p = (p == NULL) ? url : p + 3;
  const char *colon = strrchr(p, ':');
  if (colon == NULL) {
    diem("the base URL must include a port");
  }
  size_t hlen = (size_t)(colon - p);
  if (hlen >= hostsz) {
    diem("host too long");
  }
  memcpy(host, p, hlen);
  host[hlen] = '\0';
  *port = atoi(colon + 1);
}

static void http_connect(http_conn *c, const char *host, int port) {
  c->fd = socket(AF_INET, SOCK_STREAM, 0);
  if (c->fd < 0) {
    die("socket");
  }
  int one = 1;
  setsockopt(c->fd, IPPROTO_TCP, TCP_NODELAY, &one, sizeof(one));

  struct sockaddr_in addr;
  memset(&addr, 0, sizeof(addr));
  addr.sin_family = AF_INET;
  addr.sin_port = htons((uint16_t)port);
  if (inet_pton(AF_INET, host, &addr.sin_addr) != 1) {
    diem("the loopback URL must be a numeric IPv4 address");
  }
  if (connect(c->fd, (struct sockaddr *)&addr, sizeof(addr)) != 0) {
    die("connect");
  }
  c->len = 0;
}

/*
 * One request/response round trip. Returns the body in *body_out (a pointer
 * into the connection buffer, valid until the next call).
 */
static void http_get(http_conn *c, const char *request, size_t reqlen, char **body_out) {
  size_t sent = 0;
  while (sent < reqlen) {
    ssize_t n = write(c->fd, request + sent, reqlen - sent);
    if (n <= 0) {
      die("write");
    }
    sent += (size_t)n;
  }

  c->len = 0;
  char *headers_end = NULL;
  long content_length = -1;

  for (;;) {
    if (c->len + 1 >= sizeof(c->buf)) {
      diem("response larger than the client's buffer");
    }
    ssize_t n = read(c->fd, c->buf + c->len, sizeof(c->buf) - c->len - 1);
    if (n <= 0) {
      die("read");
    }
    c->len += (size_t)n;
    c->buf[c->len] = '\0';

    if (headers_end == NULL) {
      headers_end = strstr(c->buf, "\r\n\r\n");
      if (headers_end != NULL) {
        const char *cl = find_ci(c->buf, "content-length:");
        if (cl == NULL || cl > headers_end) {
          diem("the server did not send a Content-Length; this client cannot chunk");
        }
        content_length = strtol(cl + strlen("content-length:"), NULL, 10);
        headers_end += 4;
      }
    }
    if (headers_end != NULL) {
      size_t have = c->len - (size_t)(headers_end - c->buf);
      if (have >= (size_t)content_length) {
        *body_out = headers_end;
        return;
      }
    }
  }
}

/* ------------------------------------------------------------------ */

int main(int argc, char **argv) {
  if (argc < 4 || argc > 5) {
    fprintf(stderr,
            "usage: %s LIBRARY SNAPSHOT.json http://127.0.0.1:PORT [iterations]\n",
            argv[0]);
    return 2;
  }
  const char *libpath = argv[1];
  const char *snappath = argv[2];
  const char *baseurl = argv[3];
  int iterations = (argc == 5) ? atoi(argv[4]) : DEFAULT_ITERATIONS;
  if (iterations < 100) {
    diem("iterations must be at least 100");
  }

  char *snapshot = read_file(snappath);

  /* --- in-process ---------------------------------------------------- */

  void *lib = dlopen(libpath, RTLD_NOW | RTLD_LOCAL);
  if (lib == NULL) {
    fprintf(stderr, "bench: dlopen: %s\n", dlerror());
    return 2;
  }
  fn_new f_new = (fn_new)dlsym(lib, "openrate_new");
  fn_call f_call = (fn_call)dlsym(lib, "openrate_call");
  fn_close f_close = (fn_close)dlsym(lib, "openrate_close");
  fn_free f_free = (fn_free)dlsym(lib, "openrate_free");
  if (!f_new || !f_call || !f_close || !f_free) {
    diem("the library does not export the ABI");
  }

  char *err = NULL;
  uint64_t h = f_new("{\"base\":\"ZAR\",\"quiet\":true}", &err);
  if (h == 0) {
    fprintf(stderr, "bench: openrate_new: %s\n", err ? err : "(no message)");
    return 2;
  }
  char *out = f_call(h, "load", snapshot, &err);
  if (out == NULL) {
    fprintf(stderr, "bench: load: %s\n", err ? err : "(no message)");
    return 2;
  }
  f_free(out);

  const char *convert_req = "{\"from\":\"USD\",\"to\":\"ZAR\",\"amount\":100}";

  double *ffi_samples = malloc(sizeof(double) * (size_t)iterations);
  if (ffi_samples == NULL) {
    diem("out of memory");
  }

  /* Warm up: first-call costs (lazy allocation, page faults, branch
   * prediction) belong to neither transport's steady state. */
  for (int i = 0; i < 1000; i++) {
    out = f_call(h, "convert", convert_req, &err);
    if (out == NULL) {
      fprintf(stderr, "bench: convert: %s\n", err ? err : "(no message)");
      return 2;
    }
    f_free(out);
  }

  double check = 0;
  for (int i = 0; i < iterations; i++) {
    double t0 = now_ns();
    out = f_call(h, "convert", convert_req, &err);
    double t1 = now_ns();
    if (out == NULL) {
      diem("a conversion failed mid-benchmark");
    }
    if (i == 0 && (!number_after(out, "\"result\":", &check) || !close_enough(check))) {
      fprintf(stderr,
              "bench: in-process answered %.17g, expected %.17g — this benchmark is timing "
              "the wrong thing\n",
              check, (double)EXPECTED_RESULT);
      return 2;
    }
    f_free(out);
    ffi_samples[i] = t1 - t0;
  }
  stats ffi = summarize(ffi_samples, iterations);

  /* --- loopback HTTP -------------------------------------------------- */

  char host[64];
  int port = 0;
  parse_url(baseurl, host, sizeof(host), &port);

  http_conn conn;
  http_connect(&conn, host, port);

  char request[512];
  int reqlen = snprintf(request, sizeof(request),
                        "GET /api/v1/convert?from=USD&to=ZAR&amount=100 HTTP/1.1\r\n"
                        "Host: %s:%d\r\n"
                        "Connection: keep-alive\r\n"
                        "Accept: application/json\r\n"
                        "\r\n",
                        host, port);
  if (reqlen <= 0) {
    diem("could not build the request");
  }

  char *body = NULL;
  for (int i = 0; i < 1000; i++) {
    http_get(&conn, request, (size_t)reqlen, &body);
  }

  double *http_samples = malloc(sizeof(double) * (size_t)iterations);
  if (http_samples == NULL) {
    diem("out of memory");
  }
  for (int i = 0; i < iterations; i++) {
    double t0 = now_ns();
    http_get(&conn, request, (size_t)reqlen, &body);
    double t1 = now_ns();
    if (i == 0) {
      if (!number_after(body, "\"result\":", &check) || !close_enough(check)) {
        fprintf(stderr,
                "bench: loopback answered %.17g, expected %.17g — the two sides are not "
                "answering the same question\n",
                check, (double)EXPECTED_RESULT);
        return 2;
      }
    }
    http_samples[i] = t1 - t0;
  }
  stats hs = summarize(http_samples, iterations);

  close(conn.fd);
  f_close(h);

  /* --- report ---------------------------------------------------------- */

  double granularity = measure_granularity();

  printf("\nopenrate: one conversion, %d iterations each\n", iterations);
  printf("  timer granularity on this machine: %.0f ns\n", granularity);
  printf("  both sides answered USD 100 -> ZAR %.0f from the same snapshot\n\n",
         EXPECTED_RESULT);
  report("in-process (C ABI)", ffi);
  report("loopback HTTP", hs);
  printf("\n  in-process is %.1fx faster at the mean, %.1fx at p50, %.1fx at p99\n",
         hs.mean / ffi.mean, hs.p50 / ffi.p50, hs.p99 / ffi.p99);
  printf("  absolute saving per call: %.1f us at the mean\n\n",
         (hs.mean - ffi.mean) / 1000.0);

  free(ffi_samples);
  free(http_samples);
  free(snapshot);
  return 0;
}
