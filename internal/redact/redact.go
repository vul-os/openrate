// Package redact masks secrets that would otherwise leak into logs or
// error-bearing API fields. Paid source fetchers carry their credentials in the
// request URL (e.g. ?apiKey=…), and net/http surfaces failures as *url.Error
// whose message embeds that full URL — so a transient dial/timeout error logged
// verbatim would print the key. Sanitize such errors before they are recorded.
package redact

import (
	"errors"
	"regexp"
	"strings"
)

// sensitive lists query-parameter names (lower-cased) whose values are secrets
// and must never be logged. It covers the exact names the shipped adapters use
// — app_id (oxr), apiKey (polygon), apikey (twelvedata), api_key (tradermade
// and FRED) — plus the common spellings a future or third-party Source is
// likely to reach for.
var sensitive = map[string]bool{
	"apikey":       true,
	"api_key":      true,
	"key":          true,
	"token":        true,
	"access_token": true,
	"auth":         true,
	"appid":        true,
	"app_id":       true,
	"secret":       true,
	"password":     true,
	"passwd":       true,
	"pwd":          true,
}

// sensitiveSubstrings catch the spellings an exact list keeps missing:
// access-key, client_secret, subscription_key, x-api-key, refresh_token,
// authorization. fxsource.Source is a public interface, so the parameter names
// this has to survive are not only the ones openrate ships — and the cost of
// over-redacting a diagnostic string is nil next to publishing a credential at
// /readyz. Every non-secret parameter the shipped adapters send (symbol,
// currency, series_id, file_type, limit, sort_order, interval, range,
// observation_start, startPeriod) is unaffected.
var sensitiveSubstrings = []string{"key", "token", "secret", "pass", "auth", "credential"}

// isSensitive reports whether a query-parameter name names a secret.
func isSensitive(name string) bool {
	n := strings.ToLower(name)
	if sensitive[n] {
		return true
	}
	for _, sub := range sensitiveSubstrings {
		if strings.Contains(n, sub) {
			return true
		}
	}
	return false
}

// userinfo matches the credential half of a URL's userinfo component —
// scheme://user:password@host. net/url does NOT mask this on the way out:
// url.Error embeds the URL string it was given, so a Source built against
// https://user:secret@host/… would publish that password verbatim into
// Status.LastError, which /readyz and /api/v1/meta serve to unauthenticated
// callers. No shipped adapter uses this shape; the pattern is here so that the
// first one that does is not a disclosure.
var userinfo = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.\-]*://[^/?#\s:@"']*):([^/?#\s@"']*)@`)

// queryParam matches a single name=value pair inside a query string, including
// its leading ? or & separator so the separator can be preserved on rewrite.
var queryParam = regexp.MustCompile(`([?&])([A-Za-z0-9_.\-]+)=([^&\s"'\\]*)`)

// Query masks the values of sensitive query parameters, and the password half of
// any URL userinfo, wherever they appear in s — leaving everything else (scheme,
// host, path, non-secret params) intact, because the point of recording the
// error at all is to say which source failed and why. It works on raw strings so
// it sanitizes URLs no matter how they are embedded (e.g. inside a *url.Error
// message such as `Get "https://h/p?apiKey=abc": dial tcp …`).
func Query(s string) string {
	s = userinfo.ReplaceAllString(s, "$1:REDACTED@")
	return queryParam.ReplaceAllStringFunc(s, func(m string) string {
		sub := queryParam.FindStringSubmatch(m)
		if isSensitive(sub[2]) {
			return sub[1] + sub[2] + "=REDACTED"
		}
		return m
	})
}

// Error returns a copy of err whose message has sensitive query parameters
// masked, suitable for logging or exposing in an API field. nil in, nil out.
// The original error is not modified and its chain is intentionally dropped so
// callers cannot accidentally unwrap back to the unredacted message.
func Error(err error) error {
	if err == nil {
		return nil
	}
	return errors.New(Query(err.Error()))
}
