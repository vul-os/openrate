# openrate × Vulos Cloud

The hosted, multi-tenant side of openrate is **not** built in this repo. It is
absorbed into **Vulos Cloud** (`~/code/vulos/vulos-cloud`), the same way the
other Vulos products are. This repo stays a self-contained, self-hostable engine
(single Go binary + embedded UI); the cloud layer wraps it.

This file is the running TODO for that absorption. Nothing here is implemented
yet — it is the contract for what Vulos Cloud must add around the engine.

## Division of responsibility

| Concern | openrate (this repo) | Vulos Cloud |
|---|---|---|
| Rate ingestion + graph + all-pairs matrix | ✅ owns | consumes |
| JSON API + embedded UI | ✅ owns | proxies / brands |
| API keys, auth, per-project isolation | ❌ | ✅ owns |
| Usage metering + billing (per request / per plan) | ❌ | ✅ owns |
| Rate limiting / quotas / plan ceilings | 🟡 best-effort per-IP (`-ratelimit`) | ✅ owns per-key quotas + WAF/CDN |
| TLS, custom domains, CDN edge cache | ❌ | ✅ owns |
| Multi-region deploy + failover | ❌ | ✅ owns |
| Historical storage + time-series endpoints | basic / TODO | long-retention store |

## Integration approach (decided)

openrate becomes **`product=openrate`** in the Vulos Cloud control-plane (CP)
contract — the same pattern as `llm` (llmux), `office`, `mail`, `meet`. There are
two sides, and the engine stays self-contained.

**Engine side — optional CP seam (mirror `llmux/integration/cp/cp.go`).**
A separate `internal/cp` package implementing the engine's Identity / Quota /
UsageLogger interfaces against CP, wired in `main.go` ONLY when `OPENRATE_CP_URL`
is set. Standalone build never imports it → self-host stays keyless + free. When
enabled, an API request is: Bearer key → CP `GET /api/entitlements?product=openrate`
(tier + remaining quota, cached with TTL + degraded fail-bounded mode) → serve from
snapshot → CP `POST /api/usage {kind:"api_request"}`. Auth via `X-Relay-Auth`
shared secret (`OPENRATE_CP_SECRET`).

**CP side (in vulos-cloud) — reuse what already exists:**
- **API keys:** `internal/publicapi` token store (`GenerateToken`/`AuthenticateToken`/
  `RevokeToken`/`ListTokens`). Add session-gated `…/api/openrate/keys` CRUD routes.
- **Entitlements:** add an `openrate` case + `openrateLimitsForTier()` to the
  resolver in `cmd/server/routes_mailbilling.go` (Free 10k/60rpm … Business 50M/2k rpm),
  driven by `billing.Store.EffectiveTierFor` + `IsSuspended`.
- **Metering:** reuse the generic counted bucket — `billing.Store.EmitCountedEvent(
  account, "openrate:requests", n)`; quota read-back via `CountedThisMonthByBucket`.
  Monthly invoice rollup already aggregates buckets — just price `openrate:requests`.
- **Tiers:** map this repo's billing model (`vulos-cloud/billingmodel/openrate`)
  Free/Developer/Startup/Business/Enterprise onto CP tiers; keep openrate's
  request quotas in `openrateLimitsForTier()` (request-billed, not seat-billed).
- **Dashboard:** `src/pages/app/Openrate.jsx` (key create/list/revoke + usage/plan),
  following `Account.jsx`/`Billing.jsx`; sidebar link in `Layout.jsx`.

**Deploy:** engine on Fly (like other products), behind an edge cache keyed by base
currency (caps origin, most reads free — matches `billingmodel/openrate/COSTS.md`).
Flip `OPENRATE_CP_URL` on to enter cloud mode.

Phasing: (1) engine CP seam [this repo, no-op standalone] → (2) CP key CRUD +
entitlements + bucket pricing → (3) dashboard page → (4) deploy + soft-launch Free.

## TODO — cloud absorption

- [x] **Integration approach decided** — CP seam (engine) + `product=openrate` in the
      existing CP contract (keys via publicapi, usage via counted bucket). See above.
- [ ] **Engine CP seam.** Add `internal/cp` mirroring `llmux/integration/cp`; wire
      in `main.go` behind `OPENRATE_CP_URL`. Standalone stays default.
- [ ] **Auth + projects.** Reuse the Vulos Cloud project model (note: term is
      "project", not "tenant"). Issue API keys scoped to a project; map keys →
      plan.
- [ ] **Metering.** Emit a usage event per API call (project, endpoint, ts) into
      the Vulos Cloud usage pipeline. Mirror the serverless-usage + per-tier
      max-conn ceiling model used elsewhere in Vulos Cloud.
- [ ] **Plans / ceilings.** Free (daily ECB only) → paid tiers unlock SARB +
      higher refresh cadence + history. Ceiling, not reservation.
      **Billing model is done** — see `vulos-cloud/billingmodel/openrate/`
      (model.py + TIERS.md + COSTS.md): Free $0 / Developer $9 / Startup $39 /
      Business $149 / Enterprise, request-billed, ~98% margin by 1k customers,
      break-even ~29 signups. The openrate landing Pricing tab reflects it.
- [ ] **Freshness tiers as a product axis.** The engine's refresh interval is the
      lever: free = daily, paid = hourly, premium = streaming push. Gate per
      plan at the Cloud layer; the engine just takes `-refresh` / a feed handle.
- [ ] **Edge cache.** Rates change at most hourly for most plans → cache
      `/api/v1/rates` and `/convert` at the edge with short TTLs keyed by base.
- [ ] **History service.** Engine keeps the live snapshot only; Cloud persists
      daily/intraday snapshots for `?date=` and time-series queries.
- [ ] **Status + SLA.** Surface per-source freshness (already in `/meta`) on a
      public status page; alert when a source goes stale past its expected
      cadence (e.g. ECB not updated on a business day).
- [ ] **Branding.** Cloud serves the marketing site + docs; the embedded engine
      UI stays as the self-host dashboard.

## Non-goals for the engine

Keep auth, billing, quotas, and multi-region **out** of this repo. If a feature
needs a database of users or money, it belongs in Vulos Cloud.
