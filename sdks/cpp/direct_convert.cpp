// direct_convert.cpp — openrate in this process, through openrate.hpp.
//
//   make direct_convert && ./direct_convert
//   ./direct_convert --fetch          # also builds a refresher (uses the network)
//
// Compare with ../c/direct_convert.c: the same calls in the same order producing
// the same output, with the C version's single `goto done` cleanup label
// replaced by destructors, and the engine/refresher split promoted from a
// runtime error into two C++ types.
//
// EVERYTHING BEFORE --fetch OPENS NO SOCKET. That is not a claim about how the
// example is written; an openrate::Engine has no method that can fetch, and the
// ABI underneath refuses one.
//
// SPDX-License-Identifier: MIT OR Apache-2.0

#include <cstdlib>
#include <cstring>
#include <iostream>
#include <string>
#include <string_view>

#include "openrate.hpp"

namespace {

// Rates you obtained yourself — a fixings file, a bank statement, your own
// treasury desk. The zero-network path.
constexpr const char *BOOK = R"({"built_at":"2026-08-09T00:00:00Z","edges":[
  {"from":"USD","to":"ZAR","rate":18.5,"source":"my-desk"},
  {"from":"EUR","to":"USD","rate":1.09,"source":"my-desk"},
  {"from":"GBP","to":"USD","rate":1.27,"source":"my-desk"}]})";

// Print a field for a human. NOT a JSON parser — see ../c/jsonpeek.h for the
// same disclaimer at length. A real program links nlohmann/json or simdjson.
std::string peek(std::string_view doc, std::string_view key, bool last = false) {
	const std::string needle = "\"" + std::string(key) + "\":";
	std::string out;
	size_t found = std::string_view::npos;
	for (size_t at = doc.find(needle); at != std::string_view::npos;
	     at = doc.find(needle, at + needle.size())) {
		found = at;
		if (!last) break;
	}
	if (found == std::string_view::npos) return out;
	size_t i = found + needle.size();
	while (i < doc.size() && doc[i] == ' ') i++;
	const bool quoted = i < doc.size() && doc[i] == '"';
	if (quoted) i++;
	for (; i < doc.size(); i++) {
		if (quoted && doc[i] == '"') break;
		if (!quoted && (doc[i] == ',' || doc[i] == '}')) break;
		if (quoted && doc[i] == '\\' && i + 1 < doc.size()) i++;
		out.push_back(doc[i]);
	}
	return out;
}

// The rate number lives inside the "rate" object, whose key is also "rate".
std::string peek_rate(std::string_view doc) {
	const size_t at = doc.find("\"rate\":{");
	if (at == std::string_view::npos) return {};
	return peek(doc.substr(at + std::strlen("\"rate\":")), "rate");
}

std::string peek_path(std::string_view doc) {
	const size_t at = doc.find("\"path\":[");
	if (at == std::string_view::npos) return "(no path)";
	std::string out;
	bool first = true;
	for (size_t i = at + std::strlen("\"path\":["); i < doc.size() && doc[i] != ']'; i++) {
		if (doc[i] != '"') continue;
		if (!first) out += " -> ";
		first = false;
		for (i++; i < doc.size() && doc[i] != '"'; i++) out.push_back(doc[i]);
	}
	return out;
}

}  // namespace

