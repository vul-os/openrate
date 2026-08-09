// openrate.hpp — an idiomatic C++ wrapper over the openrate C ABI.
//
// Header-only, C++17, no dependencies beyond the standard library and
// <openrate.h>. Link against libopenrate.
//
//     #include "openrate.hpp"
//
//     openrate::Engine engine(R"({"base":"ZAR"})");   // throws on failure
//     engine.load(R"({"edges":[{"from":"USD","to":"ZAR","rate":18.5}]})");
//     std::string answer = engine.convert("USD", "ZAR", 100);
//                                                     // ~Engine closes the handle
//
// TWO TYPES, BECAUSE THERE ARE TWO KINDS OF HANDLE, AND THE DIFFERENCE IS
// OPENRATE'S WHOLE DESIGN.
//
//   openrate::Engine     computes. Constructing one starts no thread, opens no
//                        socket, reads no environment variable and sends no
//                        packet. Feed it with load().
//   openrate::Refresher  fetches. A separate construction over an engine, with
//                        its own handle. It is the only thing here that can
//                        touch the network, and building it still does not:
//                        refresh() and start() are what fetch.
//
// They are separate C++ types rather than one class with a flag on purpose: a
// function taking `const Engine&` cannot fetch, and that is checked by the
// compiler rather than promised in a comment. The ABI enforces the same thing
// underneath — an engine handle refuses the "refresh" method.
//
// There is no stream(). openrate answers from a snapshot it already holds, so
// there is no incremental operation to stream, and there is no openrate_stream
// in the C ABI either. llmux, which shares this ABI shape, has one.
//
// WHAT THIS WRAPPER IS FOR. The C ABI hands out two kinds of resource — a
// uint64 handle and malloc'd char* strings that only openrate_free may release
// — and both are easy to leak on an error path, which in C++ means any path,
// because any line can throw. So:
//
//   * Every char* the library returns is owned by openrate::OwnedString the
//     instant it comes back, INCLUDING the error message we are about to throw.
//   * Handles are owned by move-only types that close in their destructors.
//     Both destructors are noexcept and openrate_close is idempotent, so
//     double-close and close-during-unwind are both fine — as is closing an
//     Engine before a Refresher built over it, which the library handles by
//     releasing the refresher with the engine.
//
// ERROR STYLE. Both, and neither is a second implementation:
//
//   engine.convert(...)      throws openrate::Error with the library's message.
//   engine.try_convert(...)  returns openrate::Result<std::string>, never throws.
//
// Define OPENRATE_NO_EXCEPTIONS (or build with -fno-exceptions, which defines
// it for you) to compile only the try_ layer.
//
// SPDX-License-Identifier: MIT OR Apache-2.0

#ifndef OPENRATE_HPP
#define OPENRATE_HPP

#include <cstdint>
#include <optional>
#include <string>
#include <string_view>
#include <utility>

#include "openrate.h"

#if !defined(OPENRATE_NO_EXCEPTIONS) && !defined(__cpp_exceptions)
#define OPENRATE_NO_EXCEPTIONS 1
#endif

#ifndef OPENRATE_NO_EXCEPTIONS
#include <stdexcept>
#endif

namespace openrate {

// ---------------------------------------------------------------------------
// OwnedString — a char* the library allocated, released with openrate_free.
// ---------------------------------------------------------------------------

class OwnedString {
public:
	OwnedString() noexcept = default;
	explicit OwnedString(char *p) noexcept : p_(p) {}

	~OwnedString() { reset(); }

	OwnedString(OwnedString &&other) noexcept : p_(other.release()) {}
	OwnedString &operator=(OwnedString &&other) noexcept {
		if (this != &other) {
			reset();
			p_ = other.release();
		}
		return *this;
	}

	OwnedString(const OwnedString &) = delete;
	OwnedString &operator=(const OwnedString &) = delete;

	[[nodiscard]] const char *c_str() const noexcept { return p_; }
	[[nodiscard]] explicit operator bool() const noexcept { return p_ != nullptr; }
	[[nodiscard]] std::string str() const { return p_ ? std::string(p_) : std::string(); }

	char *release() noexcept {
		char *p = p_;
		p_ = nullptr;
		return p;
	}

