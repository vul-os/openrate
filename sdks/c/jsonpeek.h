/*
 * jsonpeek.h — pull a couple of fields out of a JSON document, for printing.
 *
 * THIS IS NOT A JSON PARSER. It scans for `"key":` and reads what follows,
 * understanding \" and \\ and nothing else. It does not validate, does not
 * handle nesting, does not know that a key inside a string literal is not a
 * key, and does not decode \uXXXX. It is here so the examples can print an
 * answer without dragging a parser into a directory whose subject is the ABI.
 *
 * A real program links a real parser — cJSON, jansson, yyjson — and openrate's
 * responses are ordinary JSON, so any of them works unchanged.
 *
 * SPDX-License-Identifier: MIT OR Apache-2.0
 */

#ifndef JSONPEEK_H
#define JSONPEEK_H

#include <stddef.h>

/*
 * Copy the string value of the first `"key":"..."` into out (always
 * NUL-terminated). Returns 1 if found, 0 otherwise.
 */
int json_string(const char *doc, const char *key, char *out, size_t outcap);

/*
 * Append the string value of EVERY `"key":"..."` to out, in document order.
 * openrate has no streaming operation; this collects every value for a
 * repeated key out of one response document.
 * Returns the number of matches appended.
 */
int json_string_append_all(const char *doc, const char *key, char *out, size_t outcap);

/*
 * Copy the value of the LAST `"key":"..."` in the document into out.
 *
 * openrate nests: a convert response has a top-level "to" and another inside
 * every leg of the rate path, and Go marshals object keys in sorted order, so
 * the top-level one comes last. Scanning forwards would read a leg's.
 */
int json_string_last(const char *doc, const char *key, char *out, size_t outcap);

/*
 * Read the first `"key":<number>` into *out. Returns 1 if found, 0 otherwise.
 *
 * Pass a pointer INTO the document to scan from there rather than from the
 * start — how the examples reach the number inside the "rate" object, whose
 * key is also "rate".
 */
int json_number(const char *doc, const char *key, double *out);

/*
 * How many times `"key":` appears. The examples use it to count currencies by
 * counting a field that occurs exactly once per currency.
 */
int json_count(const char *doc, const char *key);

#endif /* JSONPEEK_H */