int main(int argc, char **argv) {
	const bool want_fetch = argc > 1 && std::strcmp(argv[1], "--fetch") == 0;

	// 1. Probe the version. OPENRATE_ABI_VERSION was compiled in from the
	//    header; abi_version() is what the loaded library reports.
	std::cout << "abi version:  " << openrate::abi_version() << " (built against "
	          << OPENRATE_ABI_VERSION << ")\n";
	if (!openrate::abi_version_matches())
		std::cerr << "WARNING: a stale libopenrate is on the load path\n";
	std::cout << "open handles: " << openrate::open_handles() << "\n";

	try {
		// 2. The engine owns its handle for the rest of this scope, including
		//    every path out of it. It also cannot fetch: there is no method on
		//    this type that does, and the ABI refuses one.
		openrate::Engine engine(R"({"base":"ZAR","quiet":true})");
		std::cout << "engine:       handle " << engine.handle() << "\n\n";

		// --- an engine with no rates says so, rather than guessing ----------
		{
			const openrate::StringResult r = engine.try_convert("USD", "ZAR", 100);
			std::cout << "empty engine  " << (r.ok() ? "IT ANSWERED?!" : r.error()) << "\n";
		}

		// --- feed it ---------------------------------------------------------
		std::cout << "load          " << engine.load(BOOK) << "\n";

		// --- convert -----------------------------------------------------------
		{
			const std::string out = engine.convert("USD", "ZAR", 100);
			std::cout << "convert       " << peek(out, "amount") << " " << peek(out, "from") << " = "
			          << peek(out, "result") << " " << peek(out, "to", /*last=*/true) << "\n";
			std::cout << "              rate " << peek_rate(out) << ", " << peek(out, "hops")
			          << " hop(s), path " << peek_path(out) << "\n";
		}

		// A pair nobody quoted directly is still answered, through the graph,
		// and the path says so. That auditability is the product.
		{
			const std::string out = engine.convert("GBP", "EUR", 1);
			std::cout << "cross         1 GBP = " << peek(out, "result") << " EUR via "
			          << peek_path(out) << "\n";
		}

		std::cout << "meta          " << engine.meta() << "\n";
		std::cout << "              ^ \"sources\":[] — nothing is refreshing this engine\n";

		// --- the error path -----------------------------------------------------
		// The library allocated that message; OwnedString freed it before this
		// exception was constructed, so a throw is not a leak.
		try {
			(void)engine.convert("USD", "XXX", 1);
			std::cout << "error         an unknown pair returned a result\n";
		} catch (const openrate::Error &e) {
			std::cout << "error         " << e.what() << "\n";
		}

		// --- the split, enforced at the ABI as well as in the type system -------
		// There is no engine.refresh() to call — that is the compiler's half.
		// This is the library's half, reached only by going around the wrapper.
		{
			const openrate::StringResult r = engine.try_call("refresh", "{}");
			std::cout << "no fetching   " << (r.ok() ? "AN ENGINE FETCHED?!" : r.error()) << "\n";
		}

		if (want_fetch) {
			// --- the other handle kind ------------------------------------------
			std::cout << "\n--fetch: building a refresher (this one does use the network)\n";
			openrate::Refresher refresher(engine, R"({"sources":"ecb","quiet":true})");
			std::cout << "refresher:    handle " << refresher.handle() << ", open handles "
			          << openrate::open_handles() << "\n";
			std::cout << "before        " << refresher.status() << "\n";

			const openrate::StringResult fetched = refresher.try_refresh(20000);
			if (!fetched.ok()) {
				std::cout << "refresh       failed: " << fetched.error() << "\n";
			} else {
				std::cout << "refresh       " << fetched.value() << "\n";
				const std::string live = engine.convert("EUR", "ZAR", 100);
				std::cout << "live          100 EUR = " << peek(live, "result") << " ZAR via "
				          << peek_path(live) << "\n";
			}
			// ~Refresher runs here, before ~Engine. Either order is fine.
		}

		// --- RAII on the throw path ---------------------------------------------
		// An engine created inside a scope that throws is still closed. This is
		// the guarantee the C example spells with a goto label.
		std::uint64_t leaked = 0;
		try {
			openrate::Engine inner(R"({"quiet":true})");
			leaked = inner.handle();
			throw std::runtime_error("something went wrong mid-block");
		} catch (const std::runtime_error &) {
			std::cout << "unwind        handle " << leaked
			          << " was closed by ~Engine during unwinding\n";
		}

		// --- closing in the "wrong" order is safe --------------------------------
		{
			openrate::Engine outer(R"({"quiet":true})");
			openrate::Refresher over(outer, R"({"sources":"ecb","quiet":true})");
			const std::uint64_t before = openrate::open_handles();
			outer.close();  // the ENGINE first, with a refresher still live
			std::cout << "order         closed the engine first: " << before << " -> "
			          << openrate::open_handles()
			          << " handles (the refresher went with it)\n";
			over.close();  // must be a harmless no-op, not a double free
		}
	} catch (const openrate::Error &e) {
		std::cerr << "openrate: " << e.what() << "\n";
		return 1;
	}

	std::cout << "\nafter close   open handles " << openrate::open_handles() << "\n";
	{
		openrate::Engine closed = openrate::Engine::try_open(R"({"quiet":true})").take();
		const std::uint64_t h = closed.handle();
		closed.close();
		closed.close();  // idempotent
		const openrate::StringResult r = closed.try_meta();
		std::cout << "use-after-close  handle " << h << ": "
		          << (r.ok() ? "the closed handle answered!" : r.error()) << "\n";
	}
	return 0;
}
