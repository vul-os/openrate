/*
 * direct_convert.c — openrate running IN THIS PROCESS, through the C ABI.
 *
 *   make direct_convert && ./direct_convert
 *   ./direct_convert --fetch          # also builds a refresher (uses the network)
 *
 * C is where this ABI is defined, so this file is the reference reading of it:
 * every other binding in sdks/ is doing what happens here, with more ceremony.
 * It links against libopenrate at build time and #includes the stable header,
 * which is how a program with an installed library is actually written.
 *
 * NOT A TEST. ffi/test/smoke.c is the test — it dlopen()s the library, resolves
 * every symbol by name, takes an OS-level socket census to check that an engine
 * opens nothing, and asserts 40 named checks including how many checks ran.
 * That catches a missing //export or a drifted header; this shows someone how
 * to call the thing. If you are changing the ABI, that file is the one that
 * must fail.
 *
 * THE SHAPE OF THIS FILE IS THE ARGUMENT. Everything up to --fetch is a
 * complete, useful conversion program that opens no socket, starts no thread
 * and reads no environment variable. Fetching is below a flag because in
 * openrate it is below a second, explicit construction: an ENGINE handle
 * refuses the "refresh" method, and only a REFRESHER handle can fetch. There
 * is no other code path.
 *
 * SPDX-License-Identifier: MIT OR Apache-2.0
 */

#define _POSIX_C_SOURCE 200809L

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "jsonpeek.h"
#include "openrate.h"

/* Rates you obtained yourself — a fixings file, a bank statement, your own
 * treasury desk. "load" is the zero-network path and has no HTTP counterpart,
 * because the server is read-only. */
static const char *BOOK =
    "{\"built_at\":\"2026-08-09T00:00:00Z\",\"edges\":["
    "{\"from\":\"USD\",\"to\":\"ZAR\",\"rate\":18.5,\"source\":\"my-desk\"},"
    "{\"from\":\"EUR\",\"to\":\"USD\",\"rate\":1.09,\"source\":\"my-desk\"},"
    "{\"from\":\"GBP\",\"to\":\"USD\",\"rate\":1.27,\"source\":\"my-desk\"}]}";

/* Print the path array of a rate view: ["USD","EUR","ZAR"] -> USD -> EUR -> ZAR.
 * jsonpeek deliberately does not understand arrays, so this walks the quoted
 * strings between the brackets by hand. See jsonpeek.h on why there is no
 * parser here. */
static void print_path(const char *doc) {
	const char *at = strstr(doc, "\"path\":[");
	if (!at) {
		printf("(no path)");
		return;
	}
	at += strlen("\"path\":[");
	int first = 1;
	while (*at && *at != ']') {
		if (*at == '"') {
			at++;
			if (!first) printf(" -> ");
			first = 0;
			while (*at && *at != '"') putchar(*at++);
		}
		if (*at) at++;
	}
}

