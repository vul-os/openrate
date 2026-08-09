// sidecar_convert.cpp — openrate as a child process, spoken to over loopback HTTP.
//
//   make sidecar_convert && OPENRATE_BINARY=../../dist/openrate ./sidecar_convert
//   ./sidecar_convert 8080          # a server you already run, on that port
//
// No library is loaded here, and that is the point: this program forks, and a
// process that has loaded the Go runtime must not. direct_convert.cpp and this
// one are deliberately separate binaries.
//
// Everything with a lifetime gets a destructor — the socket, the child process
// — so there is no cleanup path to forget, including when an exception leaves
// the middle of a request.
//
// UNLIKE direct_convert.cpp, THIS FETCHES. A server refreshes on startup and on
// its interval; that is what a server is for. For a process that provably sends
// no packets, that is direct_convert.cpp — an engine with no refresher.
//
// WHEN TO PREFER THIS:
//   - your process forks (the Go runtime does not survive fork without exec)
//   - your process installs its own SIGSEGV/SIGBUS/SIGPROF handling, or you
//     build with sanitizers or ship a crash reporter
//   - several processes should share one refreshing rate book
//   - you are not on darwin/arm64; see README.md, where the platform table is
//     narrower than you probably expect
//
// The socket code is just enough HTTP/1.1 for 127.0.0.1 with
// `Connection: close`. It is not an HTTP client: no TLS, no chunked encoding,
// no keep-alive, no redirects. A real program links libcurl or cpp-httplib.
//
// SPDX-License-Identifier: MIT OR Apache-2.0

#include <arpa/inet.h>
#include <netinet/in.h>
#include <signal.h>
#include <sys/socket.h>
#include <sys/wait.h>
#include <unistd.h>

#include <cerrno>
#include <chrono>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <functional>
#include <iostream>
#include <optional>
#include <stdexcept>
#include <string>
#include <string_view>
#include <thread>

namespace {

const char *binary_path() {
	const char *b = std::getenv("OPENRATE_BINARY");
	return (b && *b) ? b : "openrate";  // resolved on PATH by execlp
}

// Print a field for a human. NOT a JSON parser; see ../c/jsonpeek.h.
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
		if (!quoted && (doc[i] == ',' || doc[i] == '}' || doc[i] == '\n')) break;
		if (quoted && doc[i] == '\\' && i + 1 < doc.size()) i++;
		out.push_back(doc[i]);
	}
	return out;
}

// "hops" occurs exactly once per currency; "rate" also occurs inside every leg
// and quote, so counting that would over-report.
int count(std::string_view doc, std::string_view needle) {
	int n = 0;
	for (size_t at = doc.find(needle); at != std::string_view::npos;
	     at = doc.find(needle, at + needle.size()))
		n++;
	return n;
}

// ---------------------------------------------------------------------------
// Socket — a file descriptor with a destructor.
// ---------------------------------------------------------------------------

class Socket {
public:
	explicit Socket(int port) {
		fd_ = ::socket(AF_INET, SOCK_STREAM, 0);
		if (fd_ < 0) throw std::runtime_error("socket: " + std::string(std::strerror(errno)));
		sockaddr_in addr{};
		addr.sin_family = AF_INET;
		addr.sin_port = htons(static_cast<uint16_t>(port));
		::inet_pton(AF_INET, "127.0.0.1", &addr.sin_addr);
		if (::connect(fd_, reinterpret_cast<sockaddr *>(&addr), sizeof(addr)) != 0) {
			const std::string why = std::strerror(errno);
			::close(fd_);
			fd_ = -1;
			throw std::runtime_error("connect: " + why);
		}
	}
	~Socket() {
		if (fd_ >= 0) ::close(fd_);
	}
	Socket(const Socket &) = delete;
	Socket &operator=(const Socket &) = delete;

	void send_all(std::string_view data) const {
		while (!data.empty()) {
			ssize_t n = ::send(fd_, data.data(), data.size(), 0);
			if (n <= 0) {
				if (n < 0 && errno == EINTR) continue;
				throw std::runtime_error("send: " + std::string(std::strerror(errno)));
			}
			data.remove_prefix(static_cast<size_t>(n));
		}
	}

