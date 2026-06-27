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
// and must never be logged.
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

// queryParam matches a single name=value pair inside a query string, including
// its leading ? or & separator so the separator can be preserved on rewrite.
var queryParam = regexp.MustCompile(`([?&])([A-Za-z0-9_.\-]+)=([^&\s"'\\]*)`)

// Query masks the values of sensitive query parameters wherever they appear in
// s, leaving everything else (host, path, non-secret params) intact. It works on
// raw strings so it sanitizes URLs no matter how they are embedded (e.g. inside
// a *url.Error message such as `Get "https://h/p?apiKey=abc": dial tcp …`).
func Query(s string) string {
	return queryParam.ReplaceAllStringFunc(s, func(m string) string {
		sub := queryParam.FindStringSubmatch(m)
		if sensitive[strings.ToLower(sub[2])] {
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