	void reset() noexcept {
		if (p_) {
			openrate_free(p_);  // openrate_free(nullptr) is safe; the branch is for clarity
			p_ = nullptr;
		}
	}

	/// Address to hand to a `char** err` out-parameter. Whatever the library
	/// writes there is owned from that moment on.
	char **err_slot() noexcept {
		reset();
		return &p_;
	}

private:
	char *p_ = nullptr;
};

// ---------------------------------------------------------------------------
// Result — the non-throwing return shape.
// ---------------------------------------------------------------------------

/// An expected-like value. `error()` is the library's message: plain UTF-8
/// text, not JSON. Do not parse it.
///
/// The value is held in a std::optional rather than as a plain member, and that
/// is not a style choice: a failure must not construct a T. With a plain member,
/// `Result<Engine>::failure(...)` would default-construct an Engine — opening a
/// real engine on the failure path, from inside the code reporting that an
/// engine could not be opened.
template <typename T> class Result {
public:
	[[nodiscard]] static Result success(T v) {
		Result r;
		r.value_.emplace(std::move(v));
		return r;
	}
	[[nodiscard]] static Result failure(std::string message) {
		Result r;
		r.error_ = message.empty() ? std::string("openrate failed without a message")
		                           : std::move(message);
		return r;
	}

	[[nodiscard]] bool ok() const noexcept { return error_.empty(); }
	[[nodiscard]] explicit operator bool() const noexcept { return ok(); }
	[[nodiscard]] const std::string &error() const noexcept { return error_; }

	/// Precondition: ok(). Reading it otherwise is a programming error.
	[[nodiscard]] const T &value() const & { return *value_; }
	[[nodiscard]] T take() { return std::move(*value_); }

private:
	std::optional<T> value_;
	std::string error_;
};

using StringResult = Result<std::string>;

#ifndef OPENRATE_NO_EXCEPTIONS
class Error : public std::runtime_error {
public:
	explicit Error(const std::string &message) : std::runtime_error(message) {}
};

namespace detail {
template <typename T> T unwrap(Result<T> r) {
	if (!r.ok()) throw Error(r.error());
	return r.take();
}
}  // namespace detail
#endif

// ---------------------------------------------------------------------------

/// The openrate version the loaded library was built from. Static storage
/// inside the library: never freed.
///
/// Compare it against OPENRATE_ABI_VERSION, which the header compiled into your
/// program. A mismatch means a stale libopenrate is earlier on your load path.
[[nodiscard]] inline std::string abi_version() {
	const char *v = openrate_abi_version();
	return v ? std::string(v) : std::string();
}

/// True when the loaded library matches the header this was built against.
[[nodiscard]] inline bool abi_version_matches() {
	return abi_version() == std::string(OPENRATE_ABI_VERSION);
}

/// How many handles the library currently holds. Diagnostic only — for a host
/// test suite asserting it closed what it opened.
[[nodiscard]] inline std::uint64_t open_handles() { return openrate_open_handles(); }

// ---------------------------------------------------------------------------
// Handle — the lifecycle both kinds share.
// ---------------------------------------------------------------------------

namespace detail {

class Handle {
public:
	~Handle() { close(); }

	Handle(Handle &&other) noexcept : h_(other.h_) { other.h_ = 0; }
	Handle &operator=(Handle &&other) noexcept {
		if (this != &other) {
			close();
			h_ = other.h_;
			other.h_ = 0;
		}
		return *this;
	}

	Handle(const Handle &) = delete;
	Handle &operator=(const Handle &) = delete;

	[[nodiscard]] std::uint64_t handle() const noexcept { return h_; }
	[[nodiscard]] bool is_open() const noexcept { return h_ != 0; }

	/// Idempotent, and noexcept: it runs from the destructor, possibly during
	/// stack unwinding.
	void close() noexcept {
		if (h_ != 0) {
			openrate_close(h_);
			h_ = 0;
		}
	}

