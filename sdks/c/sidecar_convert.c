/*
 * sidecar_convert.c — openrate as a child process, spoken to over loopback HTTP.
 *
 *   make sidecar_convert && ./sidecar_convert
 *   ./sidecar_convert http://openrate.example    # use a server you already run
 *
 * Nobody runs a server: this program picks a free port, spawns `openrate` with
 * OPENRATE_ADDR pointing at it, waits for /healthz (the listener) and then for
 * /readyz (the first rate), uses it, and kills it.
 *
 * UNLIKE direct_convert.c, THIS FETCHES. A server refreshes on startup and on
 * its interval; that is what a server is for. If you want a process that
 * provably sends no packets, that is direct_convert.c — an engine with no
 * refresher.
 *
 * WHEN TO PREFER THIS OVER direct_convert.c, from C specifically:
 *   - Your process forks. The Go runtime inside libopenrate does not survive
 *     fork() without exec(). This program forks, and is safe doing so ONLY
 *     because it never loads the library. Do not merge the two examples.
 *   - Your process has its own SIGSEGV/SIGBUS/SIGPROF handling — a crash
 *     reporter, a sampling profiler, a sanitizer build.
 *   - Several processes should share one refreshing rate book. Four workers
 *     each fetching their own copy is worse in every dimension.
 *   - You are not on darwin/arm64: see README.md, where the platform table is
 *     narrower than you probably expect.
 *
 * SPDX-License-Identifier: MIT OR Apache-2.0
 */

#define _POSIX_C_SOURCE 200809L

#include <errno.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/types.h>
#include <sys/wait.h>
#include <time.h>
#include <unistd.h>

#include "jsonpeek.h"
#include "mini_http.h"

static const char *binary(void) {
	const char *b = getenv("OPENRATE_BINARY");
	return (b && *b) ? b : "openrate"; /* resolved on PATH by execvp */
}

/* ------------------------------------------------------------------ */

struct sidecar {
	pid_t pid;
	int port;
};

static void millisleep(long ms) {
	struct timespec ts = {ms / 1000, (ms % 1000) * 1000000L};
	nanosleep(&ts, NULL);
}

static int wait_healthy(int port, double seconds) {
	char errbuf[256];
	for (int i = 0; i < (int)(seconds * 20); i++) {
		http_response r;
		if (http_request(port, "GET", "/healthz", NULL, NULL, &r, errbuf, sizeof(errbuf)) == 0) {
			int ok = r.status == 200;
			http_response_free(&r);
			if (ok) return 0;
		}
		millisleep(50);
	}
	return -1;
}

static int sidecar_start(struct sidecar *s) {
	s->pid = -1;
	s->port = http_free_port();
	if (s->port == 0) {
		fprintf(stderr, "could not find a free loopback port\n");
		return -1;
	}

	char addr[64];
	snprintf(addr, sizeof(addr), "OPENRATE_ADDR=127.0.0.1:%d", s->port);
	/* One fast source keeps the example quick; openrate's default is four. */
	char sources[64];
	snprintf(sources, sizeof(sources), "OPENRATE_SOURCES=%s",
	         getenv("OPENRATE_SOURCES") ? getenv("OPENRATE_SOURCES") : "ecb");
	/* The binary limits /api/ to 120 requests a minute per IP by default. That
	 * is anti-scraping for a public deployment, and this child listens on
	 * loopback and serves exactly one client — this process. There is no
	 * stranger here to throttle, while a legitimate batch of conversions would
	 * sail past 120/min and take a 429 from our own sidecar. Set
	 * OPENRATE_RATELIMIT yourself to put it back. */
	char ratelimit[] = "OPENRATE_RATELIMIT=0";

	pid_t pid = fork();
	if (pid < 0) {
		perror("fork");
		return -1;
	}
	if (pid == 0) {
		/* Child. The environment is inherited, so API keys for the optional
		 * sources pass through exactly as for the standalone binary. */
		putenv(addr);
		putenv(sources);
		if (!getenv("OPENRATE_RATELIMIT")) putenv(ratelimit);
		execvp(binary(), (char *const[]){(char *)binary(), NULL});
		fprintf(stderr, "exec %s: %s\n", binary(), strerror(errno));
		_exit(127);
	}
	s->pid = pid;

	if (wait_healthy(s->port, 15.0) != 0) {
		fprintf(stderr, "openrate did not become healthy on 127.0.0.1:%d\n", s->port);
		return -1;
	}
	return 0;
}

/* Idempotent, and called on every exit path including the error paths. */
static void sidecar_stop(struct sidecar *s) {
	if (!s || s->pid <= 0) return;
	kill(s->pid, SIGTERM);
	for (int i = 0; i < 100; i++) { /* up to 5s for a clean shutdown */
		if (waitpid(s->pid, NULL, WNOHANG) == s->pid) {
			s->pid = -1;
			return;
		}
		millisleep(50);
	}
	kill(s->pid, SIGKILL);
	waitpid(s->pid, NULL, 0);
	s->pid = -1;
}

