# Security Policy

openrate is a self-hosted multi-currency FX rate service: it fetches, caches and
serves exchange rates that other software converts money with. Because a wrong
or tampered rate has direct financial impact, its integrity boundary matters.
Reports are taken seriously and handled with priority.

## Reporting a vulnerability

**Please do not open a public issue for security problems.**

- Preferred: [GitHub private vulnerability reporting](https://github.com/vul-os/openrate/security/advisories/new) on `vul-os/openrate`.
- Alternatively, email **vulosorg@gmail.com** with `[openrate security]` in the subject.

Include what you can: affected area (a rate source/adapter, the cache, the
served API, decimal parsing), reproduction steps, and impact as you understand
it. You'll get an acknowledgement within **72 hours** and a status update at
least every **14 days** until resolution. Please give a reasonable window to ship
a fix before public disclosure — we'll credit you in the release notes unless
you'd rather stay anonymous.

## Scope

Especially interested in:

- **Rate integrity** — any path that lets a served rate be tampered with,
  spoofed, or silently served stale without its provenance/staleness reflecting
  it (a downstream product converts money on this).
- **Source authentication** — a malicious upstream or man-in-the-middle injecting
  rates the service accepts as genuine.
- **Numeric correctness as integrity** — float contamination, rounding, or
  parsing flaws that change a converted amount.
- **Egress** — any network call beyond the rate sources the operator configured.

Out of scope: vulnerabilities requiring an already-compromised host, and issues
in the third-party rate providers the operator configures.

## Supported versions

Only the latest release (and `main`) receives fixes.