	/// One call against this handle. `request_json` may be nullptr, meaning {}.
	[[nodiscard]] StringResult try_call(const char *method, const char *request_json) const {
		if (h_ == 0) return StringResult::failure("handle is closed");
		OwnedString err;
		OwnedString out(openrate_call(h_, method, request_json, err.err_slot()));
		if (!out) return StringResult::failure(err.str());
		return StringResult::success(out.str());
	}

#ifndef OPENRATE_NO_EXCEPTIONS
	std::string call(const char *method, const char *request_json) const {
		return detail::unwrap(try_call(method, request_json));
	}
#endif

protected:
	explicit Handle(std::uint64_t h) noexcept : h_(h) {}

private:
	std::uint64_t h_ = 0;
};

/// Build {"from":"USD","to":"ZAR","amount":100} without a JSON library. The
/// currency codes are emitted verbatim, so pass codes — not user input.
inline std::string convert_request(std::string_view from, std::string_view to, double amount) {
	std::string out = "{";
	if (!from.empty()) out += "\"from\":\"" + std::string(from) + "\",";
	if (!to.empty()) out += "\"to\":\"" + std::string(to) + "\",";
	out += "\"amount\":" + std::to_string(amount) + "}";
	return out;
}

}  // namespace detail

// ---------------------------------------------------------------------------
// Engine — computes. Cannot fetch.
// ---------------------------------------------------------------------------

class Refresher;

/// A conversion engine. Move-only; closes on destruction.
///
/// Constructing one starts no thread, opens no socket, reads no environment
/// variable and sends no packet. It answers from the snapshot it holds and says
/// "unknown or unreachable currency pair" until load(), or a Refresher, gives
/// it one.
///
/// Every method here is const, and that is not decoration: an Engine reference
/// is a capability to COMPUTE, and no amount of it adds up to a capability to
/// fetch. For that you need a Refresher, which needs a non-const Engine to
/// build.
class Engine : public detail::Handle {
public:
	/// Non-throwing construction. Check .ok() before using .value.
	[[nodiscard]] static Result<Engine> try_open(const char *config_json = nullptr) {
		OwnedString err;
		std::uint64_t h = openrate_new(config_json, err.err_slot());
		if (h == 0) return Result<Engine>::failure(err.str());
		return Result<Engine>::success(Engine(h));
	}

#ifndef OPENRATE_NO_EXCEPTIONS
	/// Throws openrate::Error if the engine cannot be created.
	/// config_json: {"base":"ZAR","quiet":false}
	explicit Engine(const char *config_json = nullptr)
	    : Engine(detail::unwrap(try_open(config_json))) {}
	explicit Engine(const std::string &config_json) : Engine(config_json.c_str()) {}
#endif

	// -- non-throwing ------------------------------------------------------

	[[nodiscard]] StringResult try_convert(std::string_view from, std::string_view to,
	                                       double amount = 1) const {
		return try_call("convert", detail::convert_request(from, to, amount).c_str());
	}
	[[nodiscard]] StringResult try_rates(std::string_view base = {}) const {
		const std::string req = base.empty() ? "{}" : "{\"base\":\"" + std::string(base) + "\"}";
		return try_call("rates", req.c_str());
	}
	[[nodiscard]] StringResult try_meta() const { return try_call("meta", "{}"); }
	[[nodiscard]] StringResult try_load(const char *request_json) const {
		return try_call("load", request_json);
	}

#ifndef OPENRATE_NO_EXCEPTIONS
	// -- throwing ----------------------------------------------------------

	/// Identical to GET /api/v1/convert. An omitted currency means the engine's
	/// default base.
	std::string convert(std::string_view from, std::string_view to, double amount = 1) const {
		return detail::unwrap(try_convert(from, to, amount));
	}
	/// Identical to GET /api/v1/rates. rates[X].rate reads as
	/// "1 base = rate units of X". An unknown base is an error here, where the
	/// HTTP endpoint answers 200 with an empty book — the one deliberate
	/// difference between the modes, and it follows the Go library.
	std::string rates(std::string_view base = {}) const {
		return detail::unwrap(try_rates(base));
	}
	/// Identical to GET /api/v1/meta. "sources" is [] for an engine nobody
	/// refreshes.
	std::string meta() const { return detail::unwrap(try_meta()); }
	/// Install rates you obtained yourself. The zero-network path; no HTTP
	/// counterpart, because the server is read-only.
	///   {"edges":[{"from","to","rate","source","time"}], "built_at": ...}
	std::string load(const char *request_json) const {
		return detail::unwrap(try_load(request_json));
	}
	std::string load(const std::string &request_json) const { return load(request_json.c_str()); }
#endif