/*
 * Turn a /readyz 503 body into the one line a caller can act on: the server's
 * own `reason`, then `name: last_error` for every source that has one.
 *
 * `last_error` is `omitempty`, so a source that has simply not been tried yet
 * has no such key — printing "ecb: " for it would be worse than printing
 * nothing, so a body with no errors in it degrades to the reason alone.
 *
 * The sources array is walked object by object rather than through jsonpeek's
 * whole-document scan, because "name" and "last_error" have to be read as a
 * PAIR: a document-wide search for "last_error" finds one source's error and a
 * document-wide search for "name" finds a different source's name. No field of
 * a source is itself an object or an array, so bounding each object at the next
 * `}` is enough, and the array itself ends at the first `]`.
 */
static void describe_not_ready(const char *body, char *out, size_t outcap) {
	if (!outcap) return;
	char reason[512] = "";
	if (!json_string(body, "reason", reason, sizeof(reason)) || !reason[0]) {
		snprintf(reason, sizeof(reason), "not ready, and it did not say why");
	}
	int n = snprintf(out, outcap, "%s", reason);
	if (n < 0 || (size_t)n >= outcap) return;

	const char *arr = strstr(body, "\"sources\"");
	const char *first = arr ? strchr(arr, '[') : NULL;
	const char *end = first ? strchr(first, ']') : NULL;
	int printed = 0;
	for (const char *p = first; p && end && p < end;) {
		const char *open = strchr(p, '{');
		if (!open || open >= end) break;
		const char *close = strchr(open, '}');
		if (!close || close > end) break;

		char object[1024];
		size_t len = (size_t)(close - open) + 1;
		if (len >= sizeof(object)) len = sizeof(object) - 1;
		memcpy(object, open, len);
		object[len] = '\0';

		char name[64] = "", err[512] = "";
		json_string(object, "name", name, sizeof(name));
		if (json_string(object, "last_error", err, sizeof(err)) && err[0]) {
			int add = snprintf(out + n, outcap - (size_t)n, "%s%s: %s", printed ? "; " : " (",
			                   name[0] ? name : "?", err);
			if (add < 0 || (size_t)(n + add) >= outcap) return; /* truncated; stop cleanly */
			n += add;
			printed = 1;
		}
		p = close + 1;
	}
	if (printed) snprintf(out + n, outcap - (size_t)n, ")");
}

/*
 * "Live" and "useful" are different questions. /healthz answers 200 from the
 * moment the listener binds, with an empty book behind it; /readyz answers 200
 * only once the snapshot has a currency in it, and 503 with a JSON body saying
 * why not. Waiting on the wrong one is how this example used to convert against
 * nothing and report every pair as an unknown currency.
 *
 * Neither endpoint is under /api/, and the rate limiter only guards /api/, so a
 * fixed short interval is free — no backoff to dodge a limiter this poll never
 * meets. (The earlier workaround polled /api/v1/meta, which did meet it.)
 *
 * On failure `why` holds the most useful explanation seen: the last 503's, if
 * there ever was one, otherwise the transport error. A server that told us
 * "ecb: connection refused" explains more than a socket that later stopped
 * answering, so a transport error never displaces a reason the server gave.
 */
static int wait_ready(int port, double seconds, char *why, size_t whycap) {
	char errbuf[256];
	const long interval_ms = 150;
	const int attempts = (int)(seconds * 1000 / (double)interval_ms);
	int explained = 0;
	snprintf(why, whycap, "openrate never answered /readyz");

	for (int i = 0; i < attempts; i++) {
		http_response r;
		if (http_request(port, "GET", "/readyz", NULL, NULL, &r, errbuf, sizeof(errbuf)) == 0) {
			if (r.status == 200) {
				http_response_free(&r);
				return 0;
			}
			/* A 503 carries its reasons in the BODY. mini_http hands back the
			 * body whatever the status, which is the only reason this poll can
			 * be specific rather than printing a bare timeout. */
			describe_not_ready(r.body, why, whycap);
			explained = 1;
			http_response_free(&r);
		} else if (!explained) {
			snprintf(why, whycap, "%s", errbuf);
		}
		millisleep(interval_ms);
	}
	return -1;
}

/* ------------------------------------------------------------------ */