	[[nodiscard]] std::string read_all() const {
		std::string out;
		char buf[4096];
		for (;;) {
			ssize_t n = ::recv(fd_, buf, sizeof(buf), 0);
			if (n < 0) {
				if (errno == EINTR) continue;
				throw std::runtime_error("recv: " + std::string(std::strerror(errno)));
			}
			if (n == 0) return out;
			out.append(buf, static_cast<size_t>(n));
		}
	}

private:
	int fd_ = -1;
};

struct Response {
	int status = 0;
	std::string body;
};

Response http_get(int port, std::string_view path) {
	Socket sock(port);
	sock.send_all(std::string("GET ") + std::string(path) + " HTTP/1.1\r\n" +
	              "Host: 127.0.0.1:" + std::to_string(port) + "\r\n" +
	              "User-Agent: openrate-cpp-example/1\r\n" + "Accept: application/json\r\n" +
	              "Connection: close\r\n\r\n");
	const std::string raw = sock.read_all();

	Response r;
	const size_t sep = raw.find("\r\n\r\n");
	if (sep == std::string::npos) throw std::runtime_error("no header terminator in the response");
	std::sscanf(raw.c_str(), "HTTP/%*d.%*d %d", &r.status);
	r.body = raw.substr(sep + 4);
	return r;
}

int free_port() {
	int fd = ::socket(AF_INET, SOCK_STREAM, 0);
	if (fd < 0) return 0;
	sockaddr_in addr{};
	addr.sin_family = AF_INET;
	addr.sin_port = 0;
	::inet_pton(AF_INET, "127.0.0.1", &addr.sin_addr);
	int port = 0;
	socklen_t len = sizeof(addr);
	if (::bind(fd, reinterpret_cast<sockaddr *>(&addr), sizeof(addr)) == 0 &&
	    ::getsockname(fd, reinterpret_cast<sockaddr *>(&addr), &len) == 0)
		port = ntohs(addr.sin_port);
	::close(fd);
	return port;  // racy by construction; the health poll catches a lost race
}

// ---------------------------------------------------------------------------
// Sidecar — a child process with a destructor.
// ---------------------------------------------------------------------------

class Sidecar {
public:
	Sidecar() {
		port_ = free_port();
		if (port_ == 0) throw std::runtime_error("no free loopback port");

		pid_ = ::fork();
		if (pid_ < 0) throw std::runtime_error("fork: " + std::string(std::strerror(errno)));
		if (pid_ == 0) {
			// Child. The environment is inherited, so API keys for the optional
			// sources pass through exactly as for the standalone binary.
			const std::string addr = "127.0.0.1:" + std::to_string(port_);
			::setenv("OPENRATE_ADDR", addr.c_str(), 1);
			// One fast source keeps the example quick; the default is four.
			if (!std::getenv("OPENRATE_SOURCES")) ::setenv("OPENRATE_SOURCES", "ecb", 1);
			// The binary limits /api/ to 120 requests a minute per IP by
			// default — anti-scraping for a public deployment. This child
			// listens on loopback and serves exactly one client, this process,
			// so there is no stranger to throttle; meanwhile a legitimate batch
			// of conversions would sail past 120/min and take a 429 from our
			// own sidecar. Set OPENRATE_RATELIMIT yourself to put it back.
			if (!std::getenv("OPENRATE_RATELIMIT")) ::setenv("OPENRATE_RATELIMIT", "0", 1);
			::execlp(binary_path(), binary_path(), nullptr);
			std::cerr << "exec " << binary_path() << ": " << std::strerror(errno) << "\n";
			::_exit(127);
		}

		if (!wait_healthy(std::chrono::seconds(15))) {
			stop();  // the destructor would too, but a throw should not leave a child running
			throw std::runtime_error("openrate did not become healthy on 127.0.0.1:" +
			                         std::to_string(port_));
		}
	}