int main(int argc, char **argv) {
	const int want_fetch = (argc > 1) && strcmp(argv[1], "--fetch") == 0;
	char *err = NULL;
	char *out = NULL;
	uint64_t engine = 0;
	uint64_t refresher = 0;
	int status = 1;

	/*
	 * 1. Probe the version before anything else. The header's
	 *    OPENRATE_ABI_VERSION is compiled into this program; the library
	 *    reports what it was built from. A mismatch means a stale libopenrate
	 *    is earlier on your load path — which otherwise misbehaves in ways that
	 *    look like openrate bugs. The returned string is static: do NOT free it.
	 */
	printf("abi version:  %s (this program was built against %s)\n", openrate_abi_version(),
	       OPENRATE_ABI_VERSION);
	if (strcmp(openrate_abi_version(), OPENRATE_ABI_VERSION) != 0) {
		fprintf(stderr, "WARNING: a stale libopenrate is on the load path\n");
	}
	printf("open handles: %llu\n", (unsigned long long)openrate_open_handles());

	/*
	 * 2. Create the ENGINE. This starts no thread, opens no socket, reads no
	 *    environment variable and sends no packet.
	 */
	engine = openrate_new("{\"base\":\"ZAR\",\"quiet\":true}", &err);
	if (engine == 0) {
		fprintf(stderr, "openrate_new: %s\n", err ? err : "(no message)");
		openrate_free(err);
		return 1;
	}
	printf("engine:       handle %llu\n\n", (unsigned long long)engine);

	/*
	 * From here every exit goes through `done:`. There is no RAII in C, so the
	 * discipline is one cleanup label and no early returns — that is what keeps
	 * the handles and the malloc'd strings from leaking on an error path.
	 */

	/* --- an engine with no rates says so, rather than guessing ------------ */
	out = openrate_call(engine, "convert", "{\"from\":\"USD\",\"to\":\"ZAR\",\"amount\":100}", &err);
	if (out) {
		fprintf(stderr, "an empty engine answered a conversion\n");
		goto done;
	}
	printf("empty engine  %s\n", err ? err : "(no message)");
	openrate_free(err);
	err = NULL;

	/* --- feed it ----------------------------------------------------------- */
	out = openrate_call(engine, "load", BOOK, &err);
	if (!out) {
		fprintf(stderr, "load: %s\n", err ? err : "(no message)");
		goto done;
	}
	printf("load          %s\n", out);
	openrate_free(out);
	out = NULL;

	/* --- convert ------------------------------------------------------------ */
	out = openrate_call(engine, "convert", "{\"from\":\"USD\",\"to\":\"ZAR\",\"amount\":100}", &err);
	if (!out) {
		fprintf(stderr, "convert: %s\n", err ? err : "(no message)");
		goto done;
	}
	{
		double amount = 0, result = 0, rate = 0, hops = 0;
		char from[8] = "", to[8] = "";
		json_number(out, "amount", &amount);
		json_number(out, "result", &result);
		json_number(out, "hops", &hops);
		/* "rate" appears twice: once as the rate VIEW object and, inside it,
		 * once as the number. Scanning from the object's opening brace picks
		 * the inner one. A real JSON parser would not need the care — see
		 * jsonpeek.h, which is explicit about not being one. */
		const char *view = strstr(out, "\"rate\":{");
		if (view) json_number(view + strlen("\"rate\":"), "rate", &rate);
		json_string(out, "from", from, sizeof(from));
		json_string(out, "to", to, sizeof(to));
		printf("convert       %.0f %s = %.2f %s\n", amount, from, result, to);
		printf("              rate %.4f, %.0f hop(s), path ", rate, hops);
		print_path(out);
		printf("\n");
	}
	openrate_free(out);
	out = NULL;

	/* A pair nobody quoted directly is still answered, through the graph, and
	 * the path says so. That auditability is the product. */
	out = openrate_call(engine, "convert", "{\"from\":\"GBP\",\"to\":\"EUR\",\"amount\":1}", &err);
	if (!out) {
		fprintf(stderr, "cross: %s\n", err ? err : "(no message)");
		goto done;
	}
	{
		double result = 0;
		json_number(out, "result", &result);
		printf("cross         1 GBP = %.4f EUR via ", result);
		print_path(out);
		printf("\n");
	}
	openrate_free(out);
	out = NULL;

	/* --- metadata ------------------------------------------------------------ */
	out = openrate_call(engine, "meta", NULL, &err);
	if (!out) {
		fprintf(stderr, "meta: %s\n", err ? err : "(no message)");
		goto done;
	}
	printf("meta          %s\n", out);
	printf("              ^ \"sources\":[] — nothing is refreshing this engine\n");
	openrate_free(out);
	out = NULL;

	/* --- the error path ------------------------------------------------------ */
	/* The message is plain UTF-8 text, NOT JSON. Print it; do not parse it. And
	 * free it: an error string is allocated exactly like a result. */
	out = openrate_call(engine, "convert", "{\"from\":\"USD\",\"to\":\"XXX\"}", &err);
	if (out) {
		fprintf(stderr, "an unknown pair returned a result\n");
		goto done;
	}
	printf("error         %s\n", err ? err : "(no message)");
	openrate_free(err);
	err = NULL;

	/* --- the split, enforced at the ABI --------------------------------------- */
	out = openrate_call(engine, "refresh", "{}", &err);
	if (out) {
		fprintf(stderr, "an ENGINE handle performed a refresh\n");
		goto done;
	}
	printf("no fetching   %s\n", err ? err : "(no message)");
	openrate_free(err);
	err = NULL;

	/* --- an invented handle is an error, not a segfault ------------------------ */
	out = openrate_call(999999, "meta", NULL, &err);
	printf("bad handle    %s\n", err ? err : "(no message)");
	openrate_free(err);
	err = NULL;

	if (want_fetch) {
		/* --- the other handle kind ------------------------------------------
		 * A REFRESHER is a separate construction with its own handle and its
		 * own lifetime. Building it STILL opens nothing; "refresh" is what
		 * fetches. */
		printf("\n--fetch: building a refresher (this one does use the network)\n");
		refresher = openrate_refresher_new(engine, "{\"sources\":\"ecb\",\"quiet\":true}", &err);
		if (refresher == 0) {
			fprintf(stderr, "openrate_refresher_new: %s\n", err ? err : "(no message)");
			goto done;
		}
		printf("refresher:    handle %llu, open handles %llu\n",
		       (unsigned long long)refresher, (unsigned long long)openrate_open_handles());

		out = openrate_call(refresher, "status", NULL, &err);
		printf("before        %s\n", out ? out : (err ? err : "(no message)"));
		openrate_free(out);
		out = NULL;
		openrate_free(err);
		err = NULL;

		out = openrate_call(refresher, "refresh", "{\"timeout_ms\":20000}", &err);
		if (!out) {
			printf("refresh       failed: %s\n", err ? err : "(no message)");
			openrate_free(err);
			err = NULL;
		} else {
			printf("refresh       %s\n", out);
			openrate_free(out);
			out = NULL;

			out = openrate_call(engine, "convert",
			                    "{\"from\":\"EUR\",\"to\":\"ZAR\",\"amount\":100}", &err);
			if (out) {
				double result = 0;
				json_number(out, "result", &result);
				printf("live          100 EUR = %.2f ZAR via ", result);
				print_path(out);
				printf("\n");
			}
			openrate_free(out);
			out = NULL;
		}
	}

	status = 0;

done:
	openrate_free(out); /* openrate_free(NULL) is safe, so no branch here */
	openrate_free(err);

	/* Closing the ENGINE also stops and releases every refresher built over it,
	 * so closing in the "wrong" order cannot leak a running loop. Closing the
	 * refresher afterwards must therefore be a harmless no-op, not a double
	 * free — and openrate_open_handles() is how you check rather than assume. */
	openrate_close(engine);
	openrate_close(refresher);
	openrate_close(engine); /* idempotent */
	printf("\nafter close   open handles %llu\n",
	       (unsigned long long)openrate_open_handles());

	out = openrate_call(engine, "meta", NULL, &err);
	printf("use-after-close  %s\n", err ? err : "(the closed handle answered!)");
	openrate_free(out);
	openrate_free(err);
	return status;
}