int main(int argc, char **argv) {
	struct sidecar sc = {-1, 0};
	char errbuf[256];
	http_response r;
	int status = 1;
	int port;
	const int managed = (argc <= 1);

	if (managed) {
		if (sidecar_start(&sc) != 0) {
			sidecar_stop(&sc);
			return 1;
		}
		port = sc.port;
		printf("base url:     http://127.0.0.1:%d  (managed, pid %d)\n", port, (int)sc.pid);
	} else {
		/* A URL you already run. Only the port is parsed: this example's HTTP
		 * client speaks to 127.0.0.1 and nothing else, on purpose. */
		const char *colon = strrchr(argv[1], ':');
		port = colon ? atoi(colon + 1) : 80;
		printf("base url:     %s  (not managed — spawning nothing)\n", argv[1]);
	}

	{
		char why[1024] = "";
		if (wait_ready(port, 30.0, why, sizeof(why)) != 0) {
			fprintf(stderr, "openrate has no rates after 30s: %s\n", why);
			printf("\nThe server is listening but its book is empty, and everything below\n");
			printf("needs a rate to exist. For a mode that needs no network at all, see\n");
			printf("direct_convert.c.\n");
			goto done;
		}
	}

	/* --- meta -------------------------------------------------------------- */
	if (http_request(port, "GET", "/api/v1/meta", NULL, NULL, &r, errbuf, sizeof(errbuf)) != 0) {
		fprintf(stderr, "GET /api/v1/meta: %s\n", errbuf);
		goto done;
	}
	{
		char base[8] = "";
		char source[32] = "";
		double edges = 0;
		json_string(r.body, "default_base", base, sizeof(base));
		json_string(r.body, "name", source, sizeof(source));
		json_number(r.body, "edges", &edges);
		printf("meta          HTTP %d, default base %s, first source %s with %.0f edges\n",
		       r.status, base, source[0] ? source : "(none)", edges);
	}
	http_response_free(&r);

	/* --- convert ------------------------------------------------------------ */
	if (http_request(port, "GET", "/api/v1/convert?from=USD&to=ZAR&amount=100", NULL, NULL, &r,
	                 errbuf, sizeof(errbuf)) != 0) {
		fprintf(stderr, "GET /api/v1/convert: %s\n", errbuf);
		goto done;
	}
	{
		double amount = 0, result = 0, rate = 0, hops = 0, age = 0;
		char from[8] = "", to[8] = "", grade[4] = "";
		json_number(r.body, "amount", &amount);
		json_number(r.body, "result", &result);
		json_number(r.body, "hops", &hops);
		json_number(r.body, "age_sec", &age);
		json_string(r.body, "grade", grade, sizeof(grade));
		/* "from", "to" and "rate" each occur again inside the rate view's legs,
		 * and picking the right one takes knowing that Go marshals object keys
		 * in sorted order: the document runs amount, from, rate{...legs...},
		 * result, to. So the top-level "from" is the FIRST occurrence and the
		 * top-level "to" is the LAST, and the rate number is inside the "rate"
		 * object rather than being it.
		 *
		 * If that reasoning makes you uneasy, good — it is exactly the reasoning
		 * a JSON parser exists to delete, and jsonpeek.h says plainly that it is
		 * not one. Link cJSON in a real program and none of this paragraph
		 * survives. */
		json_string(r.body, "from", from, sizeof(from));
		json_string_last(r.body, "to", to, sizeof(to));
		const char *view = strstr(r.body, "\"rate\": {");
		if (!view) view = strstr(r.body, "\"rate\":{");
		if (view) json_number(view + strlen("\"rate\":"), "rate", &rate);
		printf("convert       HTTP %d, %.0f %s = %.2f %s\n", r.status, amount, from, result, to);
		printf("              rate %.4f, %.0f hop(s), quality %s, %.0fs old\n", rate, hops, grade,
		       age);
	}
	http_response_free(&r);

	/* --- the whole book ------------------------------------------------------ */
	if (http_request(port, "GET", "/api/v1/rates?base=ZAR", NULL, NULL, &r, errbuf,
	                 sizeof(errbuf)) != 0) {
		fprintf(stderr, "GET /api/v1/rates: %s\n", errbuf);
		goto done;
	}
	{
		/* "hops" occurs exactly once per currency; "rate" also occurs inside
		 * every leg and every quote, so counting that would over-report. */
		printf("rates         HTTP %d, %d currencies against ZAR\n", r.status,
		       json_count(r.body, "hops"));
	}
	http_response_free(&r);

	/* --- the error path -------------------------------------------------------
	 * Same wire contract, different shape of failure: an HTTP status and a JSON
	 * error body, rather than a char** err carrying plain text. */
	if (http_request(port, "GET", "/api/v1/convert?from=USD&to=XXX", NULL, NULL, &r, errbuf,
	                 sizeof(errbuf)) != 0) {
		fprintf(stderr, "GET /api/v1/convert: %s\n", errbuf);
		goto done;
	}
	{
		char message[256] = "";
		json_string(r.body, "error", message, sizeof(message));
		printf("error         HTTP %d: %s\n", r.status, message);
	}
	http_response_free(&r);

	/* An unknown BASE, though, answers 200 with an empty book — where direct
	 * mode raises. That is the one deliberate difference between the modes. */
	if (http_request(port, "GET", "/api/v1/rates?base=XXX", NULL, NULL, &r, errbuf,
	                 sizeof(errbuf)) == 0) {
		printf("unknown base  HTTP %d with an empty book (direct mode errors instead —\n",
		       r.status);
		printf("              the one deliberate difference between the two modes)\n");
		http_response_free(&r);
	}

	status = 0;

done:
	sidecar_stop(&sc);
	printf("\n%s\n", managed ? "sidecar stopped" : "left the remote server alone");
	return status;
}