	~Sidecar() { stop(); }
	Sidecar(const Sidecar &) = delete;
	Sidecar &operator=(const Sidecar &) = delete;

	[[nodiscard]] int port() const noexcept { return port_; }
	[[nodiscard]] pid_t pid() const noexcept { return pid_; }

	/// Idempotent and noexcept: it runs from the destructor, possibly while an
	/// exception is in flight.
	void stop() noexcept {
		if (pid_ <= 0) return;
		::kill(pid_, SIGTERM);
		for (int i = 0; i < 100; i++) {  // up to 5s for a clean shutdown
			if (::waitpid(pid_, nullptr, WNOHANG) == pid_) {
				pid_ = -1;
				return;
			}
			std::this_thread::sleep_for(std::chrono::milliseconds(50));
		}
		::kill(pid_, SIGKILL);
		::waitpid(pid_, nullptr, 0);
		pid_ = -1;
	}

private:
	bool wait_healthy(std::chrono::seconds budget) const {
		const auto deadline = std::chrono::steady_clock::now() + budget;
		while (std::chrono::steady_clock::now() < deadline) {
			try {
				if (http_get(port_, "/healthz").status == 200) return true;
			} catch (const std::exception &) {
				// not up yet
			}
			std::this_thread::sleep_for(std::chrono::milliseconds(50));
		}
		return false;
	}

	int port_ = 0;
	pid_t pid_ = -1;
};

// Turn a /readyz 503 body into the one line a caller can act on: the server's
// own `reason`, then `name: last_error` for every source that has one.
//
// `last_error` is `omitempty`, so a source nobody has tried yet has no such key
// — printing "ecb: " for it would be worse than printing nothing, and a body
// with no errors in it degrades to the reason alone.
//
// The array is walked object by object because "name" and "last_error" have to
// be read as a PAIR: a document-wide peek for one finds a different source's
// than a document-wide peek for the other. No field of a source is itself an
// object or an array, so the next `}` bounds each one and the first `]` bounds
// the array.
std::string describe_not_ready(std::string_view body) {
	std::string out = peek(body, "reason");
	if (out.empty()) out = "not ready, and it did not say why";

	const size_t npos = std::string_view::npos;
	const size_t key = body.find("\"sources\"");
	const size_t first = key == npos ? npos : body.find('[', key);
	const size_t end = first == npos ? npos : body.find(']', first);
	bool printed = false;
	for (size_t p = first; p != npos && end != npos && p < end;) {
		const size_t open = body.find('{', p);
		if (open == npos || open >= end) break;
		const size_t close = body.find('}', open);
		if (close == npos || close > end) break;

		const std::string_view source = body.substr(open, close - open + 1);
		const std::string err = peek(source, "last_error");
		if (!err.empty()) {
			const std::string name = peek(source, "name");
			out += (printed ? "; " : " (") + (name.empty() ? "?" : name) + ": " + err;
			printed = true;
		}
		p = close + 1;
	}
	if (printed) out += ")";
	return out;
}

// "Live" and "useful" are different questions. /healthz answers 200 the moment
// the listener binds, with an empty book behind it; /readyz answers 200 only
// once the snapshot holds a currency, and 503 with a JSON body saying why not.
// Waiting on the wrong one is how this example used to convert against nothing
// and report every pair as an unknown currency.
//
// Neither endpoint is under /api/, and the rate limiter guards only /api/, so a
// flat short interval is free — there is no limiter here to back off from. The
// workaround this replaces polled /api/v1/meta, which there was.
//
// `why` ends up holding the most useful explanation seen: the last 503's if
// there ever was one, otherwise the transport error. A server that said
// "ecb: connection refused" explains more than a socket that later stopped
// answering, so a transport error never displaces a reason the server gave.
bool wait_ready(int port, std::chrono::seconds budget, std::string &why) {
	const auto deadline = std::chrono::steady_clock::now() + budget;
	bool explained = false;
	why = "openrate never answered /readyz";
	while (std::chrono::steady_clock::now() < deadline) {
		try {
			// http_get returns the body whatever the status, which is the only
			// reason this can be specific rather than a bare timeout.
			const Response r = http_get(port, "/readyz");
			if (r.status == 200) return true;
			why = describe_not_ready(r.body);
			explained = true;
		} catch (const std::exception &e) {
			if (!explained) why = e.what();  // not listening (yet, or any more)
		}
		std::this_thread::sleep_for(std::chrono::milliseconds(150));
	}
	return false;
}

}  // namespace