	/// Build a Refresher over this engine. Defined below, once Refresher is a
	/// complete type.
	[[nodiscard]] inline Result<Refresher> try_refresher(const char *config_json = nullptr);
#ifndef OPENRATE_NO_EXCEPTIONS
	[[nodiscard]] inline Refresher refresher(const char *config_json = nullptr);
#endif

private:
	explicit Engine(std::uint64_t h) noexcept : detail::Handle(h) {}
};

// ---------------------------------------------------------------------------
// Refresher — fetches. The only thing here that touches the network.
// ---------------------------------------------------------------------------

/// Move-only; closes on destruction. Constructing one still opens nothing:
/// refresh() and start() are what fetch.
///
/// It does NOT keep the engine alive — the C ABI's registry does the
/// bookkeeping, and closing the engine releases every refresher over it, so
/// closing in the "wrong" order cannot leak a running loop. Do keep the Engine
/// object in scope for as long as you want to use it, as you would anyway.
class Refresher : public detail::Handle {
public:
	[[nodiscard]] static Result<Refresher> try_open(const Engine &engine,
	                                                const char *config_json = nullptr) {
		if (!engine.is_open())
			return Result<Refresher>::failure("cannot build a refresher over a closed engine");
		OwnedString err;
		std::uint64_t h = openrate_refresher_new(engine.handle(), config_json, err.err_slot());
		if (h == 0) return Result<Refresher>::failure(err.str());
		return Result<Refresher>::success(Refresher(h));
	}

#ifndef OPENRATE_NO_EXCEPTIONS
	/// config_json: {"sources":"ecb,coinbase","interval_ms":3600000,
	///               "fetch_timeout_ms":50000,"quiet":false}
	explicit Refresher(const Engine &engine, const char *config_json = nullptr)
	    : Refresher(detail::unwrap(try_open(engine, config_json))) {}
#endif

	// -- non-throwing ------------------------------------------------------

	/// Per-source fetch status. Opens nothing.
	[[nodiscard]] StringResult try_status() const { return try_call("status", "{}"); }
	/// One synchronous fetch of every configured source. THIS OPENS SOCKETS.
	[[nodiscard]] StringResult try_refresh(int timeout_ms = 0) const {
		const std::string req =
		    timeout_ms > 0 ? "{\"timeout_ms\":" + std::to_string(timeout_ms) + "}" : "{}";
		return try_call("refresh", req.c_str());
	}
	/// Start the background loop. The only thread this library starts itself.
	[[nodiscard]] StringResult try_start() const { return try_call("start", "{}"); }
	/// Stop it and wait for it to exit.
	[[nodiscard]] StringResult try_stop() const { return try_call("stop", "{}"); }
	/// Block until the engine holds at least one currency. Does not fetch:
	/// something must be refreshing, or it waits.
	[[nodiscard]] StringResult try_ready(int timeout_ms = 0) const {
		const std::string req =
		    timeout_ms > 0 ? "{\"timeout_ms\":" + std::to_string(timeout_ms) + "}" : "{}";
		return try_call("ready", req.c_str());
	}

#ifndef OPENRATE_NO_EXCEPTIONS
	std::string status() const { return detail::unwrap(try_status()); }
	std::string refresh(int timeout_ms = 0) const { return detail::unwrap(try_refresh(timeout_ms)); }
	std::string start() const { return detail::unwrap(try_start()); }
	std::string stop() const { return detail::unwrap(try_stop()); }
	std::string ready(int timeout_ms = 0) const { return detail::unwrap(try_ready(timeout_ms)); }
#endif

private:
	explicit Refresher(std::uint64_t h) noexcept : detail::Handle(h) {}
};

inline Result<Refresher> Engine::try_refresher(const char *config_json) {
	return Refresher::try_open(*this, config_json);
}

#ifndef OPENRATE_NO_EXCEPTIONS
inline Refresher Engine::refresher(const char *config_json) {
	return Refresher(*this, config_json);
}
#endif

}  // namespace openrate

#endif  // OPENRATE_HPP
