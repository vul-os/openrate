/*
 * sidecar_convert.c — openrate as a child process, spoken to over loopback HTTP.
 *
 *   make sidecar_convert && ./sidecar_convert
 *   ./sidecar_convert http://openrate.example    # use a server you already run
 *
 * Nobody runs a server: this program picks a free port, spawns `openrate` with
 * OPENRATE_ADDR pointing at it, waits for /healthz, uses it, and kills it.
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
 * "Healthy" and "useful" are different questions: openrate answers 200 from the
 * moment it is listening, with an empty book until the first fetch lands. So
 * poll for a currency rather than assuming one.
 */
static int wait_for_rates(int port, double seconds) {
	char errbuf[256];
	for (int i = 0; i < (int)(seconds * 4); i++) {
		http_response r;
		if (http_request(port, "GET", "/api/v1/meta", NULL, NULL, &r, errbuf, sizeof(errbuf)) == 0) {
			int have = strstr(r.body, "\"currencies\": []") == NULL &&
			           strstr(r.body, "\"currencies\":[]") == NULL;
			http_response_free(&r);
			if (have) return 0;
		}
		millisleep(250);
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

	if (wait_for_rates(port, 30.0) != 0) {
		printf("\nno rates arrived within 30s — the server started fine but could not\n");
		printf("reach its sources. Everything below needs a rate to exist.\n");
		printf("For a mode that needs no network at all, see direct_convert.c.\n");
		goto done;
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