int main(int argc, char **argv) {
	try {
		// Only the port is taken from the command line: this example's HTTP
		// client speaks to 127.0.0.1 and nothing else, on purpose.
		const bool managed = argc <= 1;
		// std::optional so the unmanaged case constructs no Sidecar at all —
		// and so the managed one is still destroyed on every path out of here.
		std::optional<Sidecar> sidecar;
		int port;
		if (managed) {
			sidecar.emplace();
			port = sidecar->port();
			std::cout << "base url:     http://127.0.0.1:" << port << "  (managed, pid "
			          << sidecar->pid() << ")\n";
		} else {
			port = std::atoi(argv[1]);
			std::cout << "base url:     http://127.0.0.1:" << port
			          << "  (not managed — spawning nothing)\n";
		}

		std::string why;
		if (!wait_ready(port, std::chrono::seconds(30), why)) {
			std::cerr << "openrate has no rates after 30s: " << why << "\n";
			std::cout << "\nThe server is listening but its book is empty, and everything below\n"
			             "needs a rate to exist. For a mode that needs no network at all, see\n"
			             "direct_convert.cpp.\n";
			return 1;
		}

		{
			const Response r = http_get(port, "/api/v1/meta");
			std::cout << "meta          HTTP " << r.status << ", default base "
			          << peek(r.body, "default_base") << ", first source " << peek(r.body, "name")
			          << " with " << peek(r.body, "edges") << " edges\n";
		}

		{
			const Response r = http_get(port, "/api/v1/convert?from=USD&to=ZAR&amount=100");
			// "from" is the first occurrence and "to" the last, because Go
			// marshals keys in sorted order and the rate path's legs sit in
			// between. A JSON parser deletes this sentence; see ../c/jsonpeek.h.
			std::cout << "convert       HTTP " << r.status << ", " << peek(r.body, "amount") << " "
			          << peek(r.body, "from") << " = " << peek(r.body, "result") << " "
			          << peek(r.body, "to", /*last=*/true) << "\n";
			std::cout << "              " << peek(r.body, "hops") << " hop(s), quality "
			          << peek(r.body, "grade") << ", " << peek(r.body, "age_sec") << "s old\n";
		}

		{
			const Response r = http_get(port, "/api/v1/rates?base=ZAR");
			std::cout << "rates         HTTP " << r.status << ", " << count(r.body, "\"hops\":")
			          << " currencies against ZAR\n";
		}

		// The same wire contract, a different shape of failure: an HTTP status
		// and a JSON error body, rather than a char** err carrying plain text.
		{
			const Response r = http_get(port, "/api/v1/convert?from=USD&to=XXX");
			std::cout << "error         HTTP " << r.status << ": " << peek(r.body, "error") << "\n";
		}

		// An unknown BASE, though, answers 200 with an empty book — where direct
		// mode raises. That is the one deliberate difference between the modes.
		{
			const Response r = http_get(port, "/api/v1/rates?base=XXX");
			std::cout << "unknown base  HTTP " << r.status << " with " << count(r.body, "\"hops\":")
			          << " rates (direct mode errors instead — the one\n"
			          << "              deliberate difference between the two modes)\n";
		}

		std::cout << "\n" << (managed ? "sidecar stopped" : "left the remote server alone") << "\n";
		// ~Sidecar runs here, and is a no-op when we already stopped it.
	} catch (const std::exception &e) {
		std::cerr << "sidecar_convert: " << e.what() << "\n";
		return 1;
	}
	return 0;
}
